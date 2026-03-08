package ui

import (
	"os"
	"path/filepath"
	"strings"
)

// buildFilesystemMap scans each root path 3 levels deep (directories only)
// and returns a formatted tree string for injection into the model context.
func buildFilesystemMap(roots []string) string {
	if len(roots) == 0 {
		return ""
	}

	var tree strings.Builder
	for _, root := range roots {
		resolved, ok := resolveScanRoot(root)
		if !ok {
			continue
		}

		entries, err := os.ReadDir(resolved)
		if err != nil {
			continue
		}

		if tree.Len() > 0 {
			tree.WriteString("\n")
		}
		tree.WriteString(withTrailingSlash(resolved))
		tree.WriteString("\n")

		for _, entry := range entries {
			if !entry.IsDir() || isHiddenDir(entry.Name()) {
				continue
			}
			levelOneName := entry.Name()
			tree.WriteString("  ")
			tree.WriteString(levelOneName)
			tree.WriteString("/\n")

			levelTwoEntries, err := os.ReadDir(filepath.Join(resolved, levelOneName))
			if err != nil {
				continue
			}

			for _, levelTwo := range levelTwoEntries {
				if !levelTwo.IsDir() || isHiddenDir(levelTwo.Name()) {
					continue
				}
				levelTwoName := levelTwo.Name()
				tree.WriteString("    ")
				tree.WriteString(levelTwoName)
				tree.WriteString("/\n")

				levelThreeEntries, err := os.ReadDir(filepath.Join(resolved, levelOneName, levelTwoName))
				if err != nil {
					continue
				}
				for _, levelThree := range levelThreeEntries {
					if !levelThree.IsDir() || isHiddenDir(levelThree.Name()) {
						continue
					}
					tree.WriteString("      ")
					tree.WriteString(levelThree.Name())
					tree.WriteString("/\n")
				}
			}
		}
	}

	if tree.Len() == 0 {
		return ""
	}

	return "<filesystem_map>\n" +
		"Your local filesystem structure (key directories only):\n\n" +
		strings.TrimSuffix(tree.String(), "\n") +
		"\n\nIMPORTANT: Use absolute paths from this map directly. Do not run find, ls, or bash search commands.\n" +
		"</filesystem_map>"
}

func resolveScanRoot(root string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false
	}

	if strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		root = filepath.Join(home, root[2:])
	}

	if !filepath.IsAbs(root) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return "", false
		}
		root = absRoot
	}

	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return root, true
}

func isHiddenDir(name string) bool {
	return strings.HasPrefix(name, ".")
}

func withTrailingSlash(path string) string {
	if strings.HasSuffix(path, string(os.PathSeparator)) {
		return path
	}
	return path + string(os.PathSeparator)
}
