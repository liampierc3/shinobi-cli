package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"shinobi/internal/config"
	"shinobi/internal/search"
	"shinobi/internal/storage"
	"shinobi/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// ANSI escape sequences for mouse tracking control
const (
	// Disable mouse tracking modes that cause SGR codes
	// We disable X10 (1000), button-event (1002), and any-event (1003) tracking
	// but keep SGR mode (1006) disabled separately after to ensure clean state
	disableMouseSeq = "\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[?1006l"
	// Re-enable default mouse tracking (if needed on exit)
	enableMouseSeq = ""
)

// disableMouseTracking explicitly disables all mouse tracking modes in the terminal
// This prevents SGR mouse codes from appearing in input when the terminal itself
// or a previous program left mouse tracking enabled
func disableMouseTracking() {
	// Write directly to /dev/tty to ensure it reaches the terminal
	// even if stdout/stderr are redirected
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		// Fallback to stdout if we can't open /dev/tty
		fmt.Print(disableMouseSeq)
		return
	}
	defer tty.Close()
	tty.WriteString(disableMouseSeq)
}

// enableMouseTracking restores mouse tracking (currently a no-op)
func enableMouseTracking() {
	if enableMouseSeq != "" {
		tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if err != nil {
			fmt.Print(enableMouseSeq)
			return
		}
		defer tty.Close()
		tty.WriteString(enableMouseSeq)
	}
}

func main() {
	var store *storage.Store
	if db, err := storage.OpenDefault(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to open conversation store: %v\n", err)
	} else {
		store = db
		defer store.Close()
	}

	// Clear screen and hide cursor BEFORE any other output
	// This prevents previous terminal output from showing at the top
	fmt.Print("\033[2J\033[H\033[?25l")

	// Configure helpers
	userCfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to load config: %v\n", err)
	}

	// First-run setup: if no backend URL is configured, walk the user through it.
	if strings.TrimSpace(userCfg.EffectiveLMStudioURL()) == "" && strings.TrimSpace(userCfg.OllamaURL) == "" {
		result, err := ui.RunSetupWizardWithConfig("", "", "", nil, "", nil, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
			os.Exit(1)
		}
		if result.Cancelled {
			fmt.Print("\033[?25h")
			os.Exit(0)
		}
		applySetupResult(&userCfg, result)
		if err := config.Save(userCfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
		}
		fmt.Print("\033[2J\033[H\033[?25l")
	}
	braveToken := userCfg.BraveToken
	if braveToken == "" {
		braveToken = userCfg.BraveAPIKey
	}
	searchClient := search.NewFromConfig(braveToken, userCfg.TavilyAPIKey, userCfg.SerpAPIKey, userCfg.DDGEnabled)
	// Build the list of filesystem roots, deduplicating as we go.
	var fsRoots []string
	seen := make(map[string]bool)
	addRoot := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid FS root %q: %v\n", p, err)
			return
		}
		if !seen[abs] {
			seen[abs] = true
			fsRoots = append(fsRoots, abs)
		}
	}
	addRoot(userCfg.FilesystemRoot)
	for _, p := range userCfg.FilesystemRoots {
		addRoot(p)
	}
	if len(fsRoots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			addRoot(cwd)
		} else {
			addRoot(".")
		}
	}

	// Optional per-user model pin. When unset, the app auto-selects the first
	// model returned by the active backend's /v1/models at startup.
	modelID := strings.TrimSpace(userCfg.DefaultModel)
	contextPaths := append([]string(nil), userCfg.ContextPaths...)
	autoLoadSkills := append([]string(nil), userCfg.AutoLoadSkills...)
	agentDirs := append([]string(nil), userCfg.AgentDirs...)
	skillsDir := strings.TrimSpace(userCfg.SkillsDir)

	// Resolve which backend to use. If both are configured and no preference is
	// saved, prompt the user once and persist their choice.
	activeBackend, activeURL := resolveActiveBackend(&userCfg)

	// Create the UI model once the connection is confirmed
	m, err := ui.NewModel(modelID, store, searchClient, fsRoots, contextPaths, autoLoadSkills, activeURL, userCfg.BackendAPIKey, activeBackend, userCfg.EffectiveLMStudioURL(), userCfg.OllamaURL, agentDirs, skillsDir, braveToken, userCfg.TavilyAPIKey, userCfg.SerpAPIKey, userCfg.DDGEnabled)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing application: %v\n", err)
		os.Exit(1)
	}

	// Explicitly disable mouse tracking in the terminal to prevent SGR codes
	// from appearing in the input field when scrolling
	disableMouseTracking()
	defer enableMouseTracking()

	// Show cursor on exit
	defer fmt.Print("\033[?25h")

	// Create and run the Bubble Tea program
	p := tea.NewProgram(
		m,
		tea.WithAltScreen(), // Use alternate screen buffer
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}

