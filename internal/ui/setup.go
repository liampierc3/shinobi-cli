package ui

import (
	"strings"

	"github.com/liampierc3/shinobi-cli/internal/config"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type setupStep int

const (
	setupStepLMStudioURL setupStep = iota
	setupStepOllamaURL
	setupStepBackendPicker // only shown when both URLs are set
	setupStepAgentDirs
	setupStepSkillsDir
	setupStepFSRoots
	setupStepSearchProvider // picker: Brave / Tavily / SerpAPI / DDG / None
	setupStepSearchKey      // key input (skipped for DDG/None)
)

// SetupResult holds the values collected by the setup wizard.
type SetupResult struct {
	LMStudioURL   string
	OllamaURL     string
	ActiveBackend config.Backend // empty = not changed
	AgentDirs     []string       // empty = use defaults
	SkillsDir     string         // empty = use default
	FSRoots       []string       // empty = use CWD
	BraveKey      string
	TavilyKey     string
	SerpAPIKey    string
	DDGEnabled    bool
	Cancelled     bool
}

// setupCompleteMsg is sent when the wizard finishes (instead of tea.Quit),
// so the model can be embedded inside the main app.
type setupCompleteMsg struct{ Result SetupResult }

// SetupModel is a Bubble Tea model for first-run and in-app configuration.
// When embedded in the main app it sends setupCompleteMsg on completion.
// When run standalone, wrap it in setupStandaloneModel.
type SetupModel struct {
	step               setupStep
	input              textinput.Model
	pickerCursor       int // cursor for backend picker
	searchPickerCursor int // cursor for search provider picker
	agentDirs          []string
	fsRoots            []string
	width              int
	height             int
	err                string
	Result             SetupResult
}

// searchProviders is the ordered list shown in the search provider picker.
var searchProviders = []string{"brave", "tavily", "serpapi", "duckduckgo", "none"}

func NewSetupModel() *SetupModel {
	return newSetupModel("", "", config.Backend(""), nil, "", nil, "", "", "", false)
}

// NewSetupModelFromConfig pre-fills the wizard with existing config values.
func NewSetupModelFromConfig(lmStudioURL string, ollamaURL string, agentDirs []string, skillsDir string, fsRoots []string, braveKey string) *SetupModel {
	return newSetupModel(lmStudioURL, ollamaURL, "", agentDirs, skillsDir, fsRoots, braveKey, "", "", false)
}

// NewSetupModelFromConfigWithBackend pre-fills the wizard including the active backend.
func NewSetupModelFromConfigWithBackend(lmStudioURL string, ollamaURL string, activeBackend config.Backend, agentDirs []string, skillsDir string, fsRoots []string, braveKey string, tavilyKey string, serpapiKey string, ddgEnabled bool) *SetupModel {
	return newSetupModel(lmStudioURL, ollamaURL, activeBackend, agentDirs, skillsDir, fsRoots, braveKey, tavilyKey, serpapiKey, ddgEnabled)
}

func newSetupModel(lmStudioURL string, ollamaURL string, activeBackend config.Backend, agentDirs []string, skillsDir string, fsRoots []string, braveKey string, tavilyKey string, serpapiKey string, ddgEnabled bool) *SetupModel {
	ti := textinput.New()
	ti.Placeholder = "http://127.0.0.1:1234/v1"
	ti.SetValue(lmStudioURL)
	ti.Focus()
	ti.Width = 52
	ti.CharLimit = 256

	dirs := append([]string(nil), agentDirs...)
	roots := append([]string(nil), fsRoots...)

	// Set picker cursor to match the current active backend.
	cursor := 0
	if activeBackend == config.BackendOllama {
		cursor = 1
	}

	// Pre-select the search provider picker based on existing config.
	searchCursor := 4 // default: none
	switch {
	case braveKey != "":
		searchCursor = 0
	case tavilyKey != "":
		searchCursor = 1
	case serpapiKey != "":
		searchCursor = 2
	case ddgEnabled:
		searchCursor = 3
	}

	return &SetupModel{
		input:              ti,
		pickerCursor:       cursor,
		searchPickerCursor: searchCursor,
		agentDirs:          dirs,
		fsRoots:            roots,
		Result: SetupResult{
			LMStudioURL:   lmStudioURL,
			OllamaURL:     ollamaURL,
			ActiveBackend: activeBackend,
			AgentDirs:     dirs,
			SkillsDir:     skillsDir,
			FSRoots:       roots,
			BraveKey:      braveKey,
			TavilyKey:     tavilyKey,
			SerpAPIKey:    serpapiKey,
			DDGEnabled:    ddgEnabled,
		},
	}
}

func (m *SetupModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *SetupModel) complete() tea.Cmd {
	return func() tea.Msg { return setupCompleteMsg{Result: m.Result} }
}

func (m *SetupModel) cancel() tea.Cmd {
	m.Result.Cancelled = true
	return func() tea.Msg { return setupCompleteMsg{Result: m.Result} }
}

func (m *SetupModel) advanceTo(step setupStep, placeholder, current string) tea.Cmd {
	m.step = step
	m.err = ""
	m.input.SetValue(current)
	m.input.CursorEnd()
	m.input.Placeholder = placeholder
	if step == setupStepSearchProvider {
		m.input.Blur()
		return nil
	}
	m.input.Focus()
	return textinput.Blink
}

// existingKeyForProvider returns the currently configured key for a provider.
func (m *SetupModel) existingKeyForProvider(provider string) string {
	switch provider {
	case "brave":
		return m.Result.BraveKey
	case "tavily":
		return m.Result.TavilyKey
	case "serpapi":
		return m.Result.SerpAPIKey
	}
	return ""
}

// afterOllamaURL advances past the Ollama URL step. If both backends are
// configured it shows the picker, otherwise skips straight to agent dirs.
func (m *SetupModel) afterOllamaURL() tea.Cmd {
	if m.Result.LMStudioURL != "" && m.Result.OllamaURL != "" {
		m.step = setupStepBackendPicker
		m.err = ""
		m.input.Blur()
		return nil
	}
	return m.advanceTo(setupStepAgentDirs, "~/memory/ai/local", "")
}

func (m *SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, m.cancel()

		case tea.KeyEsc:
			if m.step == setupStepLMStudioURL {
				return m, m.cancel()
			}

		case tea.KeyUp:
			if m.step == setupStepBackendPicker {
				m.pickerCursor = (m.pickerCursor - 1 + 2) % 2
			}
			if m.step == setupStepSearchProvider {
				m.searchPickerCursor = (m.searchPickerCursor - 1 + len(searchProviders)) % len(searchProviders)
			}

		case tea.KeyDown:
			if m.step == setupStepBackendPicker {
				m.pickerCursor = (m.pickerCursor + 1) % 2
			}
			if m.step == setupStepSearchProvider {
				m.searchPickerCursor = (m.searchPickerCursor + 1) % len(searchProviders)
			}

		case tea.KeyTab:
			switch m.step {
			case setupStepLMStudioURL:
				m.Result.LMStudioURL = strings.TrimSpace(m.input.Value())
				return m, m.advanceTo(setupStepOllamaURL, "http://localhost:11434/v1", m.Result.OllamaURL)
			case setupStepOllamaURL:
				m.Result.OllamaURL = strings.TrimSpace(m.input.Value())
				if m.Result.LMStudioURL == "" && m.Result.OllamaURL == "" {
					m.err = "at least one backend url is required"
					return m, nil
				}
				return m, m.afterOllamaURL()
			case setupStepBackendPicker:
				return m, m.advanceTo(setupStepAgentDirs, "~/memory/ai/local", "")
			case setupStepAgentDirs:
				m.Result.AgentDirs = m.agentDirs
				return m, m.advanceTo(setupStepSkillsDir, "~/memory/ai/skills", m.Result.SkillsDir)
			case setupStepSkillsDir:
				m.Result.SkillsDir = ""
				return m, m.advanceTo(setupStepFSRoots, "~/notes", "")
			case setupStepFSRoots:
				m.Result.FSRoots = m.fsRoots
				return m, m.advanceTo(setupStepSearchProvider, "", "")
			case setupStepSearchProvider:
				return m, m.complete()
			case setupStepSearchKey:
				return m, m.complete()
			}

		case tea.KeyEnter:
			switch m.step {
			case setupStepLMStudioURL:
				m.Result.LMStudioURL = strings.TrimSpace(m.input.Value())
				return m, m.advanceTo(setupStepOllamaURL, "http://localhost:11434/v1", m.Result.OllamaURL)

			case setupStepOllamaURL:
				m.Result.OllamaURL = strings.TrimSpace(m.input.Value())
				if m.Result.LMStudioURL == "" && m.Result.OllamaURL == "" {
					m.err = "at least one backend url is required"
					return m, nil
				}
				return m, m.afterOllamaURL()

			case setupStepBackendPicker:
				if m.pickerCursor == 0 {
					m.Result.ActiveBackend = config.BackendLMStudio
				} else {
					m.Result.ActiveBackend = config.BackendOllama
				}
				return m, m.advanceTo(setupStepAgentDirs, "~/memory/ai/local", "")

			case setupStepAgentDirs:
				path := strings.TrimSpace(m.input.Value())
				if path == "" {
					m.Result.AgentDirs = m.agentDirs
					return m, m.advanceTo(setupStepSkillsDir, "~/memory/ai/skills", m.Result.SkillsDir)
				}
				m.agentDirs = append(m.agentDirs, path)
				m.input.SetValue("")
				return m, nil

			case setupStepSkillsDir:
				m.Result.SkillsDir = strings.TrimSpace(m.input.Value())
				return m, m.advanceTo(setupStepFSRoots, "~/notes", "")

			case setupStepFSRoots:
				path := strings.TrimSpace(m.input.Value())
				if path == "" {
					m.Result.FSRoots = m.fsRoots
					return m, m.advanceTo(setupStepSearchProvider, "", "")
				}
				m.fsRoots = append(m.fsRoots, path)
				m.input.SetValue("")
				return m, nil

			case setupStepSearchProvider:
				provider := searchProviders[m.searchPickerCursor]
				existingKey := m.existingKeyForProvider(provider)
				m.Result.DDGEnabled = false
				switch provider {
				case "duckduckgo":
					m.Result.BraveKey = ""
					m.Result.TavilyKey = ""
					m.Result.SerpAPIKey = ""
					m.Result.DDGEnabled = true
					return m, m.complete()
				case "none":
					m.Result.BraveKey = ""
					m.Result.TavilyKey = ""
					m.Result.SerpAPIKey = ""
					return m, m.complete()
				default:
					switch provider {
					case "brave":
						m.Result.BraveKey = existingKey
						m.Result.TavilyKey = ""
						m.Result.SerpAPIKey = ""
					case "tavily":
						m.Result.BraveKey = ""
						m.Result.TavilyKey = existingKey
						m.Result.SerpAPIKey = ""
					case "serpapi":
						m.Result.BraveKey = ""
						m.Result.TavilyKey = ""
						m.Result.SerpAPIKey = existingKey
					}
					return m, m.advanceTo(setupStepSearchKey, "", existingKey)
				}

			case setupStepSearchKey:
				key := strings.TrimSpace(m.input.Value())
				provider := searchProviders[m.searchPickerCursor]
				if key == "" {
					key = strings.TrimSpace(m.existingKeyForProvider(provider))
				}
				if key == "" {
					m.err = "api key required (or choose duckduckgo/none)"
					return m, nil
				}
				switch provider {
				case "brave":
					m.Result.BraveKey = key
				case "tavily":
					m.Result.TavilyKey = key
				case "serpapi":
					m.Result.SerpAPIKey = key
				}
				return m, m.complete()
			}

		case tea.KeyBackspace:
			if m.step == setupStepAgentDirs && m.input.Value() == "" && len(m.agentDirs) > 0 {
				m.agentDirs = m.agentDirs[:len(m.agentDirs)-1]
				return m, nil
			}
			if m.step == setupStepFSRoots && m.input.Value() == "" && len(m.fsRoots) > 0 {
				m.fsRoots = m.fsRoots[:len(m.fsRoots)-1]
				return m, nil
			}
		}
	}

	if m.step != setupStepBackendPicker && m.step != setupStepSearchProvider {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *SetupModel) View() string {
	if m.width == 0 {
		return ""
	}

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorPrimary)).
		Bold(true)

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorForeground))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorMuted))

	added := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorAssistantRole))

	cursor := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorPrimary))

	errStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorError))

	// Total steps depends on whether backend picker is shown.
	total := "7"
	if m.step >= setupStepBackendPicker {
		total = "8"
	}

	var lines []string

	switch m.step {
	case setupStepLMStudioURL:
		lines = []string{
			title.Render("shinobi") + hint.Render("  1/"+total),
			"",
			hint.Render("LM Studio backend url  (optional)"),
			"",
			"",
			label.Render("lm studio url"),
			m.input.View(),
			"",
			hint.Render("default  http://127.0.0.1:1234/v1"),
		}
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("Enter continue  •  Tab skip  •  Esc cancel"))

	case setupStepOllamaURL:
		lines = []string{
			title.Render("shinobi") + hint.Render("  2/"+total),
			"",
			hint.Render("Ollama backend url  (optional)"),
			"",
			"",
			label.Render("ollama url"),
			m.input.View(),
			"",
			hint.Render("default  http://localhost:11434/v1"),
		}
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("Enter continue  •  Tab skip  •  Ctrl+C cancel"))

	case setupStepBackendPicker:
		backends := []string{"LM Studio", "Ollama"}
		lines = []string{
			title.Render("shinobi") + hint.Render("  3/7"),
			"",
			hint.Render("which backend do you want to use?"),
			"",
			"",
		}
		for i, name := range backends {
			if i == m.pickerCursor {
				lines = append(lines, cursor.Render("▸ ")+label.Render(name))
			} else {
				lines = append(lines, hint.Render("  "+name))
			}
		}
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("↑↓ move  •  Enter select  •  Tab skip  •  Ctrl+C cancel"))

	case setupStepAgentDirs:
		lines = []string{
			title.Render("shinobi") + hint.Render("  3/"+total),
			"",
			hint.Render("where are your agent files?"),
			"",
			"",
			label.Render("agent directories"),
		}
		for _, d := range m.agentDirs {
			lines = append(lines, added.Render("  + "+d))
		}
		lines = append(lines,
			m.input.View(),
			"",
			hint.Render("Enter to add each path, Enter on empty to continue"),
			hint.Render("Tab to skip and use defaults  •  Backspace to remove last"),
		)
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("Enter add/continue  •  Ctrl+C cancel"))

	case setupStepSkillsDir:
		lines = []string{
			title.Render("shinobi") + hint.Render("  4/"+total),
			"",
			hint.Render("where are your skills?"),
			"",
			"",
			label.Render("skills directory"),
			m.input.View(),
			"",
			hint.Render("default  ~/memory/ai/skills"),
		}
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("Enter save  •  Tab skip  •  Ctrl+C cancel"))

	case setupStepFSRoots:
		lines = []string{
			title.Render("shinobi") + hint.Render("  5/"+total),
			"",
			hint.Render("what directories should shinobi have access to?"),
			"",
			"",
			label.Render("filesystem roots"),
		}
		for _, d := range m.fsRoots {
			lines = append(lines, added.Render("  + "+d))
		}
		lines = append(lines,
			m.input.View(),
			"",
			hint.Render("Enter to add each path, Enter on empty to continue"),
			hint.Render("e.g. ~/notes, ~/dev, ~/Documents/vault"),
			hint.Render("Tab to skip  •  Backspace to remove last"),
		)
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("Enter add/continue  •  Ctrl+C cancel"))

	case setupStepSearchProvider:
		lines = []string{
			title.Render("shinobi") + hint.Render("  6/"+total),
			"",
			hint.Render("web search provider  (optional)"),
			"",
			"",
			label.Render("search provider"),
		}
		providerHints := map[string]string{
			"brave":      "brave.com/search/api",
			"tavily":     "tavily.com  —  built for AI agents",
			"serpapi":    "serpapi.com  —  Google results",
			"duckduckgo": "no api key required",
			"none":       "",
		}
		for i, p := range searchProviders {
			row := p
			if h := providerHints[p]; h != "" {
				row += "  " + hint.Render(h)
			}
			if i == m.searchPickerCursor {
				lines = append(lines, cursor.Render("▸ ")+label.Render(row))
			} else {
				lines = append(lines, hint.Render("  "+row))
			}
		}
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("↑↓ move  •  Enter select  •  Tab skip  •  Ctrl+C cancel"))

	case setupStepSearchKey:
		provider := searchProviders[m.searchPickerCursor]
		providerURLs := map[string]string{
			"brave":   "brave.com/search/api",
			"tavily":  "tavily.com",
			"serpapi": "serpapi.com",
		}
		lines = []string{
			title.Render("shinobi") + hint.Render("  7/"+total),
			"",
			hint.Render(provider + " api key"),
			"",
			"",
			label.Render("api key"),
			m.input.View(),
			"",
			hint.Render(providerURLs[provider]),
		}
		if m.err != "" {
			lines = append(lines, "", errStyle.Render(m.err))
		}
		lines = append(lines, "", "", hint.Render("Enter save  •  Tab skip  •  Ctrl+C cancel"))
	}

	content := lipgloss.NewStyle().PaddingLeft(4).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)

	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Center, content)
}

