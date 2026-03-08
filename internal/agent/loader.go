package agent

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

// Loader loads agents from builtin and user directories.
type Loader struct {
	builtinDir string
	userDirs   []string
}

var errMissingFrontmatter = errors.New("missing frontmatter")

func DefaultLoader() Loader {
	return LoaderWithExtraDirs(nil)
}

// LoaderWithExtraDirs creates a Loader using the provided dirs as the source
// of truth. If dirs is non-empty, only those are used. If empty, falls back
// to the hardwired defaults.
func LoaderWithExtraDirs(dirs []string) Loader {
	builtin := locateBuiltInAgents()
	var userDirs []string
	if len(dirs) > 0 {
		userDirs = append([]string(nil), dirs...)
	} else {
		userDirs = defaultUserDirs()
	}
	return Loader{builtinDir: builtin, userDirs: dedupeDirs(userDirs)}
}

func locateBuiltInAgents() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "agents"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "agents"))
	}

	for _, candidate := range candidates {
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate
		}
	}
	return "agents"
}

func (l Loader) Load() ([]Agent, error) {
	seenPath := make(map[string]struct{})
	result := make([]Agent, 0)

	addAgent := func(a Agent) {
		if _, exists := seenPath[a.FilePath]; exists {
			return
		}
		seenPath[a.FilePath] = struct{}{}
		result = append(result, a)
	}

	// Load user and builtin filesystem dirs first so they take priority over
	// embedded defaults.
	sources := make([]string, 0, 1+len(l.userDirs))
	sources = append(sources, l.builtinDir)
	sources = append(sources, l.userDirs...)

	for _, dir := range sources {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			agent, err := parseAgentFile(path)
			if err != nil {
				if errors.Is(err, errMissingFrontmatter) {
					continue
				}
				return nil, err
			}
			addAgent(agent)
		}
	}

	// Append embedded defaults even when names overlap with local/user agents.
	// This keeps stock agents visible in the dedicated "shinobi" group.
	if embedded, err := loadEmbeddedAgents(); err == nil {
		for _, a := range embedded {
			addAgent(a)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].FilePath < result[j].FilePath
	})
	return result, nil
}

func parseAgentFile(path string) (Agent, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Agent{}, err
	}

	meta, body := splitFrontMatter(string(content))
	if meta == "" {
		return Agent{}, errMissingFrontmatter
	}

	agent, err := parseMetadata(meta)
	if err != nil {
		return Agent{}, fmt.Errorf("parse agent %s: %w", path, err)
	}
	agent.Prompt = strings.TrimSpace(body)
	agent.FilePath = path
	applyAgentFallbacks(&agent, body)
	if err := agent.Validate(); err != nil {
		return Agent{}, err
	}
	return agent, nil
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

func parseMetadata(front string) (Agent, error) {
	agent := Agent{}
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
			agent.Name = value
		case "description":
			agent.Description = value
		case "model":
			agent.Model = value
		case "color":
			agent.Color = value
		case "tools":
			agent.Tools = parseList(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func parseList(raw string) []string {
	raw = strings.Trim(raw, "[]")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.Trim(strings.TrimSpace(part), "\"")
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func defaultUserDirs() []string {
	var dirs []string
	if env := strings.TrimSpace(os.Getenv("SHINOBI_AGENT_DIR")); env != "" {
		dirs = append(dirs, splitPathList(env)...)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dirs = append(dirs, filepath.Join(home, ".shinobi", "agents"))
		memoryAgents := filepath.Join(home, "memory", "ai", "local")
		if stat, err := os.Stat(memoryAgents); err == nil && stat.IsDir() {
			dirs = append(dirs, memoryAgents)
		}
		codingAgents := filepath.Join(home, "memory", "ai", "code")
		if stat, err := os.Stat(codingAgents); err == nil && stat.IsDir() {
			dirs = append(dirs, codingAgents)
		}
	}
	return dedupeDirs(dirs)
}

func splitPathList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ':' || r == ';'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = append(result, part)
	}
	return result
}

func dedupeDirs(input []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(input))
	for _, dir := range input {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func applyAgentFallbacks(agent *Agent, body string) {
	if agent == nil {
		return
	}
	name, desc := deriveAgentIdentity(body)
	if agent.Name == "" {
		agent.Name = name
	}
	if agent.Description == "" {
		agent.Description = desc
	}
	if agent.Description == "" {
		agent.Description = agent.Name
	}
}

func deriveAgentIdentity(body string) (string, string) {
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
		name, desc := splitTitle(title)
		return name, desc
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

// Validate ensures agent has required fields.
func (a Agent) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("agent missing name")
	}
	if a.Description == "" {
		return fmt.Errorf("agent %q missing description", a.Name)
	}
	if a.Prompt == "" {
		return fmt.Errorf("agent %q missing prompt", a.Name)
	}
	return nil
}
