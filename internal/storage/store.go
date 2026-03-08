package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store wraps a SQLite database for conversation persistence.
type Store struct {
	db *sql.DB
}

// Session represents a single saved conversation.
type Session struct {
	ID          int64
	ProjectID   int64
	Title       string
	LastSummary string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MessageRecord captures a single message for persistence.
type MessageRecord struct {
	Role      string
	Content   string
	Display   string
	Model     string
	CreatedAt time.Time
}

const (
	defaultDBDirName  = ".shinobi"
	defaultDBFileName = "conversations.db"
)

// DefaultProjectName is used when no project exists yet.
const DefaultProjectName = "General"

// Project represents a logical grouping of conversations.
type Project struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProjectStats provides aggregate info for menu listings.
type ProjectStats struct {
	Project
	ConversationCount int
	LastConversation  time.Time
}

// SettingScope identifies whether a preference targets the whole user or a specific project.
type SettingScope string

const (
	// SettingScopeUser stores preferences that apply globally for the current user.
	SettingScopeUser SettingScope = "user"
	// SettingScopeProject stores preferences scoped to a single project.
	SettingScopeProject SettingScope = "project"
)

func (s SettingScope) valid() bool {
	switch s {
	case SettingScopeUser, SettingScopeProject:
		return true
	default:
		return false
	}
}

// Preference keys used throughout the UI layer.
const (
	SettingKeyShowStatusBar  = "ui.showStatusBar"
	SettingKeyShowTimestamps = "ui.showTimestamps"
	SettingKeyDefaultModel   = "ui.defaultModel"
	SettingKeyDefaultAgent   = "ui.defaultAgent"
)

// OpenDefault tries a series of writable locations and returns the first
// successful store handle.
func OpenDefault() (*Store, error) {
	paths := candidateDBPaths()
	var errs []error
	for _, path := range paths {
		store, err := Open(path)
		if err == nil {
			return store, nil
		}
		fmt.Fprintf(os.Stderr, "Warning: unable to open conversation store at %s: %v\n", path, err)
		errs = append(errs, fmt.Errorf("%s: %w", path, err))
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("no database paths attempted")
	}
	return nil, errors.Join(errs...)
}

// Open opens (and creates if needed) the SQLite database at the given path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	if err := configureDB(db); err != nil {
		db.Close()
		return nil, err
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// CreateSession creates a new empty conversation row for a project.
func (s *Store) CreateSession(projectID int64, label string) (Session, error) {
	if s == nil {
		return Session{}, errors.New("store is nil")
	}
	if projectID == 0 {
		return Session{}, errors.New("project id is required")
	}
	if label == "" {
		label = fmt.Sprintf("Session %s", time.Now().Format("2006-01-02 15:04"))
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO conversations (project_id, title, last_summary, created_at, updated_at)
	        VALUES (?, ?, '', ?, ?)`, projectID, label, formatTime(now), formatTime(now))
	if err != nil {
		return Session{}, fmt.Errorf("insert conversation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Session{}, fmt.Errorf("fetch conversation id: %w", err)
	}
	return Session{
		ID:          id,
		ProjectID:   projectID,
		Title:       label,
		LastSummary: "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// RecentSessions returns the most recently updated sessions.
func (s *Store) RecentSessions(limit int) ([]Session, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`SELECT id, project_id, title, last_summary, created_at, updated_at
        FROM conversations
        ORDER BY updated_at DESC
        LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	sessions := []Session{}
	for rows.Next() {
		var (
			id          int64
			projectID   sql.NullInt64
			title       string
			lastSummary string
			createdStr  string
			updatedStr  string
		)
		if err := rows.Scan(&id, &projectID, &title, &lastSummary, &createdStr, &updatedStr); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, Session{
			ID:          id,
			ProjectID:   nullInt64(projectID),
			Title:       title,
			LastSummary: lastSummary,
			CreatedAt:   parseTime(createdStr),
			UpdatedAt:   parseTime(updatedStr),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// GetSession returns metadata for a single session.
func (s *Store) GetSession(id int64) (Session, error) {
	if s == nil {
		return Session{}, errors.New("store is nil")
	}
	row := s.db.QueryRow(`SELECT id, project_id, title, last_summary, created_at, updated_at
        FROM conversations WHERE id = ?`, id)
	var (
		sessID     int64
		projectID  sql.NullInt64
		title      string
		summary    string
		createdStr string
		updatedStr string
	)
	if err := row.Scan(&sessID, &projectID, &title, &summary, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, fmt.Errorf("session %d not found", id)
		}
		return Session{}, err
	}
	return Session{
		ID:          sessID,
		ProjectID:   nullInt64(projectID),
		Title:       title,
		LastSummary: summary,
		CreatedAt:   parseTime(createdStr),
		UpdatedAt:   parseTime(updatedStr),
	}, nil
}

// LoadSessionMessages returns all messages for a session ordered by creation time.
func (s *Store) LoadSessionMessages(sessionID int64) ([]MessageRecord, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	rows, err := s.db.Query(`SELECT role, content, display, model, created_at
        FROM messages
        WHERE conversation_id = ?
        ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	records := []MessageRecord{}
	for rows.Next() {
		var (
			role      string
			content   string
			display   string
			model     sql.NullString
			createdAt string
		)
		if err := rows.Scan(&role, &content, &display, &model, &createdAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		rec := MessageRecord{
			Role:      role,
			Content:   content,
			Display:   display,
			CreatedAt: parseTime(createdAt),
		}
		if model.Valid {
			rec.Model = model.String
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// SaveMessage persists a message to the session and updates metadata.
func (s *Store) SaveMessage(sessionID int64, rec MessageRecord) error {
	if s == nil {
		return errors.New("store is nil")
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	} else {
		rec.CreatedAt = rec.CreatedAt.UTC()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO messages (conversation_id, role, content, display, model, created_at)
        VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID,
		rec.Role,
		rec.Content,
		rec.Display,
		nullableString(rec.Model),
		formatTime(rec.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}

	summary := ""
	if rec.Role == "user" {
		summary = summarizeText(rec.Display, rec.Content)
	}

	_, err = tx.Exec(`UPDATE conversations
        SET updated_at = ?,
            last_summary = CASE WHEN ? <> '' THEN ? ELSE last_summary END
        WHERE id = ?`,
		formatTime(rec.CreatedAt),
		summary,
		summary,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("update conversation metadata: %w", err)
	}

	_, err = tx.Exec(`UPDATE projects
        SET updated_at = ?
        WHERE id = (SELECT project_id FROM conversations WHERE id = ?)`,
		formatTime(rec.CreatedAt),
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("update project metadata: %w", err)
	}

	return tx.Commit()
}

// ListProjectsWithStats returns projects ordered by recent activity.
func (s *Store) ListProjectsWithStats(limit int) ([]ProjectStats, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT p.id, p.name, p.description, p.created_at, p.updated_at,
        COUNT(c.id) AS conversation_count,
        MAX(c.updated_at) AS last_conversation
        FROM projects p
        LEFT JOIN conversations c ON c.project_id = p.id
        GROUP BY p.id
        ORDER BY COALESCE(MAX(c.updated_at), p.updated_at) DESC
        LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	stats := []ProjectStats{}
	for rows.Next() {
		var (
			proj        Project
			createdStr  string
			updatedStr  string
			lastConvStr sql.NullString
			count       int64
		)
		if err := rows.Scan(
			&proj.ID,
			&proj.Name,
			&proj.Description,
			&createdStr,
			&updatedStr,
			&count,
			&lastConvStr,
		); err != nil {
			return nil, fmt.Errorf("scan project metadata: %w", err)
		}
		proj.CreatedAt = parseTime(createdStr)
		proj.UpdatedAt = parseTime(updatedStr)
		stats = append(stats, ProjectStats{
			Project:           proj,
			ConversationCount: int(count),
			LastConversation:  parseNullableTime(lastConvStr),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return stats, nil
}

// ConversationsByProject returns the conversations for a given project.
func (s *Store) ConversationsByProject(projectID int64, limit int) ([]Session, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	if projectID == 0 {
		return nil, errors.New("project id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT id, project_id, title, last_summary, created_at, updated_at
        FROM conversations
        WHERE project_id = ?
        ORDER BY updated_at DESC
        LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("query conversations: %w", err)
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var (
			sess       Session
			createdStr string
			updatedStr string
		)
		if err := rows.Scan(&sess.ID, &sess.ProjectID, &sess.Title, &sess.LastSummary, &createdStr, &updatedStr); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		sess.CreatedAt = parseTime(createdStr)
		sess.UpdatedAt = parseTime(updatedStr)
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// CreateProject inserts a new project row.
func (s *Store) CreateProject(name, description string) (Project, error) {
	if s == nil {
		return Project{}, errors.New("store is nil")
	}
	name = normalizeProjectName(name)
	if name == "" {
		return Project{}, errors.New("project name is required")
	}
	description = strings.TrimSpace(description)
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO projects (name, description, created_at, updated_at)
        VALUES (?, ?, ?, ?)`, name, description, formatTime(now), formatTime(now))
	if err != nil {
		return Project{}, fmt.Errorf("insert project: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Project{}, fmt.Errorf("fetch project id: %w", err)
	}
	return Project{
		ID:          id,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// EnsureProject finds or creates a project with the given name.
func (s *Store) EnsureProject(name string) (Project, error) {
	if s == nil {
		return Project{}, errors.New("store is nil")
	}
	name = normalizeProjectName(name)
	if name == "" {
		return Project{}, errors.New("project name is required")
	}
	proj, err := s.GetProjectByName(name)
	if err == nil {
		return proj, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Project{}, err
	}
	return s.CreateProject(name, "")
}

// RenameProject updates a project's name and returns the updated record.
func (s *Store) RenameProject(id int64, name string) (Project, error) {
	if s == nil {
		return Project{}, errors.New("store is nil")
	}
	if id == 0 {
		return Project{}, errors.New("project id is required")
	}
	name = normalizeProjectName(name)
	if name == "" {
		return Project{}, errors.New("project name is required")
	}
	now := formatTime(time.Now().UTC())
	if _, err := s.db.Exec(`UPDATE projects SET name = ?, updated_at = ? WHERE id = ?`, name, now, id); err != nil {
		return Project{}, fmt.Errorf("rename project: %w", err)
	}
	return s.GetProject(id)
}

// DeleteProject removes a project and all of its conversations.
func (s *Store) DeleteProject(id int64) error {
	if s == nil {
		return errors.New("store is nil")
	}
	if id == 0 {
		return errors.New("project id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM conversations WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("delete project conversations: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return tx.Commit()
}

// SaveSetting upserts a key/value pair for the provided scope + target.
func (s *Store) SaveSetting(scope SettingScope, targetID int64, key, value string) error {
	if s == nil {
		return errors.New("store is nil")
	}
	if !scope.valid() {
		return fmt.Errorf("invalid settings scope %q", scope)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("setting key is required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.Exec(`INSERT INTO settings (scope, target_id, key, value, updated_at)
	        VALUES (?, ?, ?, ?, ?)
	        ON CONFLICT(scope, target_id, key)
	        DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, scope, targetID, key, value, now)
	if err != nil {
		return fmt.Errorf("save setting: %w", err)
	}
	return nil
}

// LoadSettings returns all key/value pairs for the provided scope + target.
func (s *Store) LoadSettings(scope SettingScope, targetID int64) (map[string]string, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	if !scope.valid() {
		return nil, fmt.Errorf("invalid settings scope %q", scope)
	}
	rows, err := s.db.Query(`SELECT key, value FROM settings WHERE scope = ? AND target_id = ?`, scope, targetID)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		result[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// LoadSetting fetches a single key/value pair for the provided scope + target.
func (s *Store) LoadSetting(scope SettingScope, targetID int64, key string) (string, bool, error) {
	settings, err := s.LoadSettings(scope, targetID)
	if err != nil {
		return "", false, err
	}
	value, ok := settings[key]
	return value, ok, nil
}

// GetProject fetches a project by id.
func (s *Store) GetProject(id int64) (Project, error) {
	if s == nil {
		return Project{}, errors.New("store is nil")
	}
	row := s.db.QueryRow(`SELECT id, name, description, created_at, updated_at FROM projects WHERE id = ?`, id)
	var (
		proj       Project
		createdStr string
		updatedStr string
	)
	if err := row.Scan(&proj.ID, &proj.Name, &proj.Description, &createdStr, &updatedStr); err != nil {
		return Project{}, err
	}
	proj.CreatedAt = parseTime(createdStr)
	proj.UpdatedAt = parseTime(updatedStr)
	return proj, nil
}

// GetProjectByName fetches a project with an exact name match.
func (s *Store) GetProjectByName(name string) (Project, error) {
	if s == nil {
		return Project{}, errors.New("store is nil")
	}
	name = normalizeProjectName(name)
	if name == "" {
		return Project{}, errors.New("project name is required")
	}
	row := s.db.QueryRow(`SELECT id, name, description, created_at, updated_at FROM projects WHERE LOWER(name) = LOWER(?)`, name)
	var (
		proj       Project
		createdStr string
		updatedStr string
	)
	if err := row.Scan(&proj.ID, &proj.Name, &proj.Description, &createdStr, &updatedStr); err != nil {
		return Project{}, err
	}
	proj.CreatedAt = parseTime(createdStr)
	proj.UpdatedAt = parseTime(updatedStr)
	return proj, nil
}

// UpdateSessionTitle renames a conversation.
func (s *Store) UpdateSessionTitle(sessionID int64, title string) error {
	if s == nil {
		return errors.New("store is nil")
	}
	if sessionID == 0 {
		return errors.New("session id is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("title is required")
	}
	_, err := s.db.Exec(`UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?`, title, formatTime(time.Now().UTC()), sessionID)
	if err != nil {
		return fmt.Errorf("rename conversation: %w", err)
	}
	return nil
}

// DeleteSession removes a single conversation and its messages.
func (s *Store) DeleteSession(sessionID int64) error {
	if s == nil {
		return errors.New("store is nil")
	}
	if sessionID == 0 {
		return errors.New("session id is required")
	}
	_, err := s.db.Exec(`DELETE FROM conversations WHERE id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return nil
}

func summarizeText(display, content string) string {
	candidate := strings.TrimSpace(display)
	if candidate == "" {
		candidate = strings.TrimSpace(content)
	}
	candidate = strings.Join(strings.Fields(candidate), " ")
	if len(candidate) > 120 {
		candidate = candidate[:117] + "..."
	}
	return candidate
}

func nullableString(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func normalizeProjectName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.Join(strings.Fields(name), " ")
}

func nullInt64(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func parseNullableTime(value sql.NullString) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return parseTime(value.String)
}

func (s *Store) ensureConversationProjectColumn() error {
	exists, err := columnExists(s.db, "conversations", "project_id")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE conversations ADD COLUMN project_id INTEGER REFERENCES projects(id)`); err != nil {
		return fmt.Errorf("add project column: %w", err)
	}
	return nil
}

func (s *Store) ensureDefaultProject() (Project, error) {
	proj, err := s.GetProjectByName(DefaultProjectName)
	if err == nil {
		return proj, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Project{}, err
	}
	return s.CreateProject(DefaultProjectName, "")
}

func (s *Store) backfillConversationProjects(defaultProjectID int64) error {
	if defaultProjectID == 0 {
		return nil
	}
	if _, err := s.db.Exec(`UPDATE conversations SET project_id = ? WHERE project_id IS NULL OR project_id = 0`, defaultProjectID); err != nil {
		return fmt.Errorf("backfill conversations: %w", err)
	}
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	query := fmt.Sprintf("SELECT name FROM pragma_table_info(%q)", table)
	rows, err := db.Query(query)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func configureDB(db *sql.DB) error {
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	return nil
}

func (s *Store) migrate() error {
	// Create base tables first (without project_id-dependent indexes)
	baseStmts := []string{
		`CREATE TABLE IF NOT EXISTS projects (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL UNIQUE,
            description TEXT NOT NULL DEFAULT '',
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS conversations (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            title TEXT NOT NULL,
            last_summary TEXT NOT NULL DEFAULT '',
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            conversation_id INTEGER NOT NULL,
            role TEXT NOT NULL,
            content TEXT NOT NULL,
            display TEXT NOT NULL,
            model TEXT,
            created_at TEXT NOT NULL,
            FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS settings (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            scope TEXT NOT NULL,
            target_id INTEGER NOT NULL DEFAULT 0,
            key TEXT NOT NULL,
            value TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            UNIQUE(scope, target_id, key)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, id)`,
	}
	for _, stmt := range baseStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("run base migration: %w", err)
		}
	}

	// Ensure project_id column exists (for legacy databases)
	if err := s.ensureConversationProjectColumn(); err != nil {
		return fmt.Errorf("ensure project column: %w", err)
	}

	// Create default project and backfill
	proj, err := s.ensureDefaultProject()
	if err != nil {
		return fmt.Errorf("ensure default project: %w", err)
	}
	if err := s.backfillConversationProjects(proj.ID); err != nil {
		return fmt.Errorf("backfill projects: %w", err)
	}

	// Now create indexes that depend on project_id
	projectIndexStmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_conversations_project ON conversations(project_id, updated_at DESC)`,
	}
	for _, stmt := range projectIndexStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("create project indexes: %w", err)
		}
	}

	return nil
}

func candidateDBPaths() []string {
	paths := []string{}
	if custom := strings.TrimSpace(os.Getenv("OLLAMA_TUI_DB")); custom != "" {
		paths = append(paths, custom)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		paths = append(paths, filepath.Join(home, defaultDBDirName, defaultDBFileName))
	}
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		paths = append(paths, filepath.Join(wd, "."+defaultDBDirName, defaultDBFileName))
	}
	tempDir := filepath.Join(os.TempDir(), defaultDBDirName)
	paths = append(paths, filepath.Join(tempDir, defaultDBFileName))
	return uniquePaths(paths)
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// BoolSettingValue encodes a bool for storage.
func BoolSettingValue(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// ParseBoolSetting converts a stored string into a bool with a fallback.
func ParseBoolSetting(value string, defaultValue bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	default:
		return defaultValue
	}
}
