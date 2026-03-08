package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"shinobi/internal/storage"
)

func (m *Model) refreshSessionMenu() {
	summaries := []sessionSummary{}
	if m.store != nil {
		if sessions, err := m.store.RecentSessions(500); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: unable to load conversations: %v\n", err)
		} else {
			summaries = make([]sessionSummary, 0, len(sessions))
			for _, sess := range sessions {
				summaries = append(summaries, summarizeSession(sess))
			}
		}
	}

	m.sessions = summaries
	m.sessionMap = make(map[string]sessionSummary, len(summaries))
	for _, sess := range summaries {
		m.sessionMap[sess.Name] = sess
	}
	m.sessionMenuItems = m.buildSessionMenuCommands()
}

func summarizeSession(sess storage.Session) sessionSummary {
	focus := sanitizeFocus(sess.LastSummary)
	if focus == "" {
		focus = "No focus yet"
	}
	updated := sess.UpdatedAt
	if updated.IsZero() {
		updated = sess.CreatedAt
	}
	name := fmt.Sprintf("#%d · %s", sess.ID, sess.Title)
	description := fmt.Sprintf("%s · updated %s", focus, updated.Local().Format("Jan 02 15:04"))
	return sessionSummary{
		ID:          sess.ID,
		ProjectID:   sess.ProjectID,
		Name:        name,
		Title:       sess.Title,
		Description: description,
		Focus:       focus,
		UpdatedAt:   updated,
	}
}

func (m *Model) sessionTableRows() string {
	if len(m.sessions) == 0 {
		return "No saved sessions | — | —"
	}
	var rows []string
	for _, sess := range m.sessions {
		sessionLabel := sanitizeCell(sess.Title)
		if sessionLabel == "" {
			sessionLabel = sanitizeCell(sess.Name)
		}
		if sessionLabel == "" {
			sessionLabel = fmt.Sprintf("Session #%d", sess.ID)
		}
		updated := "—"
		if !sess.UpdatedAt.IsZero() {
			updated = sess.UpdatedAt.Local().Format("15:04")
		}
		focus := sanitizeCell(sess.Focus)
		if focus == "" {
			focus = "No focus yet"
		}
		rows = append(rows, fmt.Sprintf("%s | %s | %s", sessionLabel, updated, focus))
	}
	return strings.Join(rows, "\n")
}

func sanitizeFocus(value string) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(cleaned) > 80 {
		cleaned = cleaned[:77] + "..."
	}
	return cleaned
}

func sanitizeCell(value string) string {
	cleaned := strings.ReplaceAll(strings.TrimSpace(value), "|", "-")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len(cleaned) > 80 {
		cleaned = cleaned[:77] + "..."
	}
	return cleaned
}

func (m *Model) appendMessage(msg Message) {
	m.messages = append(m.messages, msg)
	m.persistMessage(msg)
}

func (m *Model) ensureAutoSessionTitle(seed string) {
	if m == nil || m.sessionTitleUserSet {
		return
	}
	title := deriveChatTitle(seed)
	if title == "" {
		return
	}
	_ = m.renameSession(m.currentSessionID, title, false)
}

func (m *Model) persistMessage(msg Message) {
	if m.store == nil || m.currentSessionID == 0 {
		return
	}
	record := storage.MessageRecord{
		Role:    msg.Role,
		Content: msg.Content,
		Display: msg.Display,
		Model:   msg.Model,
	}
	if msg.Timestamp.IsZero() {
		record.CreatedAt = time.Now()
	} else {
		record.CreatedAt = msg.Timestamp
	}
	if strings.TrimSpace(record.Display) == "" {
		record.Display = msg.Content
	}
	if err := m.store.SaveMessage(m.currentSessionID, record); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save conversation: %v\n", err)
		return
	}
	m.refreshSessionMenu()
}

