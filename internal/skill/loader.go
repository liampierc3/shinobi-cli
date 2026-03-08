package skill

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Loader loads skills from a root directory containing skill subfolders.
type Loader struct {
	rootDir string
}

func DefaultLoader() Loader {
	return Loader{rootDir: defaultSkillsDir()}
}

// LoaderWithDir returns a Loader using the given directory.
// If dir is empty it falls back to the default.
func LoaderWithDir(dir string) Loader {
	if strings.TrimSpace(dir) == "" {
		return DefaultLoader()
	}
	return Loader{rootDir: dir}
}

// Load discovers skills under the configured root directory.
// Supported layouts:
//   - skills/<name>/SKILL.md (existing behavior)
//   - skills/<name>.md (flat file compatibility)
func (l Loader) Load() ([]Skill, error) {
	if l.rootDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(l.rootDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	seen := make(map[string]Skill)
	loadedDirStems := make(map[string]struct{})

	// Pass 1: existing subdirectory layout (<root>/<name>/SKILL.md).
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if shouldSkipSkillDir(name) {
			continue
		}
		skillPath := filepath.Join(l.rootDir, name, "SKILL.md")
		skill, err := parseSkillFile(skillPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		loadedDirStems[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
		seen[skillMapKey(skill.Name)] = skill
	}

	// Pass 2: flat markdown files (<root>/<name>.md).
	// Directory skills keep precedence for same stem or same resolved name.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := strings.TrimSpace(entry.Name())
		if filename == "" || !strings.EqualFold(filepath.Ext(filename), ".md") {
			continue
		}
		stem := strings.TrimSpace(strings.TrimSuffix(filename, filepath.Ext(filename)))
		if shouldSkipSkillDir(stem) {
			continue
		}
		if _, blocked := loadedDirStems[strings.ToLower(stem)]; blocked {
			continue
		}
		skillPath := filepath.Join(l.rootDir, filename)
		skill, err := parseFlatSkillFile(skillPath, stem)
		if err != nil {
			return nil, err
		}
		key := skillMapKey(skill.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = skill
	}

	result := make([]Skill, 0, len(seen))
	for _, skill := range seen {
		result = append(result, skill)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func defaultSkillsDir() string {
	if env := strings.TrimSpace(os.Getenv("SHINOBI_SKILLS_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(home, "memory", "ai", "skills")
	if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
		return candidate
	}
	return ""
}

func shouldSkipSkillDir(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, ".") || strings.HasPrefix(lower, "_") {
		return true
	}
	if lower == "archive" {
		return true
	}
	return false
}

func parseSkillFile(path string) (Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	meta, body := splitFrontMatter(string(content))
	if meta == "" {
		return Skill{}, fmt.Errorf("skill file %s missing frontmatter", path)
	}

	skill, err := parseMetadata(meta)
	if err != nil {
		return Skill{}, fmt.Errorf("parse skill %s: %w", path, err)
	}
	skill.Prompt = strings.TrimSpace(body)
	skill.FilePath = path
	applySkillFallbacks(&skill, body)
	if err := skill.Validate(); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func parseFlatSkillFile(path string, fallbackName string) (Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	meta, body := splitFrontMatter(string(content))
	skill := Skill{}
	if meta != "" {
		parsed, err := parseMetadata(meta)
		if err != nil {
			return Skill{}, fmt.Errorf("parse skill %s: %w", path, err)
		}
		skill = parsed
	}

	body = strings.TrimSpace(body)
	skill.Prompt = body
	skill.FilePath = path
	applySkillFallbacks(&skill, body)
	if skill.Name == "" {
		skill.Name = strings.TrimSpace(fallbackName)
	}
	if skill.Description == "" {
		skill.Description = skill.Name
	}
	if err := skill.Validate(); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func skillMapKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func splitFrontMatter(input string) (front, body string) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "---") {
		return "", input
	}

	parts := strings.SplitN(input, "---", 3)
	if len(parts) < 3 {
		return "", input
	}

	front = strings.TrimSpace(parts[1])
	body = strings.TrimSpace(parts[2])
	return front, body
}

func parseMetadata(front string) (Skill, error) {
	skill := Skill{}
	scanner := bufio.NewScanner(strings.NewReader(front))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		switch key {
		case "name":
			skill.Name = value
		case "description":
			skill.Description = value
		case "active":
			if parseBool(value) {
				skill.Auto = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func applySkillFallbacks(skill *Skill, body string) {
	if skill == nil {
		return
	}
	name, desc := deriveSkillIdentity(body)
	if skill.Name == "" {
		skill.Name = name
	}
	if skill.Description == "" {
		skill.Description = desc
	}
	if skill.Description == "" {
		skill.Description = skill.Name
	}
	if !skill.Auto && isAutoSkill(skill.Description) {
		skill.Auto = true
	}
}

func deriveSkillIdentity(body string) (string, string) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
		if title == "" {
			break
		}
		return splitTitle(title)
	}
	return "", ""
}

func splitTitle(title string) (string, string) {
	separators := []string{" - ", " — ", " – ", ": "}
	for _, sep := range separators {
		if parts := strings.SplitN(title, sep, 2); len(parts) == 2 {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
	}
	return strings.TrimSpace(title), ""
}

func isAutoSkill(desc string) bool {
	upper := strings.ToUpper(desc)
	if strings.Contains(upper, "ACTIVE]") {
		return true
	}
	if strings.Contains(upper, "ALWAYS APPLY") {
		return true
	}
	return false
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// Validate ensures skill has required fields.
func (s Skill) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("skill missing name")
	}
	if s.Prompt == "" {
		return fmt.Errorf("skill %q missing prompt", s.Name)
	}
	if s.Description == "" {
		return fmt.Errorf("skill %q missing description", s.Name)
	}
	return nil
}