// resolveActiveBackend returns the backend and URL to use for this session.
// If both backends are configured and no preference is saved, it prompts the
// user once, saves their choice, and returns it.
func resolveActiveBackend(cfg *config.Config) (config.Backend, string) {
	lmStudioURL := cfg.EffectiveLMStudioURL()
	ollamaURL := strings.TrimSpace(cfg.OllamaURL)

	hasLMStudio := lmStudioURL != ""
	hasOllama := ollamaURL != ""

	// Single backend configured — use it directly.
	if hasLMStudio && !hasOllama {
		return config.BackendLMStudio, lmStudioURL
	}
	if hasOllama && !hasLMStudio {
		return config.BackendOllama, ollamaURL
	}

	// Both configured — use saved preference if available.
	if cfg.ActiveBackend != "" {
		switch config.Backend(cfg.ActiveBackend) {
		case config.BackendOllama:
			return config.BackendOllama, ollamaURL
		case config.BackendLMStudio:
			return config.BackendLMStudio, lmStudioURL
		}
	}

	// Both configured, no saved preference — run the setup wizard starting
	// at the backend picker step so the user can choose.
	result, err := ui.RunSetupWizardWithConfig(lmStudioURL, ollamaURL, "", cfg.AgentDirs, cfg.SkillsDir, allFSRoots(cfg), cfg.BraveToken)
	fmt.Print("\033[2J\033[H\033[?25l")
	if err != nil || result.Cancelled {
		fmt.Print("\033[?25h")
		os.Exit(0)
	}
	applySetupResult(cfg, result)
	if err := config.Save(*cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
	}

	if cfg.ActiveBackend == string(config.BackendOllama) {
		return config.BackendOllama, ollamaURL
	}
	return config.BackendLMStudio, lmStudioURL
}

func allFSRoots(cfg *config.Config) []string {
	var roots []string
	if cfg.FilesystemRoot != "" {
		roots = append(roots, cfg.FilesystemRoot)
	}
	roots = append(roots, cfg.FilesystemRoots...)
	return roots
}

func applySetupResult(cfg *config.Config, r ui.SetupResult) {
	if r.LMStudioURL != "" {
		cfg.LMStudioURL = r.LMStudioURL
	}
	if r.OllamaURL != "" {
		cfg.OllamaURL = r.OllamaURL
	}
	if r.ActiveBackend != "" {
		cfg.ActiveBackend = string(r.ActiveBackend)
	}
	if len(r.AgentDirs) > 0 {
		cfg.AgentDirs = r.AgentDirs
	}
	if r.SkillsDir != "" {
		cfg.SkillsDir = r.SkillsDir
	}
	if len(r.FSRoots) > 0 {
		cfg.FilesystemRoot = r.FSRoots[0]
		cfg.FilesystemRoots = r.FSRoots[1:]
	}
	if r.BraveKey != "" {
		cfg.BraveToken = r.BraveKey
	}
}