// RunSetupWizard runs the setup wizard as a standalone tea.Program and returns the result.
func RunSetupWizard() (SetupResult, error) {
	return RunSetupWizardWithConfig("", "", config.Backend(""), nil, "", nil, "")
}

// RunSetupWizardWithConfig runs the setup wizard pre-filled with existing values.
func RunSetupWizardWithConfig(lmStudioURL string, ollamaURL string, activeBackend config.Backend, agentDirs []string, skillsDir string, fsRoots []string, braveKey string) (SetupResult, error) {
	inner := newSetupModel(lmStudioURL, ollamaURL, activeBackend, agentDirs, skillsDir, fsRoots, braveKey, "", "", false)
	wrapper := &setupStandaloneModel{inner: inner}
	p := tea.NewProgram(wrapper, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return SetupResult{}, err
	}
	return inner.Result, nil
}

// setupStandaloneModel wraps SetupModel for use as a standalone tea.Program.
// It converts setupCompleteMsg into tea.Quit.
type setupStandaloneModel struct {
	inner *SetupModel
}

func (s *setupStandaloneModel) Init() tea.Cmd { return s.inner.Init() }

func (s *setupStandaloneModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(setupCompleteMsg); ok {
		return s, tea.Quit
	}
	next, cmd := s.inner.Update(msg)
	s.inner = next.(*SetupModel)
	return s, cmd
}

func (s *setupStandaloneModel) View() string { return s.inner.View() }
