package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const fsPreviewLimit = 4000

type fsWriteRequest struct {
	Path    string
	AbsPath string
	Content string
	Ready   bool
}

func (m *Model) handleFilesystemCommand(args string) tea.Cmd {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		m.showSystemNotice("Usage: /fs <ls|read|write|apply|cancel> [path]")
		return nil
	}
	sub := strings.ToLower(parts[0])
	rest := strings.TrimSpace(strings.TrimPrefix(args, parts[0]))
	switch sub {
	case "ls":
		return m.fsList(rest)
	case "read":
		return m.fsRead(rest)
	case "write":
		return m.fsBeginWrite(rest)
	case "apply":
		return m.fsApplyWrite()
	case "cancel":
		return m.fsCancelWrite()
	default:
		m.showSystemNotice("Unknown fs command. Use ls, read, write, apply, or cancel.")
		return nil
	}
}

func (m *Model) fsList(path string) tea.Cmd {
	abs, err := m.resolveFSPath(path)
	if err != nil {
		m.showSystemNotice(err.Error())
		return nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		m.showSystemNotice(fmt.Sprintf("ls error: %v", err))
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		marker := ""
		if entry.IsDir() {
			marker = "/"
		}
		lines = append(lines, entry.Name()+marker)
	}
	if len(lines) == 0 {
		lines = append(lines, "(empty)")
	}
	rel := m.relativeToRoot(abs)
	m.showSystemNotice(fmt.Sprintf("ls %s\n%s", rel, strings.Join(lines, "\n")))
	return nil
}

func (m *Model) fsRead(path string) tea.Cmd {
	if strings.TrimSpace(path) == "" {
		m.showSystemNotice("Usage: /fs read <path>")
		return nil
	}
	abs, err := m.resolveFSPath(path)
	if err != nil {
		m.showSystemNotice(err.Error())
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		m.showSystemNotice(fmt.Sprintf("read error: %v", err))
		return nil
	}
	preview := string(data)
	if len(preview) > fsPreviewLimit {
		preview = preview[:fsPreviewLimit] + "\n... (truncated)"
	}
	m.appendMessage(NewSystemMessage(fmt.Sprintf("Contents of %s:\n```\n%s\n```", m.relativeToRoot(abs), preview)))
	m.updateViewportContent()
	m.viewportGotoBottom()
	return nil
}

func (m *Model) fsBeginWrite(path string) tea.Cmd {
	if strings.TrimSpace(path) == "" {
		m.showSystemNotice("Usage: /fs write <path>")
		return nil
	}
	abs, err := m.resolveFSPath(path)
	if err != nil {
		m.showSystemNotice(err.Error())
		return nil
	}
	m.pendingFSWrite = &fsWriteRequest{Path: path, AbsPath: abs}
	m.showSystemNotice(fmt.Sprintf("Paste file content for %s as your next message. Use /fs cancel to abort.", path))
	return nil
}

func (m *Model) consumePendingFilesystemContent(value string) bool {
	if m.pendingFSWrite == nil || m.pendingFSWrite.Ready {
		return false
	}
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "/fs") {
		return false
	}
	m.pendingFSWrite.Content = value
	m.pendingFSWrite.Ready = true
	bytes := len(value)
	m.showSystemNotice(fmt.Sprintf("Staged %d bytes for %s. Use /fs apply to confirm or /fs cancel.", bytes, m.pendingFSWrite.Path))
	return true
}

func (m *Model) fsApplyWrite() tea.Cmd {
	if m.pendingFSWrite == nil || !m.pendingFSWrite.Ready {
		m.showSystemNotice("No staged file write. Use /fs write first.")
		return nil
	}
	req := m.pendingFSWrite
	if err := m.writeFile(req.AbsPath, req.Content); err != nil {
		m.showSystemNotice(fmt.Sprintf("write failed: %v", err))
		return nil
	}
	m.showSystemNotice(fmt.Sprintf("Wrote %d bytes to %s", len(req.Content), m.relativeToRoot(req.AbsPath)))
	m.pendingFSWrite = nil
	return nil
}

func (m *Model) fsCancelWrite() tea.Cmd {
	if m.pendingFSWrite == nil {
		m.showSystemNotice("No staged file write.")
		return nil
	}
	m.pendingFSWrite = nil
	m.showSystemNotice("Canceled staged file write.")
	return nil
}

func (m *Model) writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func (m *Model) resolveFSPath(input string) (string, error) {
	if m == nil {
		return "", errors.New("model unavailable")
	}
	if strings.HasPrefix(input, "~/") || input == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			if input == "~" {
				input = home
			} else {
				input = filepath.Join(home, input[2:])
			}
		}
	}
	base := m.fsRoot
	if base == "" {
		base = "."
	}
	var abs string
	if filepath.IsAbs(input) {
		return filepath.Clean(input), nil
	} else {
		abs = filepath.Join(base, input)
		abs = filepath.Clean(abs)
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q escapes workspace root", input)
	}
	return abs, nil
}

func (m *Model) relativeToRoot(abs string) string {
	root := m.fsRoot
	if root == "" {
		return abs
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return abs
	}
	return rel
}