func (m *Model) startNewChatInProject(projectID int64) error {
	if projectID == 0 {
		projectID = m.currentProjectID
	}
	if m.store == nil {
		m.currentProjectID = projectID
		m.currentSessionID = 0
		m.currentSessionLabel = "new chat"
		m.sessionTitleUserSet = false
		m.messages = nil
		m.viewportLines = []string{""}
		m.appendMessage(NewSystemMessage(m.welcomeText()))
		m.injectToolsContext()
		// Auto-load system context for new conversations
		m.loadSystemContextAuto()
		m.updateViewportContent()
		m.viewportGotoBottom()
		return nil
	}
	if projectID == 0 {
		proj, err := m.store.EnsureProject(storage.DefaultProjectName)
		if err != nil {
			return err
		}
		projectID = proj.ID
	}
	title := fmt.Sprintf("Session %s", time.Now().Format("2006-01-02 15:04"))
	session, err := m.store.CreateSession(projectID, title)
	if err != nil {
		return err
	}
	summary := summarizeSession(session)
	m.applySessionRecords(summary, nil, false)
	m.appendMessage(NewSystemMessage(m.welcomeText()))
	m.injectToolsContext()
	// Auto-load system context for new conversations
	m.loadSystemContextAuto()
	m.updateViewportContent()
	m.viewportGotoBottom()
	return nil
}

func (m *Model) loadSessionSummary(summary sessionSummary) {
	if m.store == nil {
		m.showSystemNotice("Conversation persistence unavailable")
		return
	}
	records, err := m.store.LoadSessionMessages(summary.ID)
	if err != nil {
		m.showSystemNotice(fmt.Sprintf("Failed to load %s: %v", summary.Title, err))
		return
	}
	m.applySessionRecords(summary, records, true)
	m.showSystemNotice(fmt.Sprintf("Resumed %s", summary.Title))
}

func (m *Model) applySessionRecords(summary sessionSummary, records []storage.MessageRecord, titleUserDefined bool) {
	messages := make([]Message, 0, len(records))
	for _, rec := range records {
		msg := Message{
			Role:      rec.Role,
			Content:   rec.Content,
			Display:   rec.Display,
			Timestamp: rec.CreatedAt,
			Model:     rec.Model,
		}
		messages = append(messages, msg)
	}
	m.messages = messages
	m.currentSessionID = summary.ID
	if summary.ProjectID != 0 {
		m.currentProjectID = summary.ProjectID
	}
	title := strings.TrimSpace(summary.Title)
	if title == "" {
		title = strings.TrimSpace(summary.Name)
	}
	if title == "" {
		title = fmt.Sprintf("Session #%d", summary.ID)
	}
	m.currentSessionLabel = title
	m.sessionTitleUserSet = titleUserDefined
	m.viewportLines = []string{""}
	m.scrolledUp = false
	m.reflowAssistantMessages()
	m.updateViewportContent()
	m.viewportGotoBottom()
}

func (m *Model) ensureActiveSession() error {
	if m == nil {
		return nil
	}
	if m.currentSessionID != 0 || m.store == nil {
		return nil
	}
	projectID := m.currentProjectID
	if projectID == 0 {
		proj, err := m.store.EnsureProject(storage.DefaultProjectName)
		if err != nil {
			return err
		}
		projectID = proj.ID
		m.currentProjectID = projectID
	}
	label := fmt.Sprintf("Session %s", time.Now().Format("2006-01-02 15:04"))
	session, err := m.store.CreateSession(projectID, label)
	if err != nil {
		return err
	}
	m.currentSessionID = session.ID
	m.currentSessionLabel = session.Title
	m.sessionTitleUserSet = false
	if len(m.messages) == 0 {
		m.injectToolsContext()
	}
	return nil
}

func deriveChatTitle(input string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	const maxRunes = 60
	if len(runes) > maxRunes {
		trimmed = string(runes[:maxRunes]) + "…"
	}
	return trimmed
}
