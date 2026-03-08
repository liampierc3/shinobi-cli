package commands

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

// Loader loads command definitions from builtin and user directories.
type Loader struct {
	builtinDir string
	userDir    string
}

// DefaultLoader returns a loader searching the repository commands directory and the user's overrides.
func DefaultLoader() Loader {
	builtin := locateBuiltInCommands()
	home, err := os.UserHomeDir()
	userDir := ""
	if err == nil {
		userDir = filepath.Join(home, ".shinobi", "commands")
	}
	return Loader{builtinDir: builtin, userDir: userDir}
}

func locateBuiltInCommands() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "commands"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "commands"))
	}

	for _, candidate := range candidates {
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate
		}
	}
	return "commands"
}

// NewLoader creates a loader.
func NewLoader(builtinDir, userDir string) Loader {
	return Loader{builtinDir: builtinDir, userDir: userDir}
}

// Load discovers commands from both directories.
func (l Loader) Load() ([]Command, error) {
	sources := []string{l.builtinDir, l.userDir}
	seen := make(map[string]Command)

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
			cmd, err := parseCommandFile(path)
			if err != nil {
				return nil, err
			}

			seen[cmd.Name] = cmd
		}
	}

	result := make([]Command, 0, len(seen))
	for _, cmd := range seen {
		result = append(result, cmd)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func parseCommandFile(path string) (Command, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Command{}, err
	}

	meta, body := splitFrontMatter(string(content))
	if meta == "" {
		return Command{}, fmt.Errorf("command file %s missing frontmatter", path)
	}

	cmd, err := parseMetadata(meta)
	if err != nil {
		return Command{}, fmt.Errorf("parse command %s: %w", path, err)
	}
	cmd.Prompt = strings.TrimSpace(body)
	cmd.FilePath = path
	if err := cmd.Validate(); err != nil {
		return Command{}, err
	}
	return cmd, nil
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

func parseMetadata(front string) (Command, error) {
	cmd := Command{}
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
			cmd.Name = value
		case "description":
			cmd.Description = value
		case "color":
			cmd.Color = value
		case "model":
			cmd.Model = value
		case "priority":
			// Parse priority as integer
			var p int
			fmt.Sscanf(value, "%d", &p)
			cmd.Priority = p
		}
	}
	if err := scanner.Err(); err != nil {
		return Command{}, err
	}
	return cmd, nil
}
