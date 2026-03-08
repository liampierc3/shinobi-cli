package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	agentpkg "github.com/liampierc3/shinobi-cli/internal/agent"
	cmds "github.com/liampierc3/shinobi-cli/internal/commands"
	"github.com/liampierc3/shinobi-cli/internal/config"
	"github.com/liampierc3/shinobi-cli/internal/llm"
	"github.com/liampierc3/shinobi-cli/internal/lmstudio"
	"github.com/liampierc3/shinobi-cli/internal/search"
	skillpkg "github.com/liampierc3/shinobi-cli/internal/skill"
	"github.com/liampierc3/shinobi-cli/internal/storage"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/reflow/wordwrap"
)

// Model is the main application model
type Model struct {
	// UI components
	input           textinput.Model
	menuPromptInput textinput.Model
	styles          Styles
	spin            spinner.Model
	setupOverlay    *SetupModel // non-nil when /config is open
	// viewportLines mirrors the rendered content so we can render a rich view
	// while managing scroll math ourselves. Plain lines drive the math, styled
	// lines keep ANSI coloring for display.
	viewportLines       []string
	viewportStyledLines []string
	viewportHeight      int
	viewportYOffset     int

	// Application state
	messages              []Message
	isLoading             bool
	isStreaming           bool
	streamBuffer          strings.Builder
	streamThinkingBuffer  strings.Builder
	markdownRenderer      *MarkdownRenderer
	tokenChan             chan llm.StreamToken
	cancelStream          context.CancelFunc
	scrolledUp            bool
	err                   error
	ready                 bool
	width                 int
	height                int
	currentModelID        string
	currentBackend        config.Backend
	dynamicModelSelection bool
	responseModel         string
	lastViewportUpdate    time.Time
	lastThinkingUpdate    time.Time
	lastRequestPromptLen  int
	// Debug/telemetry for streaming; only used when OLLAMA_TUI_DEBUG_STREAM is set.
	debugStreamTokenCount int

	// Tracks whether the most recent prompt sent to the model was generated
	// from web search results. Used to improve messaging when the model
	// returns an empty response after a search-based question.
	lastPromptWasSearchSynthesis bool

	// Tracks the most recent cancellation initiated by the UI so we can
	// differentiate self-cancellation from genuine backend errors.
	lastCancelReason string
	lastCancelTime   time.Time

	// Services
	llmClients             map[config.Backend]llm.Client
	modelRoutes            []config.ModelRoute
	currentModelRouteIndex int
	store                  *storage.Store
	searchClient           search.Client
	fsRoot                 string   // primary root (first in fsRoots)
	fsRoots                []string // all configured roots
	contextPaths           []string
	backendURL             string // active backend URL
	lmStudioURL            string
	ollamaURL              string
	agentDirs              []string
	skillsDir              string
	braveToken             string
	tavilyKey              string
	serpapiKey             string
	ddgEnabled             bool

	// Commands
	baseCommands               []cmds.Command
	commandMap                 map[string]cmds.Command
	commandPalette             *CommandPalette
	pendingCommand             *cmds.Command
	defaultPrompt              string
	commandMenuOn              bool
	commandFilter              string
	commandMenuMode            commandMenuMode
	agentMenuCommands          []cmds.Command
	agentMenuIndex             map[string]agentpkg.Agent
	agentGroupMenuCommands     []cmds.Command
	agentGroups                map[string][]agentpkg.Agent
	agentGroupNames            []string
	currentAgentGroup          string
	agents                     []agentpkg.Agent
	activeAgent                *agentpkg.Agent
	skills                     []skillpkg.Skill
	skillMap                   map[string]skillpkg.Skill
	skillMenuCommands          []cmds.Command
	activeSkills               map[string]skillpkg.Skill
	activeSkillOrder           []string
	modelOptions               []modelOption
	modelCommandMap            map[string]string
	modelMenuCommands          []cmds.Command
	helpVisible                bool
	principlesVisible          bool
	sessions                   []sessionSummary
	sessionMenuItems           []cmds.Command
	sessionMap                 map[string]sessionSummary
	projectsEnabled            bool
	currentProjectID           int64
	currentSessionID           int64
	currentSessionLabel        string
	menuMode                   modalMenuMode
	menuStack                  []modalMenuMode
	menuItems                  []modalMenuItem
	menuSelection              int
	menuStatus                 string
	menuPrompt                 *menuPromptState
	menuConfirm                *menuConfirmState
	pendingFSWrite             *fsWriteRequest
	pendingToolApproval        *toolApprovalRequest
	toolRunnerEvents           []toolResult
	toolRunnerGuidance         string
	toolRunnerLastError        string
	toolRunnerLastAction       string
	toolRunnerPendingApproval  string
	toolRunnerPendingWritePath string
	showStatusBar              bool
	showTimestamps             bool
	showThinking               bool
	showContext                bool // when false, pinned context blocks are collapsed to one line
	autoLoadSkills             []string
	sessionTitleUserSet        bool
	inputFocusCaptured         bool
	modelWarmStatus            string
	availableModels            map[string]bool
	missingModelWarn           map[string]bool
	loadedContextFile          string // Filename of auto-loaded system context (for status bar)
}

type commandMenuMode int

const (
	menuModeHidden commandMenuMode = iota
	menuModeCommands
	menuModeAgentGroups
	menuModeAgents
	menuModeModels
	menuModeSessions
	menuModeSkills
)

type modelOption struct {
	Label string
	ID    string
}

type sessionSummary struct {
	ID          int64
	ProjectID   int64
	Name        string
	Title       string
	Description string
	Focus       string
	UpdatedAt   time.Time
}

var defaultModelOptions = func() []modelOption {
	return []modelOption{}
}()

var builtinPaletteCommands = []cmds.Command{
	{Name: "agent", Description: "Switch agents", Color: "#89b4fa", Priority: 1},
	{Name: "skill", Description: "Apply skills", Color: "#f5c2e7", Priority: 3},
	{Name: "history", Description: "Browse past conversations", Color: "#a6e3a1", Priority: 5},
	{Name: "new", Description: "Start a fresh chat", Color: "#a6adc8", Priority: 7},
	{Name: "config", Description: "Edit configuration", Color: "#a6adc8", Priority: 10},
	{Name: "principles", Description: "Show design principles", Color: "#a6adc8", Priority: 11},
	{Name: "help", Description: "Toggle help panel", Color: "#a6adc8", Priority: 12},
}

const (
	messageGutterWidth = 3
	minContentWidth    = 10
)

var (
	csiSequenceRegex  = regexp.MustCompile(`\x1b\[[0-9;:<=>?]*[ -/]*[@-~]`)
	oscSequenceRegex  = regexp.MustCompile(`\x1b\][^\x07]*(?:\x07|\x1b\\)`)
	singleEscapeRegex = regexp.MustCompile(`\x1b[@-_]`)
)

// NewModel creates a new application model.
// initialModelID is optional; when empty, Shinobi selects the first model
// returned by LM Studio /v1/models at startup.
func NewModel(initialModelID string, store *storage.Store, searchClient search.Client, fsRoots []string, contextPaths []string, autoLoadSkills []string, activeURL string, backendAPIKey string, activeBackend config.Backend, lmStudioURL string, ollamaURL string, agentDirs []string, skillsDir string, braveToken string, tavilyKey string, serpapiKey string, ddgEnabled bool) (*Model, error) {
	// Construct clients for all configured backends so switching mid-session works.
	llmClients := make(map[config.Backend]llm.Client)
	if strings.TrimSpace(lmStudioURL) != "" {
		llmClients[config.BackendLMStudio] = lmstudio.NewClient(lmStudioURL, backendAPIKey)
	}
	if strings.TrimSpace(ollamaURL) != "" {
		llmClients[config.BackendOllama] = lmstudio.NewClient(ollamaURL, "")
	}

	// Create text input
	ti := textinput.New()
	defaultPrompt := "Type a message..."
	ti.Placeholder = defaultPrompt
	ti.Focus()
	// Allow long prompts and pasted blocks; 0 = no limit.
	ti.CharLimit = 0
	ti.Width = 50

	// Create markdown renderer
	mdRenderer, err := NewMarkdownRenderer(50)
	if err != nil {
		return nil, fmt.Errorf("failed to create markdown renderer: %w", err)
	}

	commandLoader := cmds.DefaultLoader()
	commandList, err := commandLoader.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load commands: %w", err)
	}
	palette := newCommandPalette(commandList, DefaultStyles())
	commandMap := make(map[string]cmds.Command, len(commandList))
	for _, c := range commandList {
		commandMap[c.Name] = c
	}

	agentLoader := agentpkg.LoaderWithExtraDirs(agentDirs)
	agentList, err := agentLoader.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load agents: %w", err)
	}
	if len(agentList) == 0 {
		return nil, fmt.Errorf("no agents found; add files in agents/")
	}
	agentGroups, allGroupNames := buildAgentGroups(agentList)
	agentGroupNames := selectVisibleAgentGroups(allGroupNames, agentGroups)
	agentGroupMenu := buildAgentGroupMenuCommands(agentGroupNames, agentGroups)

	skillLoader := skillpkg.LoaderWithDir(skillsDir)
	skillList, err := skillLoader.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to load skills: %v\n", err)
	}
	skillMap := make(map[string]skillpkg.Skill, len(skillList))
	for _, skill := range skillList {
		skillMap[skill.Name] = skill
	}
	startupModelID, startupOptions, availableModels, err := resolveStartupModels(initialModelID, activeBackend, llmClients[activeBackend])
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(initialModelID) != "" && !availableModels[startupModelID] {
		fmt.Fprintf(os.Stderr, "Warning: default_model %q was not found in backend; attempting anyway\n", startupModelID)
	}

	if searchClient == nil {
		searchClient = search.DisabledClient{}
	}
	// Normalise roots; derive the primary root from the first entry.
	cleanRoots := make([]string, 0, len(fsRoots))
	for _, r := range fsRoots {
		if r == "" {
			continue
		}
		cleanRoots = append(cleanRoots, filepath.Clean(r))
	}
	if len(cleanRoots) == 0 {
		cleanRoots = []string{"."}
	}
	fsRoot := cleanRoots[0]
	// Projects are supported in storage but disabled in the UI for now to keep
	// the interaction model focused on a single default workspace.
	projectsEnabled := false
	// Create spinner for thinking/processing indicator
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := &Model{
		input:                  ti,
		menuPromptInput:        textinput.New(),
		styles:                 DefaultStyles(),
		spin:                   sp,
		viewportLines:          []string{""},
		viewportStyledLines:    []string{""},
		llmClients:             llmClients,
		store:                  store,
		searchClient:           searchClient,
		fsRoot:                 fsRoot,
		fsRoots:                cleanRoots,
		backendURL:             activeURL,
		lmStudioURL:            lmStudioURL,
		ollamaURL:              ollamaURL,
		agentDirs:              append([]string(nil), agentDirs...),
		skillsDir:              skillsDir,
		braveToken:             braveToken,
		tavilyKey:              tavilyKey,
		serpapiKey:             serpapiKey,
		ddgEnabled:             ddgEnabled,
		contextPaths:           append([]string(nil), contextPaths...),
		autoLoadSkills:         append([]string(nil), autoLoadSkills...),
		messages:               []Message{},
		currentModelID:         startupModelID,
		currentBackend:         activeBackend,
		dynamicModelSelection:  strings.TrimSpace(initialModelID) == "",
		markdownRenderer:       mdRenderer,
		baseCommands:           commandList,
		commandMap:             commandMap,
		commandPalette:         palette,
		defaultPrompt:          defaultPrompt,
		commandMenuMode:        menuModeHidden,
		agents:                 agentList,
		agentMenuIndex:         make(map[string]agentpkg.Agent),
		agentGroups:            agentGroups,
		agentGroupNames:        agentGroupNames,
		agentGroupMenuCommands: agentGroupMenu,
		skills:                 skillList,
		skillMap:               skillMap,
		activeSkills:           make(map[string]skillpkg.Skill),
		modelOptions:           startupOptions,
		modelCommandMap:        make(map[string]string, len(startupOptions)),
		sessions:               nil,
		sessionMap:             make(map[string]sessionSummary),
		projectsEnabled:        projectsEnabled,
		availableModels:        availableModels,
		missingModelWarn:       make(map[string]bool),
		currentSessionLabel:    "live-session",
		menuMode:               modalMenuHidden,
		showStatusBar:          false,
		showTimestamps:         true,
	}
	m.menuPromptInput.CharLimit = 120
	m.menuPromptInput.Placeholder = "Type value..."
	m.menuPromptInput.Prompt = ""
	m.menuPromptInput.SetValue("")

	if store != nil {
		project, err := store.EnsureProject(storage.DefaultProjectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: unable to ensure default project: %v\n", err)
		} else {
			m.currentProjectID = project.ID
			m.currentSessionLabel = "new chat"
		}
	}
	m.currentAgentGroup = defaultAgentGroup(m.agentGroupNames)
	m.activeAgent = &m.agents[0]
	m.applyStoredSettings()
	m.agentMenuCommands = m.buildAgentMenuCommands()
	m.modelMenuCommands = m.buildModelMenuCommands()
	m.skillMenuCommands = m.buildSkillMenuCommands()
	m.enableAutoSkills()
	for _, opt := range m.modelOptions {
		m.modelCommandMap[opt.Label] = opt.ID
	}
	m.refreshSessionMenu()

	// Auto-load system context for initial session
	m.loadSystemContextAuto()
	m.updateViewportContent()

	return m, nil
}

// resolveStartupModels queries the active backend and builds the model option
// list. Returns the initial model ID, all model options, and available model IDs.
func resolveStartupModels(initialModelID string, backend config.Backend, client llm.Client) (string, []modelOption, map[string]bool, error) {
	if client == nil {
		return strings.TrimSpace(initialModelID), nil, map[string]bool{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ids, err := client.ListModels(ctx)
	if err != nil || len(ids) == 0 {
		// Backend unreachable — start anyway, errors will appear on first send.
		return strings.TrimSpace(initialModelID), nil, map[string]bool{}, nil
	}

	available := make(map[string]bool, len(ids))
	var options []modelOption
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		available[id] = true
		options = append(options, modelOption{Label: id, ID: id})
	}

	override := strings.TrimSpace(initialModelID)
	if override != "" {
		return override, options, available, nil
	}
	if len(options) > 0 {
		return options[0].ID, options, available, nil
	}
	return "", options, available, nil
}

// resolveModelForRequest returns the model ID to use for an outbound request.
// When dynamic model selection is active, it refreshes from the current
// backend's /v1/models each turn and uses the first returned ID.
func (m *Model) resolveModelForRequest(ctx context.Context, client llm.Client) (string, error) {
	if m == nil {
		return "", fmt.Errorf("model unavailable")
	}
	if !m.dynamicModelSelection {
		modelID := strings.TrimSpace(m.currentModelID)
		if modelID == "" {
			return "", fmt.Errorf("no active model ID")
		}
		return modelID, nil
	}
	if client == nil {
		return "", fmt.Errorf("backend client unavailable")
	}

	ids, err := client.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to query backend models: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("backend returned no models")
	}

	available := make(map[string]bool, len(ids))
	modelID := ""
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		available[id] = true
		if modelID == "" {
			modelID = id
		}
	}
	if modelID == "" {
		return "", fmt.Errorf("backend returned no usable model IDs")
	}

	m.availableModels = available
	m.setCurrentModelFromID(modelID)
	return modelID, nil
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spin.Tick)
}

func (m *Model) welcomeText() string {
	// ASCII shinobi mascot
	shinobi := `      ╱
    ▄█▄╱
   ▄███▄
   █ ● █
    ███
   ▐█ █▌`

	// Green terminal-style prompt
	greenStart := "\033[32m"
	greenEnd := "\033[0m"
	prompt := fmt.Sprintf("%s> shinobi%s", greenStart, greenEnd)

	return fmt.Sprintf("%s\n\n%s\nType a message and press Enter to chat.", shinobi, prompt)
}

// responseMsg is sent when we receive a response from the backend (non-streaming)
type responseMsg struct {
	content string
	model   string
}

// streamTokenMsg is sent for each token during streaming
type streamTokenMsg struct {
	token    string
	thinking bool
}

type toolApprovalRequest struct {
	kind       string
	target     string
	note       string
	content    string
	hasContent bool
}

// streamCompleteMsg is sent when streaming is complete
type streamCompleteMsg struct {
	model string
}

type searchResultMsg struct {
	query   string
	results []search.Result
	err     error
}

type execResultMsg struct {
	command string
	result  toolResult
}

// streamStartMsg is sent when streaming starts
type streamStartMsg struct{}

// toolRunnerCompleteMsg is sent when ToolRunner finishes
type toolRunnerCompleteMsg struct {
	value string
	err   error
}

// errMsg is sent when an error occurs
type errMsg struct {
	err error
}

// defaultMaxHistoryExchanges controls how many recent user/assistant exchanges
// we keep in full when sending context to the model. Each exchange is
// approximated as a single user or assistant message, so this roughly
// corresponds to twice as many raw messages. This only affects what we send on
// each request; the full history remains persisted in SQLite.
const defaultMaxHistoryExchanges = 10

// isPinnedContextMessage returns true for system messages that represent
// important injected context we want to preserve even when older, such as
// auto-loaded system context, manual /inject and /context blocks, search
// results, filesystem reads, and other injected summaries.
func isPinnedContextMessage(msg Message) bool {
	if msg.Role != "system" {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	if isUIOnlySystemMessage(content) {
		return false
	}
	switch {
	case strings.HasPrefix(content, "=== INJECTED CONTEXT:"):
		return true
	case strings.HasPrefix(content, "=== CONNECTION PATTERNS ==="):
		return true
	case strings.HasPrefix(content, "=== RECENT STREAM-OF-CONSCIOUSNESS ==="):
		return true
	case strings.HasPrefix(content, "=== TOOLS AVAILABLE ==="):
		return true
	case strings.HasPrefix(content, "<filesystem_map>"):
		return true
	case strings.HasPrefix(content, "Contents of "):
		return true
	case strings.HasPrefix(content, "Web search results for "):
		return true
	case strings.HasPrefix(content, "TOOL_RESULT("):
		return true
	default:
		return false
	}
}

// contextSummaryLine returns a compact one-line label for a pinned context message.
// Used when showContext is false to collapse large injected blocks.
func contextSummaryLine(content string) string {
	content = strings.TrimSpace(content)
	switch {
	case strings.HasPrefix(content, "<filesystem_map>"):
		return "fs: loaded  —  ctrl+p to expand"
	case strings.HasPrefix(content, "=== INJECTED CONTEXT:"):
		// Extract filename from "=== INJECTED CONTEXT: filename ==="
		rest := strings.TrimPrefix(content, "=== INJECTED CONTEXT:")
		if idx := strings.Index(rest, "==="); idx > 0 {
			name := strings.TrimSpace(rest[:idx])
			return "context: " + name + "  —  ctrl+p to expand"
		}
		return "context: loaded  —  ctrl+p to expand"
	case strings.HasPrefix(content, "=== TOOLS AVAILABLE ==="):
		return "tools: loaded  —  ctrl+p to expand"
	case strings.HasPrefix(content, "=== CONNECTION PATTERNS ==="):
		return "memory: loaded  —  ctrl+p to expand"
	case strings.HasPrefix(content, "=== RECENT STREAM-OF-CONSCIOUSNESS ==="):
		return "stream: loaded  —  ctrl+p to expand"
	case strings.HasPrefix(content, "Web search results for "):
		// Keep search results visible — they're directly relevant to the response
		return content
	case strings.HasPrefix(content, "Contents of "):
		// Keep file reads visible too
		return content
	default:
		return content
	}
}

func isUIOnlySystemMessage(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "UI_ONLY:")
}

// buildChatHistoryMessages selects which prior messages from m.messages should
// be sent to the model for the current turn. The policy is:
//   - Keep the last N (maxHistoryExchanges) user/assistant messages in full.
//   - Always include any system messages between those recent messages so that
//     injected context (search results, auto memory context, notices) remains
//     aligned with the conversation.
//   - Always preserve "pinned" system context messages, such as auto-loaded
//     system context and manual /inject and /context blocks, even if they are
//     older than the recent window.
//   - Never mutate or delete stored history; this only shapes the request
//     payload.
func (m *Model) buildChatHistoryMessages() []llm.Message {
	if len(m.messages) == 0 {
		return nil
	}

	limit := historyExchangeLimit()
	if limit <= 0 {
		result := make([]llm.Message, 0, len(m.messages))
		for _, msg := range m.messages {
			if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system" {
				continue
			}
			if msg.Role == "system" && isUIOnlySystemMessage(msg.Content) {
				continue
			}
			result = append(result, llm.Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
		return result
	}

	keep := make([]bool, len(m.messages))

	// First pass: walk backward and keep the last N user/assistant messages.
	// We approximate "exchanges" as individual messages; for a typical
	// user/assistant pair, this yields ~maxHistoryExchanges pairs.
	userAssistantCount := 0
	earliestKeptIndex := len(m.messages)
	for i := len(m.messages) - 1; i >= 0; i-- {
		role := m.messages[i].Role
		if role != "user" && role != "assistant" {
			continue
		}
		if userAssistantCount >= limit {
			break
		}
		keep[i] = true
		userAssistantCount++
		if i < earliestKeptIndex {
			earliestKeptIndex = i
		}
	}

	// If we didn't keep any conversational messages (should be rare), we can
	// safely return nothing here – the harness, agent, and current user input
	// will still be sent.
	if earliestKeptIndex == len(m.messages) {
		return nil
	}

	// Second pass: include all system messages that fall within the recent
	// conversational window so that injected context, search results, and
	// auto memory context attached to recent turns are preserved.
	for i := earliestKeptIndex; i < len(m.messages); i++ {
		if m.messages[i].Role == "system" {
			keep[i] = true
		}
	}

	// Third pass: include pinned context system messages from anywhere in the
	// history (e.g., auto-loaded system context, /inject, /context). This is
	// critical for context injection to keep working even after many turns.
	for i, msg := range m.messages {
		if isPinnedContextMessage(msg) {
			keep[i] = true
		}
	}

	// Finally, build the ordered slice of messages for the model. We continue to
	// exclude error-role messages from the model request.
	result := make([]llm.Message, 0, len(m.messages))
	for i, msg := range m.messages {
		if !keep[i] {
			continue
		}
		if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system" {
			continue
		}
		if msg.Role == "system" && isUIOnlySystemMessage(msg.Content) {
			continue
		}
		result = append(result, llm.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	return result
}

// harnessSystemPrompt builds the neutral harness block that describes
// the runtime environment and tool/safety rules. Persona and task focus
// are delegated to the active agent configuration.
func (m *Model) harnessSystemPrompt() string {
	roots := make([]string, 0, len(m.fsRoots))
	for _, r := range m.fsRoots {
		if abs, err := filepath.Abs(r); err == nil {
			roots = append(roots, abs)
		} else {
			roots = append(roots, r)
		}
	}
	rootsLine := strings.Join(roots, ", ")

	sessionID := m.currentSessionLabel
	if m.currentSessionID != 0 {
		sessionID = fmt.Sprintf("%d", m.currentSessionID)
	}

	return fmt.Sprintf(`<harness>
Runtime: cli (shinobi / lp)
Filesystem roots: %s
Session ID: %s

Runtime rules:
- Never execute destructive commands without explicit user confirmation
- Respect .gitignore and system files
- Assume all data is local and private
- Keep chain-of-thought/private reasoning internal unless the user explicitly asks for it

Everything else – personality, task focus, response style – is delegated to the active agent configuration.
</harness>`, rootsLine, sessionID)
}

func (m *Model) toolsContextMessage() string {
	root := m.fsRoot
	if root == "" {
		root = "."
	}
	var b strings.Builder
	b.WriteString("=== TOOLS AVAILABLE ===\n\n")
	b.WriteString("- web_search: current/external info (tool-runner controlled)\n")
	b.WriteString("- bash_exec: run bash commands (agent requests may require approval for dangerous commands)\n")
	b.WriteString("- fs_read: read files (absolute paths allowed; relative paths resolved under workspace root)\n")
	b.WriteString("- fs_write: staged write requires explicit approval\n\n")
	b.WriteString("User shortcuts:\n")
	b.WriteString("- !<command> runs bash immediately\n")
	b.WriteString("- /exec <command> runs bash immediately\n")
	b.WriteString("- /fs read <path>\n")
	b.WriteString("- /fs write <path> then paste content, then /fs apply\n")
	if barePathAutoReadEnabled() {
		b.WriteString("- Paste a file/dir path to auto-read/list it before answering\n")
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Workspace root: %s\n\n", root))
	b.WriteString("Paths of interest:\n")
	for _, r := range m.fsRoots {
		b.WriteString("- " + r + "\n")
	}
	b.WriteString("\n")
	b.WriteString("Notes:\n")
	b.WriteString("- Type paths/args directly (no <angle brackets>)\n")
	b.WriteString("- If a command is blocked, run it manually with !<command>\n\n")
	b.WriteString("=== END TOOLS ===")
	return b.String()
}

// skillsSystemPrompt returns the combined skill prompts that should apply to every turn.
func (m *Model) skillsSystemPrompt() string {
	if m == nil || len(m.activeSkillOrder) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("SYSTEM INSTRUCTION: The following skill blocks are ACTIVE instructions.\n")
	b.WriteString("- Apply them silently to your response.\n")
	b.WriteString("- Do not mention the skills or that they are active.\n")
	b.WriteString("- Do not change your identity unless a skill explicitly instructs you to.\n")
	b.WriteString("- If a skill conflicts with the active agent persona, treat it as a style/strategy overlay.\n")
	b.WriteString("Active skill blocks:\n")
	for _, key := range m.activeSkillOrder {
		skill, ok := m.activeSkills[key]
		if !ok {
			continue
		}
		if skill.Name == "" || strings.TrimSpace(skill.Prompt) == "" {
			continue
		}
		b.WriteString("\n=== SKILL: ")
		b.WriteString(skill.Name)
		b.WriteString(" ===\n")
		b.WriteString("This skill is ACTIVE. Apply it now.\n")
		if strings.TrimSpace(skill.Description) != "" {
			b.WriteString("Description: ")
			b.WriteString(strings.TrimSpace(skill.Description))
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimSpace(skill.Prompt))
		b.WriteString("\n=== END SKILL ===\n")
	}
	return strings.TrimSpace(b.String())
}

// sendMessage sends a message to the active LM Studio model with streaming.
func (m *Model) sendMessage(content string, _ string, tokenChan chan llm.StreamToken) tea.Cmd {
	return func() tea.Msg {
		// Build the message history for context. We always prepend the harness
		// and agent prompts, then include a truncated view of the prior
		// conversation built by buildChatHistoryMessages().
		messages := make([]llm.Message, 0, len(m.messages)+3)

		// 1) Base harness – neutral environment/safety description
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: m.harnessSystemPrompt(),
		})

		// 2) Active agent system prompt (persona + workflow)
		if prompt := m.agentSystemPrompt(); prompt != "" {
			messages = append(messages, llm.Message{Role: "system", Content: prompt})
		}

		// 3) Active skills (optional)
		if skillPrompt := m.skillsSystemPrompt(); skillPrompt != "" {
			messages = append(messages, llm.Message{Role: "system", Content: skillPrompt})
		}

		// 4) Tool summary (if any)
		if summary := m.toolSummarySystemPrompt(); summary != "" {
			messages = append(messages, llm.Message{Role: "system", Content: summary})
		}

		// 5) Conversation history (truncated) – prior user/assistant messages
		// plus critical system context messages.
		//
		// CRITICAL: System messages that represent injected context MUST be
		// included for context injection to work. The helper below preserves:
		//   - Auto-loaded system context (loadSystemContextAuto)
		//   - Manual context injection (/context, /inject commands)
		//   - Search result blocks
		//   - Recent auto memory context and other system messages tied to the
		//     last few exchanges
		// Do not change the helper to drop all system messages.
		if history := m.buildChatHistoryMessages(); len(history) > 0 {
			messages = append(messages, history...)
		}

		// Add the new user message
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: content,
		})

		client := m.llmClients[m.currentBackend]
		if client == nil {
			tokenChan <- llm.StreamToken{
				Content: backendUnavailableError(m.currentBackend),
				Done:    true,
			}
			return streamStartMsg{}
		}

		timeout := m.requestTimeout()
		var ctx context.Context
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), timeout)
		} else {
			ctx, cancel = context.WithCancel(context.Background())
		}

		modelID, err := m.resolveModelForRequest(ctx, client)
		if err != nil {
			tokenChan <- llm.StreamToken{
				Content: fmt.Sprintf("ERROR: %v", err),
				Done:    true,
			}
			cancel()
			return streamStartMsg{}
		}
		m.responseModel = modelID

		// Store cancel function so we can abort stuck requests via Esc/Ctrl+C.
		m.cancelStream = cancel

		if debugStreamLoggingEnabled() {
			logStreamDebug("stream start: model=%s prompt_len=%d messages=%d", modelID, len(content), len(messages))
		}

		go func() {
			defer cancel()
			err := client.ChatStream(ctx, llm.ChatRequest{
				Messages: messages,
				Model:    modelID,
			}, tokenChan)

			if err != nil {
				// Send error through channel as a special token
				tokenChan <- llm.StreamToken{
					Content: fmt.Sprintf("ERROR: %v", err),
					Done:    true,
				}
			}
		}()

		// Return a command that listens to the token channel
		return streamStartMsg{}
	}
}

// waitForTokens creates a command that waits for the next token
func waitForTokens(tokenChan <-chan llm.StreamToken) tea.Cmd {
	return func() tea.Msg {
		token, ok := <-tokenChan
		if !ok {
			// Channel closed unexpectedly
			return errMsg{err: fmt.Errorf("stream channel closed unexpectedly")}
		}

		// Check if this is an error message
		if token.Done && strings.HasPrefix(token.Content, "ERROR:") {
			errText := strings.TrimPrefix(token.Content, "ERROR: ")
			return errMsg{err: fmt.Errorf("%s", errText)}
		}

		if token.Done {
			return streamCompleteMsg{model: ""}
		}

		return streamTokenMsg{token: token.Content, thinking: token.Thinking}
	}
}

// Update handles messages and updates the model
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		viewportBaseHeight := msg.Height - 4
		if viewportBaseHeight < 1 {
			viewportBaseHeight = 1
		}

		if !m.ready {
			m.ready = true
		}
		m.setViewportHeight(viewportBaseHeight)

		// Update input width
		m.input.Width = msg.Width - 4

		// Update markdown renderer width
		if m.markdownRenderer != nil {
			m.markdownRenderer.SetWidth(msg.Width)

			// Re-render all cached markdown with new width
			m.reRenderCachedMessages()
		}

		// Update viewport content
		m.updateViewportContent()

	case setupCompleteMsg:
		if m.setupOverlay != nil {
			result := msg.Result
			m.setupOverlay = nil
			m.hideCommandMenu()
			m.input.SetValue("")
			m.input.Focus()
			if !result.Cancelled {
				m.applyAndSaveConfig(result)
			}
		}
		return m, nil

	case tea.MouseMsg:
		if m.handleMouseScroll(msg) {
			return m, nil
		}

	case tea.KeyMsg:
		// When the config overlay is open, route all key events to it.
		if m.setupOverlay != nil {
			next, cmd := m.setupOverlay.Update(msg)
			m.setupOverlay = next.(*SetupModel)
			return m, cmd
		}
		// CRITICAL: Filter out ANSI escape sequences BEFORE processing
		// This prevents SGR mouse codes from being rendered in the input field
		keyStr := msg.String()
		if strings.Contains(keyStr, "\x1b[") || strings.Contains(keyStr, "[<") {
			// This is an ANSI sequence, not real input - drop it immediately
			return m, nil
		}
		if m.menuPrompt != nil && keyStr == "esc" {
			m.cancelMenuPrompt(true)
			return m, nil
		}
		if cmd, handled := m.handleModalMenuKey(msg); handled {
			return m, cmd
		}

		if m.helpVisible && (keyStr == "esc" || keyStr == "ctrl+c") {
			m.hideHelpPanel()
			return m, nil
		}
		if m.principlesVisible && (keyStr == "esc" || keyStr == "ctrl+c") {
			m.hidePrinciplesPanel()
			return m, nil
		}
		if (m.helpVisible || m.principlesVisible) && keyStr == "/" {
			m.hideHelpPanel()
			m.hidePrinciplesPanel()
			m.input.SetValue("")
			return m, nil
		}
		if m.commandMenuOn && keyStr == "/" {
			m.hideCommandMenu()
			m.input.SetValue("")
			return m, nil
		}
		if cmd, handled := m.handleCommandMenuKey(keyStr); handled {
			return m, cmd
		}

		if m.handleScrollKey(keyStr) {
			return m, nil
		}

		// Global key bindings
		switch keyStr {
		case "ctrl+c":
			// First, allow canceling an in-flight request without quitting.
			if m.isLoading || m.isStreaming {
				m.cancelOngoingRequestWithReason("ctrl_c")
				return m, nil
			}
			if m.menuPrompt != nil {
				m.cancelMenuPrompt(false)
				return m, nil
			}
			if m.pendingCommand != nil {
				m.pendingCommand = nil
				m.input.Placeholder = m.defaultPrompt
				m.input.SetValue("")
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+o":
			// Toggle thinking visibility
			m.showThinking = !m.showThinking
			m.updateViewportContent()
			if !m.scrolledUp {
				m.viewportGotoBottom()
			}
			return m, nil
		case "ctrl+p":
			// Toggle injected context visibility (filesystem map, tools, system context)
			m.showContext = !m.showContext
			m.updateViewportContent()
			if !m.scrolledUp {
				m.viewportGotoBottom()
			}
			return m, nil
		case "esc":
			// Esc cancels a stuck/thinking request.
			if m.isLoading || m.isStreaming {
				m.cancelOngoingRequestWithReason("esc")
				return m, nil
			}
			if m.pendingCommand != nil {
				m.pendingCommand = nil
				m.input.Placeholder = m.defaultPrompt
				m.input.SetValue("")
				return m, nil
			}

		case "enter":
			if m.menuPrompt != nil {
				value := strings.TrimSpace(StripUnwantedANSI(m.input.Value()))
				prompt := m.menuPrompt
				m.menuPrompt = nil
				m.input.SetValue("")
				m.input.Placeholder = m.defaultPrompt
				return m, m.handleMenuPromptSubmit(prompt, value)
			}
			if m.pendingCommand != nil {
				cmdCopy := *m.pendingCommand
				m.pendingCommand = nil
				// Strip any ANSI codes that may have leaked into input
				args := strings.TrimSpace(StripUnwantedANSI(m.input.Value()))
				m.input.SetValue("")
				m.input.Placeholder = m.defaultPrompt
				if cmd := m.startCommand(cmdCopy, args); cmd != nil {
					return m, cmd
				}
				return m, nil
			}
			if m.isLoading || m.isStreaming {
				// Don't send new messages while loading or streaming
				return m, nil
			}

			// Get the input value and strip any ANSI codes that may have leaked in
			value := strings.TrimSpace(StripUnwantedANSI(m.input.Value()))
			if value == "" {
				return m, nil
			}

			if cmd, handled := m.tryExecuteNaturalFSCommand(value); handled {
				return m, cmd
			}

			if m.consumePendingFilesystemContent(value) {
				m.input.SetValue("")
				return m, nil
			}

			if cmd, handled := m.tryExecuteBangCommand(value); handled {
				return m, cmd
			}

			if cmd, handled := m.tryExecuteSlashCommand(value); handled {
				return m, cmd
			}
			if cmd, handled := m.consumePendingToolApproval(value); handled {
				m.input.SetValue("")
				return m, cmd
			}

			if err := m.ensureActiveSession(); err != nil {
				m.showSystemNotice(fmt.Sprintf("Unable to open chat: %v", err))
				return m, nil
			}

			m.hideHelpPanel()
			m.hidePrinciplesPanel()

			// Add user message
			m.appendMessage(NewUserMessage(value))
			m.ensureAutoSessionTitle(value)

			// Clear input
			m.input.SetValue("")

			// Update viewport
			m.updateViewportContent()
			m.viewportGotoBottom()

			// Set loading state immediately so user sees spinner
			m.lastRequestPromptLen = m.totalContextLen() + len(value)
			m.beginThinking()
			m.lastPromptWasSearchSynthesis = false
			m.tokenChan = make(chan llm.StreamToken, 100)
			m.scrolledUp = false

			// Store value for async processing
			valueCopy := value

			// Run ToolRunner asynchronously if needed, otherwise send immediately
			if m.toolRunnerEnabled() && m.shouldRunToolRunner(value) {
				return m, func() tea.Msg {
					if err := m.runToolRunnerTurn(valueCopy); err != nil {
						return toolRunnerCompleteMsg{value: valueCopy, err: err}
					}
					return toolRunnerCompleteMsg{value: valueCopy, err: nil}
				}
			}

			// Send message to the configured backend
			return m, m.sendMessage(value, "", m.tokenChan)
		}

	case toolRunnerCompleteMsg:
		// ToolRunner finished; only send to the LLM when no approval gate is open.
		if msg.err != nil {
			m.showSystemNotice(fmt.Sprintf("ToolRunner error: %v", msg.err))
		}
		if m.pendingToolApproval != nil {
			m.isLoading = false
			m.isStreaming = false
			m.tokenChan = nil
			return m, nil
		}
		if blockedCmd := latestBlockedBashCommand(m.toolRunnerEvents); blockedCmd != "" {
			m.isLoading = false
			m.isStreaming = false
			m.tokenChan = nil

			reply := fmt.Sprintf("I couldn't run `%s` because ToolRunner blocks piped/redirection or potentially destructive shell commands. The command was not executed. Run it manually with `!<command>` or `/exec <command>` if you want to proceed.", blockedCmd)
			assistantMsg := NewAssistantMessage(reply, m.currentModelID)
			if cache := renderMarkdownForWidth(reply, m.width); strings.TrimSpace(cache) != "" {
				assistantMsg.RenderedCache = cache
			}
			m.appendMessage(assistantMsg)
			m.updateViewportContent()
			if !m.scrolledUp {
				m.viewportGotoBottom()
			}
			m.clearToolRunnerContext()
			return m, nil
		}
		return m, m.sendMessage(msg.value, "", m.tokenChan)

	case streamStartMsg:
		// Stream has started, begin waiting for tokens
		m.isLoading = false
		m.isStreaming = true
		m.lastThinkingUpdate = time.Now()
		m.debugStreamTokenCount = 0
		m.streamBuffer.Reset()
		m.streamThinkingBuffer.Reset()
		m.markdownRenderer.Reset()
		return m, waitForTokens(m.tokenChan)

	case streamTokenMsg:
		// Received a token, add to the appropriate buffer and continue listening.
		if msg.thinking {
			m.streamThinkingBuffer.WriteString(msg.token)
		} else {
			m.streamBuffer.WriteString(msg.token)
			// Also add content tokens to markdown renderer (but don't render yet - wait until complete)
			m.markdownRenderer.AppendToken(msg.token)
		}

		if debugStreamLoggingEnabled() {
			m.debugStreamTokenCount++
			logStreamDebug("token #%d len=%d", m.debugStreamTokenCount, len(msg.token))
		}

		m.lastThinkingUpdate = time.Now()

		// Throttle viewport updates to avoid overwhelming it
		// Only update if 50ms have passed since last update
		now := time.Now()
		if now.Sub(m.lastViewportUpdate) >= 50*time.Millisecond {
			m.lastViewportUpdate = now

			// Update viewport content
			m.updateViewportContent()

			// Auto-scroll if user hasn't manually scrolled up
			if !m.scrolledUp {
				m.viewportGotoBottom()
			}
		}

		// Continue waiting for more tokens
		return m, waitForTokens(m.tokenChan)

	case streamCompleteMsg:
		// Stream complete, save the message
		rawContent := m.streamBuffer.String()
		content := strings.TrimSpace(stripToolRunnerTags(rawContent))
		streamThinking := strings.TrimSpace(stripToolRunnerTags(m.streamThinkingBuffer.String()))
		m.lastThinkingUpdate = time.Now()

		// Some models stream all text through reasoning/thinking and leave
		// content empty. Recover a user-facing answer from the reasoning stream
		// instead of dropping the turn.
		if content == "" && streamThinking != "" {
			recoveredThinking, recoveredAnswer := extractThinking(streamThinking)
			if strings.TrimSpace(recoveredAnswer) != "" {
				content = strings.TrimSpace(recoveredAnswer)
				streamThinking = strings.TrimSpace(recoveredThinking)
			} else {
				content = streamThinking
				streamThinking = ""
			}
		}

		// If the model returned nothing, avoid appending a blank message.
		if content == "" {
			// CRITICAL: Reset ALL streaming/loading state immediately
			m.isStreaming = false
			m.isLoading = false
			m.streamBuffer.Reset()
			m.streamThinkingBuffer.Reset()
			m.tokenChan = nil
			m.responseModel = ""
			m.err = nil

			// Update viewport and show a notice instead of a blank reply.
			m.updateViewportContent()
			if !m.scrolledUp {
				m.viewportGotoBottom()
			}
			m.clearToolRunnerContext()

			notice := "Model returned no usable answer for this query. This often indicates a backend issue or an overly short/ambiguous prompt. Try rephrasing or checking your model logs."
			if m.lastPromptWasSearchSynthesis {
				notice = "Model returned no usable answer after using the web search results above. This often indicates a backend issue or a short/ambiguous query. Try rephrasing or checking your model logs."
			}
			m.showSystemNotice(notice)

			// Return immediately to force view refresh and clear spinner
			return m, nil
		}

		// Create message with plain text content (no markdown rendering). We
		// persist the backend-specific model ID here and render a richer label
		// (including backend tag) at display time.
		modelUsed := m.responseModel
		if modelUsed == "" {
			modelUsed = m.currentModelID
		}

		// Extract thinking from content
		thinking, actualContent := extractThinking(content)
		if streamThinking != "" {
			if thinking == "" {
				thinking = streamThinking
			} else if strings.TrimSpace(thinking) != strings.TrimSpace(streamThinking) {
				thinking = streamThinking + "\n\n" + thinking
			}
		}
		assistantMsg := NewAssistantMessage(actualContent, modelUsed)
		assistantMsg.Thinking = thinking

		// Render markdown for assistant messages using actualContent (without thinking)
		if m.markdownRenderer != nil && actualContent != "" {
			// Re-render using only the actual content (thinking stripped out)
			renderer, err := NewMarkdownRenderer(m.width)
			if err == nil {
				renderer.AppendToken(actualContent)
				rendered := renderer.GetFinalContent()
				if strings.TrimSpace(rendered) != "" {
					assistantMsg.RenderedCache = rendered
				}
			}
		}

		m.appendMessage(assistantMsg)

		// CRITICAL: Reset ALL streaming/loading state immediately
		// This must happen before any other operations to prevent stuck spinner
		m.isStreaming = false
		m.isLoading = false
		m.streamBuffer.Reset()
		m.streamThinkingBuffer.Reset()
		m.tokenChan = nil
		m.responseModel = ""
		m.err = nil

		// Update viewport one final time
		m.updateViewportContent()
		if !m.scrolledUp {
			m.viewportGotoBottom()
		}
		m.clearToolRunnerContext()

		// Return immediately to force view refresh and clear spinner
		return m, nil

	case searchResultMsg:
		if msg.err != nil {
			m.showSystemNotice(fmt.Sprintf("Search failed: %v", msg.err))
			return m, nil
		}
		// Inject search results as system message
		m.appendMessage(NewSystemMessage(formatSearchResults(msg.query, msg.results)))
		m.updateViewportContent()
		m.viewportGotoBottom()

		// Automatically trigger synthesis by sending a user message
		synthesisPrompt := fmt.Sprintf("Based on the web search results above, please answer: %s", msg.query)
		m.appendMessage(NewUserMessage(synthesisPrompt))
		m.updateViewportContent()
		m.viewportGotoBottom()

		// Send to model for synthesis
		m.lastRequestPromptLen = m.totalContextLen() + len(synthesisPrompt)
		m.beginThinking()
		m.lastPromptWasSearchSynthesis = true
		m.streamBuffer.Reset()
		m.streamThinkingBuffer.Reset()
		m.tokenChan = make(chan llm.StreamToken, 100)
		return m, m.sendMessage(synthesisPrompt, "", m.tokenChan)

	case execResultMsg:
		indicator := formatToolIndicator(msg.result)
		if indicator == "" {
			indicator = fmt.Sprintf("⏺ Bash(%s)", strings.TrimSpace(msg.command))
		}
		m.appendMessage(NewUIOnlySystemMessage(indicator))
		m.updateViewportContent()
		m.viewportGotoBottom()
		return m, nil

	case responseMsg:
		// Add assistant message (fallback for non-streaming), but avoid appending
		// completely empty responses as real messages.
		content := strings.TrimSpace(stripToolRunnerTags(msg.content))
		if content == "" {
			notice := "Model returned no usable answer for this query. This often indicates a backend issue or an overly short/ambiguous prompt. Try rephrasing or checking your model logs."
			if m.lastPromptWasSearchSynthesis {
				notice = "Model returned no usable answer after using the web search results above. This often indicates a backend issue or a short/ambiguous query. Try rephrasing or checking your model logs."
			}
			m.showSystemNotice(notice)
		} else {
			modelUsed := msg.model
			if modelUsed == "" {
				modelUsed = m.currentModelID
			}

			thinking, actualContent := extractThinking(content)
			assistantMsg := NewAssistantMessage(actualContent, modelUsed)
			assistantMsg.Thinking = thinking

			// Render markdown for non-streaming assistant messages using the
			// current terminal width. This mirrors the streaming completion path.
			cache := renderMarkdownForWidth(actualContent, m.width)
			if strings.TrimSpace(cache) != "" {
				assistantMsg.RenderedCache = cache
			}

			m.appendMessage(assistantMsg)
		}

		// CRITICAL: Reset ALL streaming/loading state immediately
		m.isLoading = false
		m.isStreaming = false
		m.tokenChan = nil
		m.responseModel = ""
		m.err = nil

		// Update viewport
		m.updateViewportContent()
		m.viewportGotoBottom()
		m.clearToolRunnerContext()

		// Return immediately to force view refresh and clear spinner
		return m, nil

	case errMsg:
		// Distinguish between genuine backend errors and errors caused by our
		// own cancellation (Esc/Ctrl+C/watchdog). For self-cancellation,
		// suppress the scary error and rely on the existing cancellation
		// notices/spinner behavior instead.
		isContextCanceled := false
		if msg.err != nil {
			errText := strings.ToLower(msg.err.Error())
			if strings.Contains(errText, "context canceled") || strings.Contains(errText, "context cancelled") {
				isContextCanceled = true
			}
		}

		suppressError := false
		if isContextCanceled && !m.lastCancelTime.IsZero() {
			if time.Since(m.lastCancelTime) < 5*time.Second {
				suppressError = true
			}
		}

		if !suppressError {
			m.appendMessage(NewErrorMessage(fmt.Sprintf("Error: %v", msg.err)))
		} else if debugStreamLoggingEnabled() {
			logStreamDebug("suppressed context canceled error after reason=%s", m.lastCancelReason)
		}

		if debugStreamLoggingEnabled() {
			logStreamDebug("stream error: %v (suppressed=%v)", msg.err, suppressError)
		}

		// CRITICAL: Reset ALL streaming/loading state immediately
		m.isLoading = false
		m.isStreaming = false
		m.tokenChan = nil
		m.responseModel = ""
		m.err = msg.err

		// Update viewport
		m.updateViewportContent()
		m.viewportGotoBottom()
		m.clearToolRunnerContext()

		// Return immediately to force view refresh and clear spinner
		return m, nil

	}

	// Defensive: if there's no active token channel, force thinking flags off.
	// Do this BEFORE processing spinner ticks to avoid race conditions.
	// This catches edge cases where streaming state wasn't properly cleared.
	if m.tokenChan == nil && (m.isLoading || m.isStreaming) {
		m.isLoading = false
		m.isStreaming = false
		m.err = nil
		m.responseModel = ""
		// Force viewport refresh to clear any lingering spinner display
		m.updateViewportContent()
	}

	// Update input + spinner
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.spin, cmd = m.spin.Update(msg)
	cmds = append(cmds, cmd)

	// If the spinner advanced while we're waiting on a model response, refresh
	// the viewport and run a lightweight watchdog to prevent stuck thinking
	// states when backends hang or never send a completion/error.
	if _, ok := msg.(spinner.TickMsg); ok {
		if m.isThinking() {
			now := time.Now()
			if m.lastThinkingUpdate.IsZero() {
				m.lastThinkingUpdate = now
			} else {
				timeout := m.currentThinkingTimeout()
				if timeout > 0 && now.Sub(m.lastThinkingUpdate) > timeout {
					if debugStreamLoggingEnabled() {
						logStreamDebug("watchdog timeout (timeout=%s, tokens=%d); canceling stuck request", timeout.String(), m.debugStreamTokenCount)
					}
					m.showSystemNotice("Request took too long and was cancelled. Press Enter to try again, or adjust your model/search settings.")
					m.cancelOngoingRequestWithReason("watchdog_timeout")
				}
			}
		}
		if m.isThinking() {
			m.updateViewportContent()
		}
	}

	// Defensive: strip any ANSI codes that may have leaked into the input field
	// This prevents mouse tracking codes from becoming visible in the input
	if currentValue := m.input.Value(); currentValue != "" {
		cleaned := StripUnwantedANSI(currentValue)
		if cleaned != currentValue {
			m.input.SetValue(cleaned)
		}
	}

	m.updateCommandPaletteState()

	return m, tea.Batch(cmds...)
}

func (m *Model) handleMouseScroll(msg tea.MouseMsg) bool {
	if m.viewportHeight <= 0 || len(m.viewportLines) <= m.viewportHeight {
		return false
	}

	var delta int
	switch msg.Type {
	case tea.MouseWheelUp:
		delta = -m.mouseScrollLines()
	case tea.MouseWheelDown:
		delta = m.mouseScrollLines()
	default:
		return false
	}

	if delta == 0 {
		return false
	}

	originalOffset := m.viewportYOffset
	m.viewportYOffset += delta
	m.clampViewportOffset()
	if m.viewportYOffset != originalOffset {
		m.scrolledUp = !m.viewportAtBottom()
		if !m.scrolledUp {
			m.viewportGotoBottom()
		}
		return true
	}
	return false
}

func (m *Model) handleBarePathInput(value string) tea.Cmd {
	path := strings.TrimSpace(value)
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	stat, err := os.Stat(path)
	if err != nil {
		m.showSystemNotice(fmt.Sprintf("Path not found: %v", err))
		return nil
	}
	if stat.IsDir() {
		return m.fsList(path)
	}
	return m.handleInjectCommand(path)
}

// looksLikeBarePath returns true when the input looks like a standalone
// filesystem path that should be auto-injected as context (via /inject)
// instead of being sent to the model as a normal chat message.
func (m *Model) looksLikeBarePath(input string) bool {
	s := strings.TrimSpace(input)
	if s == "" {
		return false
	}

	if m != nil && m.isKnownSlashCommandInput(s) {
		return false
	}

	// Must not contain whitespace; this keeps it from firing on sentences.
	if strings.ContainsAny(s, " \t\n") {
		return false
	}

	// Accept absolute paths and home-prefixed paths only. Relative paths can
	// still be used via explicit /inject or natural-language commands.
	if !(strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/")) {
		return false
	}

	return true
}

func (m *Model) isKnownSlashCommandInput(input string) bool {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return false
	}
	name := strings.TrimPrefix(parts[0], "/")
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return true
	}
	switch name {
	case "menu", "model", "agent", "agents", "skill", "inject", "context", "fs", "new", "rename", "delete", "config", "principles", "help":
		return true
	}
	if m != nil && m.commandMap != nil {
		if _, ok := m.commandMap[name]; ok {
			return true
		}
	}
	return false
}

// wrapText wraps text to the specified usable width, respecting existing newlines and rune widths
func wrapText(text string, usableWidth int) string {
	if usableWidth <= 0 {
		return text
	}
	if usableWidth < minContentWidth {
		usableWidth = minContentWidth
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")
	for idx, line := range lines {
		if idx > 0 {
			result.WriteString("\n")
		}

		if line == "" {
			continue
		}

		result.WriteString(wordwrap.String(line, usableWidth))
	}

	return result.String()
}

// contentAreaWidth returns the number of columns available for message content once
// lipgloss padding/margins and the message gutter are applied.
func (m *Model) contentAreaWidth() int {
	if m == nil {
		return minContentWidth
	}
	usable := m.width - messageGutterWidth - m.messageHorizontalInset()
	if usable < minContentWidth {
		return minContentWidth
	}
	return usable
}

// messageHorizontalInset computes the maximum horizontal padding+margin (left+right)
// applied by any message style so our wrap width accounts for the widest style.
func (m *Model) messageHorizontalInset() int {
	styles := []lipgloss.Style{
		m.styles.UserMessage,
		m.styles.AssistantMessage,
		m.styles.SystemMessage,
		m.styles.ErrorMessage,
	}
	maxInset := 0
	for _, style := range styles {
		inset := style.GetPaddingLeft() + style.GetPaddingRight() + style.GetMarginLeft() + style.GetMarginRight()
		if inset > maxInset {
			maxInset = inset
		}
	}
	return maxInset
}

func (m *Model) mouseScrollLines() int {
	if m.viewportHeight <= 0 {
		return 1
	}
	lines := m.viewportHeight / 4
	if lines < 1 {
		lines = 1
	}
	return lines
}

func renderMarkdownForWidth(content string, width int) string {
	renderer, err := NewMarkdownRenderer(width)
	if err != nil {
		return content
	}
	renderer.AppendToken(content)
	return renderer.GetFinalContent()
}

// stripAllANSI removes ALL ANSI escape sequences from a string, including SGR mouse codes
func stripAllANSI(s string) string {
	if s == "" {
		return ""
	}

	// Remove mouse/Sgr sequences first in case they contain characters not covered by the
	// generic CSI matcher (e.g. "[<...")
	cleaned := StripUnwantedANSI(s)
	cleaned = csiSequenceRegex.ReplaceAllString(cleaned, "")
	cleaned = oscSequenceRegex.ReplaceAllString(cleaned, "")
	cleaned = singleEscapeRegex.ReplaceAllString(cleaned, "")
	return cleaned
}

func splitLinesPreserveHeight(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m *Model) maxViewportOffset() int {
	if m.viewportHeight <= 0 {
		return 0
	}
	max := len(m.viewportLines) - m.viewportHeight
	if max < 0 {
		return 0
	}
	return max
}

func (m *Model) clampViewportOffset() {
	maxOffset := m.maxViewportOffset()
	if m.viewportYOffset > maxOffset {
		m.viewportYOffset = maxOffset
	}
	if m.viewportYOffset < 0 {
		m.viewportYOffset = 0
	}
}

func (m *Model) viewportAtBottom() bool {
	m.clampViewportOffset()
	return m.viewportYOffset >= m.maxViewportOffset()
}

func (m *Model) viewportGotoBottom() {
	m.viewportYOffset = m.maxViewportOffset()
	m.scrolledUp = false
}

func (m *Model) handleScrollKey(key string) bool {
	switch key {
	case "up", "down", "pgup", "pgdown", "home", "end":
		// continue
	default:
		return false
	}

	if m.viewportHeight <= 0 || len(m.viewportLines) <= m.viewportHeight {
		return false
	}

	originalOffset := m.viewportYOffset
	switch key {
	case "up":
		m.viewportYOffset--
	case "down":
		m.viewportYOffset++
	case "pgup":
		m.viewportYOffset -= m.viewportHeight
	case "pgdown":
		m.viewportYOffset += m.viewportHeight
	case "home":
		m.viewportYOffset = 0
	case "end":
		m.viewportGotoBottom()
		return true
	}

	m.clampViewportOffset()
	if m.viewportYOffset != originalOffset {
		m.scrolledUp = !m.viewportAtBottom()
		if !m.scrolledUp {
			m.viewportGotoBottom()
		}
		return true
	}
	return false
}

func (m *Model) handleCommandMenuKey(key string) (tea.Cmd, bool) {
	if !m.commandMenuOn || m.commandPalette == nil {
		return nil, false
	}

	// Only use j/k for navigation if user isn't typing a filter
	// (i.e., input is just "/" or empty)
	inputValue := strings.TrimSpace(m.input.Value())
	allowNavKeys := inputValue == "/" || inputValue == ""

	switch key {
	case "up", "k":
		if !allowNavKeys {
			return nil, false
		}
		m.commandPalette.MoveSelection(-1)
		return nil, true
	case "down", "j":
		if !allowNavKeys {
			return nil, false
		}
		m.commandPalette.MoveSelection(1)
		return nil, true
	case "/":
		m.hideCommandMenu()
		m.input.SetValue("")
		return nil, true
	case "esc", "ctrl+c":
		m.hideCommandMenu()
		m.input.SetValue("")
		return nil, true
	case "enter":
		current := strings.TrimSpace(m.input.Value())
		if m.commandMenuMode != menuModeCommands && current != "" && !strings.HasPrefix(current, "/") {
			m.hideCommandMenu()
			return nil, false
		}
		if selected := m.commandPalette.SelectedCommand(); selected != nil {
			return m.handleCommandSelection(*selected), true
		}
	}
	return nil, false
}

func (m *Model) handleCommandSelection(cmd cmds.Command) tea.Cmd {
	switch m.commandMenuMode {
	case menuModeCommands:
		m.input.SetValue("")
		if handler, ok := m.builtinCommandHandler(cmd.Name); ok {
			m.hideCommandMenu()
			return handler()
		}
	case menuModeAgentGroups:
		m.showAgentMenuForGroup(cmd.Name)
		m.input.SetValue("")
		return nil
	case menuModeAgents:
		m.activateAgentFromMenu(cmd.Name)
		m.hideCommandMenu()
		m.input.SetValue("")
		return nil
	case menuModeSkills:
		m.applySkillByName(cmd.Name, true)
		m.hideCommandMenu()
		m.input.SetValue("")
		return nil
	case menuModeModels:
		m.input.SetValue("")
		return m.switchModel(cmd.Name)
	case menuModeSessions:
		m.resumeSession(cmd.Name)
		m.input.SetValue("")
		return nil
	}
	if strings.Contains(cmd.Prompt, "$ARGUMENTS") {
		cmdCopy := cmd
		m.pendingCommand = &cmdCopy
		m.input.SetValue("")
		m.input.Placeholder = fmt.Sprintf("Arguments for /%s…", cmd.Name)
		return nil
	}
	return m.startCommand(cmd, "")
}

func (m *Model) startCommand(cmd cmds.Command, args string) tea.Cmd {
	finalPrompt := m.buildCommandPrompt(cmd, args)
	if strings.TrimSpace(finalPrompt) == "" {
		return nil
	}

	display := formatCommandDisplay(cmd.Name, args)
	userMsg := NewUserMessage(finalPrompt)
	userMsg.Display = display
	m.appendMessage(userMsg)
	m.updateViewportContent()
	m.viewportGotoBottom()

	m.lastRequestPromptLen = m.totalContextLen() + len(finalPrompt)
	m.beginThinking()
	m.tokenChan = make(chan llm.StreamToken, 100)
	m.scrolledUp = false
	return m.sendMessage(finalPrompt, cmd.Model, m.tokenChan)
}

func formatCommandDisplay(name, args string) string {
	display := fmt.Sprintf("/%s", name)
	if strings.TrimSpace(args) != "" {
		display += " " + args
	}
	return display
}

func (m *Model) buildCommandPrompt(cmd cmds.Command, args string) string {
	replacements := []string{
		"$ARGUMENTS", strings.TrimSpace(args),
		"$CURRENT_MODEL", strings.TrimSpace(m.currentModelID),
		"$CURRENT_SESSION", m.currentSessionName(),
		"$CURRENT_AGENT", m.currentAgentName(),
		"$SESSIONS", m.sessionTableRows(),
	}
	replacer := strings.NewReplacer(replacements...)
	return strings.TrimSpace(replacer.Replace(cmd.Prompt))
}

func (m *Model) builtinCommandHandler(name string) (func() tea.Cmd, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "agent":
		return func() tea.Cmd {
			m.showAgentMenu()
			return nil
		}, true
	case "skill":
		return func() tea.Cmd {
			m.showSkillMenu()
			return nil
		}, true
	case "menu":
		return nil, false
	case "search":
		// Manual /search is disabled; ToolRunner handles web search when needed.
		return nil, false
	case "config":
		return func() tea.Cmd {
			return m.handleConfigCommand()
		}, true
	case "principles":
		return func() tea.Cmd {
			return m.handlePrinciplesCommand()
		}, true
	case "help":
		return func() tea.Cmd {
			m.toggleHelpPanel()
			return nil
		}, true
	case "history":
		return func() tea.Cmd {
			m.showSessionMenu()
			return nil
		}, true
	default:
		return nil, false
	}
}

func (m *Model) hideCommandMenu() {
	m.commandMenuOn = false
	m.commandFilter = ""
	m.commandMenuMode = menuModeHidden
	if m.commandPalette != nil {
		m.commandPalette.Hide()
	}
}

func (m *Model) updateCommandPaletteState() {
	if m.commandPalette == nil || m.pendingCommand != nil {
		m.hideCommandMenu()
		m.commandFilter = ""
		return
	}
	if m.helpVisible || m.principlesVisible {
		return
	}
	if m.commandMenuMode == menuModeAgentGroups || m.commandMenuMode == menuModeAgents || m.commandMenuMode == menuModeModels || m.commandMenuMode == menuModeSessions || m.commandMenuMode == menuModeSkills {
		return
	}
	value := m.input.Value()
	if value == "/" {
		if !m.commandMenuOn || m.commandMenuMode != menuModeCommands {
			m.showCommandList("")
		}
		return
	}
	if strings.HasPrefix(value, "/") && !containsWhitespace(value) {
		filter := strings.TrimPrefix(value, "/")
		if m.isBuiltinSlashCommand(filter) {
			m.hideCommandMenu()
			return
		}
		m.showCommandList(filter)
		return
	}
	m.hideCommandMenu()
	m.commandFilter = ""
}

func (m *Model) toolRunnerEnabled() bool {
	disable := strings.ToLower(strings.TrimSpace(os.Getenv("SHINOBI_DISABLE_TOOLRUNNER")))
	if disable == "1" || disable == "true" || disable == "yes" || disable == "on" {
		return false
	}
	enable := strings.ToLower(strings.TrimSpace(os.Getenv("SHINOBI_ENABLE_TOOLRUNNER")))
	if enable == "0" || enable == "false" || enable == "no" || enable == "off" {
		return false
	}
	return true
}

func barePathAutoReadEnabled() bool {
	disable := strings.ToLower(strings.TrimSpace(os.Getenv("SHINOBI_DISABLE_BARE_PATH_AUTOREAD")))
	if disable == "1" || disable == "true" || disable == "yes" || disable == "on" {
		return false
	}
	return true
}

func (m *Model) shouldRunToolRunner(input string) bool {
	q := strings.ToLower(strings.TrimSpace(input))
	if q == "" {
		return false
	}
	if strings.Contains(q, "/") || strings.Contains(q, "~/") {
		return true
	}
	keywords := []string{
		"file", "files", "folder", "folders", "directory", "directories", "dir",
		"read", "open", "show", "list", "ls", "find", "search", "grep", "rg",
		"locate", "path", "create", "write", "save", "edit", "update", "append",
	}
	for _, kw := range keywords {
		if strings.Contains(q, kw) {
			return true
		}
	}

	if m != nil && m.webSearchConfigured() {
		words := strings.Fields(q)
		if isAckLikeMessage(q, len(words)) {
			return false
		}
		return true
	}

	return false
}

func (m *Model) tryExecuteNaturalFSCommand(value string) (tea.Cmd, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, false
	}

	cmd, rest := splitLeadingWord(trimmed)
	if cmd == "" || rest == "" {
		return nil, false
	}

	switch strings.ToLower(cmd) {
	case "read", "open", "show", "cat", "view":
		if target := extractPathArg(rest); target != "" {
			if resolved, info, ok := m.fsPathInfo(target); ok {
				if info.IsDir() {
					return m.fsList(resolved), true
				}
				return m.fsRead(resolved), true
			}
		}
	case "ls", "list", "dir":
		if target := extractPathArg(rest); target != "" {
			if resolved, _, ok := m.fsPathInfo(target); ok {
				return m.fsList(resolved), true
			}
		}
	}

	return nil, false
}

func splitLeadingWord(input string) (string, string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return "", ""
	}
	head := parts[0]
	rest := strings.TrimSpace(strings.TrimPrefix(input, head))
	return head, rest
}

func extractPathArg(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '"' || trimmed[0] == '\'' {
		quote := trimmed[0]
		end := strings.IndexRune(trimmed[1:], rune(quote))
		if end != -1 {
			return trimmed[1 : end+1]
		}
		return strings.Trim(trimmed, "\"'")
	}
	return trimmed
}

func (m *Model) fsPathInfo(path string) (string, os.FileInfo, bool) {
	if m == nil {
		return "", nil, false
	}
	resolved, err := m.resolveFSPath(path)
	if err != nil {
		return "", nil, false
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, false
	}
	return resolved, info, true
}

func containsWhitespace(s string) bool {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			return true
		}
	}
	return false
}

func (m *Model) isBuiltinSlashCommand(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "menu", "model", "agent", "agents", "skill", "history", "new", "rename", "delete", "fs", "inject", "context", "principles":
		return true
	default:
		return false
	}
}

func (m *Model) currentSessionName() string {
	if strings.TrimSpace(m.currentSessionLabel) != "" {
		return m.currentSessionLabel
	}
	return "live-session"
}

func (m *Model) currentProjectName() string {
	return "default"
}

func (m *Model) cycleAgent(delta int) {
	if len(m.agents) == 0 {
		return
	}
	// Find current index
	currentIdx := 0
	if m.activeAgent != nil {
		for i := range m.agents {
			if m.activeAgent == &m.agents[i] {
				currentIdx = i
				break
			}
		}
	}
	next := currentIdx + delta
	if next < 0 {
		next = len(m.agents) - 1
	}
	if next >= len(m.agents) {
		next = 0
	}
	path := m.agents[next].FilePath
	if !m.setActiveAgentByPath(path, false) {
		return
	}
	// Persist as the new default agent without adding a system notice, since
	// the status bar already reflects the change and Tab-cycling should stay
	// lightweight.
	_ = m.persistDefaultAgent(m.agents[next].Name)
}

// backendTag returns a short tag for the given backend, suitable for compact
// UI surfaces like menus and status bars.
func backendUnavailableError(backend config.Backend) string {
	switch backend {
	case config.BackendOllama:
		return "No Ollama backend available. Start a model in another terminal:\n  ollama run <model>"
	case config.BackendLMStudio:
		return "No LM Studio backend available. Load a model in LM Studio before chatting."
	default:
		return fmt.Sprintf("No client available for backend %q. Load a model and try again.", backend)
	}
}

func backendTag(backend config.Backend) string {
	switch backend {
	case config.BackendLMStudio:
		return "[lmstudio]"
	case config.BackendOllama:
		return "[ollama]"
	default:
		return ""
	}
}

// routeDisplayLabel builds a human-friendly label for a model route that
// includes a backend tag when available. This is used consistently across
// status text, headers, and menus so the active backend/model are always
// visible.
func (m *Model) routeDisplayLabel(route config.ModelRoute) string {
	tag := backendTag(route.Backend)
	if tag == "" {
		return route.Label
	}
	return fmt.Sprintf("%s %s", tag, route.Label)
}

// currentModelDisplayLabel returns the display label for the active model
// route, including backend tag.
func (m *Model) currentModelDisplayLabel() string {
	return m.routeDisplayLabel(m.currentModelRoute())
}

// modelDisplayLabelForID returns the display label for a given model ID using
// the routing table. When the ID is unknown, it falls back to the raw ID with
// a generic backend tag.
func (m *Model) modelDisplayLabelForID(id string) string {
	route := m.resolveRouteForModelID(id)
	return m.routeDisplayLabel(route)
}

// modelMenuDescription builds a rich description string for a model choice,
// including backend tag, concrete ID, capabilities, and status flags like
// "Not installed" or "Active".
func (m *Model) modelMenuDescription(id string, active bool) string {
	route := m.resolveRouteForModelID(id)
	parts := []string{}

	if tag := backendTag(route.Backend); tag != "" {
		parts = append(parts, tag)
	}
	if route.ID != "" {
		parts = append(parts, route.ID)
	}
	if len(route.Capabilities) > 0 {
		capTags := make([]string, 0, len(route.Capabilities))
		for _, cap := range route.Capabilities {
			cap = strings.TrimSpace(cap)
			if cap == "" {
				continue
			}
			capTags = append(capTags, fmt.Sprintf("[%s]", cap))
		}
		if len(capTags) > 0 {
			parts = append(parts, strings.Join(capTags, " "))
		}
	}
	// Don't block model selection based on availability check
	// Users should be able to try any configured model
	if active {
		parts = append(parts, "Active")
	}

	return strings.Join(parts, " "+m.styles.Icons.Dot+" ")
}

// currentModelRoute returns the active model route, including backend
// information. If no explicit route is selected, it falls back to the
// current model ID or the first configured default route.
func (m *Model) currentModelRoute() config.ModelRoute {
	if m.currentModelRouteIndex >= 0 && m.currentModelRouteIndex < len(m.modelRoutes) {
		return m.modelRoutes[m.currentModelRouteIndex]
	}
	if strings.TrimSpace(m.currentModelID) != "" {
		return config.ModelRoute{
			Label:   m.currentModelID,
			Backend: m.currentBackend,
			ID:      m.currentModelID,
		}
	}
	if len(m.modelRoutes) > 0 {
		for i, route := range m.modelRoutes {
			if route.Default {
				m.currentModelRouteIndex = i
				return route
			}
		}
		m.currentModelRouteIndex = 0
		return m.modelRoutes[0]
	}
	return config.ModelRoute{
		Label:   "default",
		Backend: m.currentBackend,
		ID:      m.currentModelID,
	}
}

// resolveRouteForModelID returns a route for a specific model identifier,
// using known routes when available and falling back to the current backend.
func (m *Model) resolveRouteForModelID(id string) config.ModelRoute {
	id = strings.TrimSpace(id)
	if id == "" {
		return m.currentModelRoute()
	}
	for _, route := range m.modelRoutes {
		if route.ID == id {
			return route
		}
	}
	return config.ModelRoute{
		Label:   id,
		Backend: m.currentBackend,
		ID:      id,
	}
}

// setCurrentModelFromID updates the persisted current model ID and aligns the
// active route index when the ID matches a known route.
func (m *Model) setCurrentModelFromID(modelID string) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return
	}
	m.currentModelID = modelID
	m.currentModelRouteIndex = -1
	for i, route := range m.modelRoutes {
		if route.ID == modelID {
			m.currentModelRouteIndex = i
			break
		}
	}
}

func (m *Model) currentModelLabel() string {
	for label, id := range m.modelCommandMap {
		if id == m.currentModelID {
			return label
		}
	}
	return ""
}

func (m *Model) currentAgentName() string {
	if m.activeAgent != nil {
		return m.activeAgent.Name
	}
	return "primary"
}

func (m *Model) agentSystemPrompt() string {
	if m.activeAgent != nil && m.activeAgent.Prompt != "" {
		return m.activeAgent.Prompt
	}
	return ""
}

func (m *Model) agentModelOverride() string {
	if m.activeAgent == nil {
		return ""
	}
	override := strings.TrimSpace(m.activeAgent.Model)
	if override == "" {
		return ""
	}
	if len(m.availableModels) == 0 || m.availableModels[override] {
		return override
	}
	if !m.missingModelWarn[override] {
		m.showSystemNotice(fmt.Sprintf("Model %s not available. Using %s instead.", override, m.currentModelID))
		m.missingModelWarn[override] = true
	}
	return ""
}

func (m *Model) tryExecuteSlashCommand(value string) (tea.Cmd, bool) {
	if !strings.HasPrefix(value, "/") {
		return nil, false
	}
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return nil, false
	}
	name := strings.TrimPrefix(parts[0], "/")
	name = strings.ToLower(strings.TrimSpace(name))
	args := strings.TrimSpace(strings.TrimPrefix(value, parts[0]))
	m.input.SetValue("")
	switch name {
	case "menu":
		return nil, false
	case "model":
		return m.switchModelByInput(args), true
	case "agent":
		if args != "" {
			m.activateAgentByInput(args)
			return nil, true
		}
		m.showAgentMenu()
		return nil, true
	case "agents":
		m.showAgentMenu()
		return nil, true
	case "skill":
		if args != "" {
			m.applySkillByName(args, true)
			return nil, true
		}
		m.showSkillMenu()
		return nil, true
	case "inject":
		return m.handleInjectCommand(args), true
	case "context":
		return m.handleContextCommand(args), true
	case "fs":
		return m.handleFilesystemCommand(args), true
	case "new":
		return m.handleNewChatCommand(args), true
	case "config":
		return m.handleConfigCommand(), true
	case "principles":
		return m.handlePrinciplesCommand(), true
	case "help":
		m.hideCommandMenu()
		m.toggleHelpPanel()
		return nil, true
	case "history":
		m.showSessionMenu()
		return nil, true
	case "rename":
		return m.handleRenameCommand(args), true
	case "delete":
		return m.handleDeleteCommand(args), true
	}

	cmdDef, ok := m.commandMap[name]
	if !ok {
		return nil, false
	}
	remaining := args
	if strings.Contains(cmdDef.Prompt, "$ARGUMENTS") && remaining == "" {
		cmdCopy := cmdDef
		m.pendingCommand = &cmdCopy
		m.input.Placeholder = fmt.Sprintf("Arguments for /%s…", cmdDef.Name)
		m.hideCommandMenu()
		return nil, true
	}
	m.hideCommandMenu()
	return m.startCommand(cmdDef, remaining), true
}

func (m *Model) tryExecuteBangCommand(value string) (tea.Cmd, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "!") {
		return nil, false
	}
	cmd := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
	if cmd == "" {
		m.input.SetValue("")
		m.showSystemNotice("Usage: !<command>")
		return nil, true
	}
	m.input.SetValue("")
	return m.runExec(cmd), true
}

func (m *Model) showRootMenu() {
	m.showCommandList("")
}

func (m *Model) showCommandList(filter string) {
	if m.commandPalette == nil {
		return
	}
	m.hideHelpPanel()
	m.hidePrinciplesPanel()
	m.commandMenuOn = true
	m.commandMenuMode = menuModeCommands
	cmds := append([]cmds.Command{}, builtinPaletteCommands...)
	skip := make(map[string]struct{}, len(builtinPaletteCommands))
	for _, builtin := range builtinPaletteCommands {
		skip[strings.ToLower(strings.TrimSpace(builtin.Name))] = struct{}{}
	}
	for _, base := range m.baseCommands {
		name := strings.ToLower(strings.TrimSpace(base.Name))
		if _, exists := skip[name]; exists {
			continue
		}
		cmds = append(cmds, base)
	}
	cmds = filterMenuCommands(cmds, []string{"menu", "model"})
	m.commandPalette.SetCommands(cmds)
	m.commandPalette.Show()
	m.commandPalette.SetFilter(filter)
	m.commandFilter = filter
}

func filterMenuCommands(items []cmds.Command, blocked []string) []cmds.Command {
	if len(blocked) == 0 {
		return items
	}
	block := make(map[string]struct{}, len(blocked))
	for _, name := range blocked {
		block[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	out := make([]cmds.Command, 0, len(items))
	for _, cmd := range items {
		if _, ok := block[strings.ToLower(strings.TrimSpace(cmd.Name))]; ok {
			continue
		}
		out = append(out, cmd)
	}
	return out
}

func (m *Model) showAgentMenu() {
	if m.shouldShowAgentGroups() {
		m.showAgentGroupMenu()
		return
	}
	if m.commandPalette == nil {
		return
	}
	m.hideHelpPanel()
	m.hidePrinciplesPanel()
	m.commandMenuOn = true
	m.commandMenuMode = menuModeAgents
	m.agentMenuCommands = m.buildAgentMenuCommands()
	m.commandPalette.SetCommands(m.agentMenuCommands)
	m.commandPalette.Show()
	m.commandPalette.SetFilter("")
	m.commandFilter = ""
}

func (m *Model) showAgentGroupMenu() {
	if m.commandPalette == nil {
		return
	}
	m.hideHelpPanel()
	m.hidePrinciplesPanel()
	m.commandMenuOn = true
	m.commandMenuMode = menuModeAgentGroups
	m.commandPalette.SetCommands(m.agentGroupMenuCommands)
	m.commandPalette.Show()
	m.commandPalette.SetFilter("")
	m.commandFilter = ""
}

func (m *Model) showAgentMenuForGroup(group string) {
	if group != "" {
		m.currentAgentGroup = group
	}
	m.commandMenuOn = true
	m.commandMenuMode = menuModeAgents
	m.agentMenuCommands = m.buildAgentMenuCommands()
	if m.commandPalette != nil {
		m.commandPalette.SetCommands(m.agentMenuCommands)
		m.commandPalette.Show()
		m.commandPalette.SetFilter("")
	}
	m.commandFilter = ""
}

func (m *Model) showSkillMenu() {
	if m.commandPalette == nil {
		return
	}
	m.hideHelpPanel()
	m.hidePrinciplesPanel()
	m.commandMenuOn = true
	m.commandMenuMode = menuModeSkills
	m.commandPalette.SetCommands(m.skillMenuCommands)
	m.commandPalette.Show()
	m.commandPalette.SetFilter("")
	m.commandFilter = ""
}

func (m *Model) showModelMenu() {
	if m.commandPalette == nil {
		return
	}
	m.hideHelpPanel()
	m.hidePrinciplesPanel()
	m.commandMenuOn = true
	m.commandMenuMode = menuModeModels
	m.commandPalette.SetCommands(m.modelMenuCommands)
	m.commandPalette.Show()
	m.commandPalette.SetFilter("")
	m.commandFilter = ""
}

func (m *Model) showSessionMenu() {
	if m.commandPalette == nil {
		return
	}
	m.hideHelpPanel()
	m.hidePrinciplesPanel()
	m.input.SetValue("")
	m.commandMenuOn = true
	m.commandMenuMode = menuModeSessions
	m.commandPalette.SetCommands(m.sessionMenuItems)
	m.commandPalette.Show()
	m.commandPalette.SetFilter("")
	m.commandFilter = ""
}

func (m *Model) activateAgent(name string) bool {
	if !m.setActiveAgent(name, false) {
		return false
	}
	_ = m.persistDefaultAgent(name)
	return true
}

func (m *Model) activateAgentFromMenu(name string) {
	if m == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key != "" && m.agentMenuIndex != nil {
		if ag, ok := m.agentMenuIndex[key]; ok {
			m.setActiveAgentByPath(ag.FilePath, false)
			_ = m.persistDefaultAgent(ag.Name)
			return
		}
	}
	m.activateAgent(name)
}

func (m *Model) setActiveAgent(name string, announce bool) bool {
	if name == "" {
		return false
	}
	if ag := m.findAgentInGroup(m.currentAgentGroup, name); ag != nil {
		m.activeAgent = ag
		return true
	}
	for i := range m.agents {
		if !strings.EqualFold(m.agents[i].Name, name) {
			continue
		}
		if m.activeAgent == &m.agents[i] {
			return true
		}
		m.activeAgent = &m.agents[i]
		return true
	}
	return false
}

func (m *Model) setActiveAgentByPath(path string, announce bool) bool {
	if m == nil || path == "" {
		return false
	}
	for i := range m.agents {
		if m.agents[i].FilePath == path {
			m.activeAgent = &m.agents[i]
			return true
		}
	}
	return false
}

func formatAgentDisplayName(name string) string {
	if name == "" {
		return "agent"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func buildAgentGroups(agents []agentpkg.Agent) (map[string][]agentpkg.Agent, []string) {
	groups := make(map[string][]agentpkg.Agent)
	for _, ag := range agents {
		group := agentGroupForPath(ag.FilePath)
		if group == "" {
			group = "other"
		}
		groups[group] = append(groups[group], ag)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return agentGroupOrder(names[i]) < agentGroupOrder(names[j])
	})
	return groups, names
}

func selectVisibleAgentGroups(allNames []string, groups map[string][]agentpkg.Agent) []string {
	hasLocal := len(groups["local"]) > 0
	hasCode := len(groups["code"]) > 0
	hasShinobi := len(groups["shinobi"]) > 0
	if hasLocal && hasCode {
		out := []string{"local", "code"}
		if hasShinobi {
			out = append(out, "shinobi")
		}
		return out
	}
	if len(allNames) <= 1 {
		return nil // single group — skip group picker, show agents directly
	}
	return allNames
}

func agentGroupForPath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "embedded:") {
		return "shinobi"
	}
	clean := filepath.Clean(path)
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	localRoot := filepath.Join(home, "memory", "ai", "local") + string(os.PathSeparator)
	codeRoot := filepath.Join(home, "memory", "ai", "code") + string(os.PathSeparator)
	userRoot := filepath.Join(home, ".shinobi", "agents") + string(os.PathSeparator)
	if strings.HasPrefix(clean, localRoot) {
		return "local"
	}
	if strings.HasPrefix(clean, codeRoot) {
		return "code"
	}
	if strings.HasPrefix(clean, userRoot) {
		return "builtin"
	}
	if strings.HasPrefix(clean, filepath.Clean("agents")+string(os.PathSeparator)) {
		return "builtin"
	}
	return "other"
}

func agentGroupOrder(name string) int {
	switch strings.ToLower(name) {
	case "local":
		return 0
	case "code":
		return 1
	case "shinobi":
		return 2
	case "user":
		return 3
	default:
		return 4
	}
}

func agentGroupColor(group string) string {
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "local":
		return colorPrimary
	case "code":
		return colorAssistantRole
	default:
		return ""
	}
}

func defaultAgentGroup(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	for _, group := range groups {
		if group == "local" {
			return group
		}
	}
	for _, group := range groups {
		if group == "shinobi" {
			return group
		}
	}
	return groups[0]
}

func formatSearchResults(query string, results []search.Result) string {
	if len(results) == 0 {
		return fmt.Sprintf("No web results for %q", query)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Web search results for %q:\n", query))
	for i, res := range results {
		b.WriteString(fmt.Sprintf("%d. %s\n%s\n%s\n\n", i+1, res.Title, res.URL, res.Snippet))
	}
	return b.String()
}

func formatExecResult(command string, result toolResult) string {
	var b strings.Builder
	trimmed := strings.TrimSpace(command)
	if trimmed != "" {
		b.WriteString(fmt.Sprintf("Bash exec: %s\n", trimmed))
	} else {
		b.WriteString("Bash exec result:\n")
	}
	for _, line := range result.Lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if strings.TrimSpace(result.Error) != "" {
		b.WriteString("Error: ")
		b.WriteString(strings.TrimSpace(result.Error))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (m *Model) activateAgentByInput(input string) {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		m.showAgentMenu()
		return
	}
	if group, name := splitAgentSelector(input); group != "" {
		if m.showAgentGroupByName(group) {
			if name == "" {
				return
			}
			if ag := m.findAgentInGroup(group, name); ag != nil {
				if !m.setActiveAgentByPath(ag.FilePath, false) {
					m.showSystemNotice(fmt.Sprintf("Agent %s unavailable", ag.Name))
				}
				_ = m.persistDefaultAgent(ag.Name)
				return
			}
		}
	}
	if m.showAgentGroupByName(input) {
		return
	}
	for _, ag := range m.agents {
		if strings.EqualFold(ag.Name, input) {
			if !m.activateAgent(ag.Name) {
				m.showSystemNotice(fmt.Sprintf("Agent %s unavailable", ag.Name))
			}
			return
		}
	}
	m.showSystemNotice(fmt.Sprintf("Unknown agent %s", input))
}

func (m *Model) agentsForCurrentGroup() []agentpkg.Agent {
	if m == nil || m.currentAgentGroup == "" || len(m.agentGroups) == 0 {
		return m.agents
	}
	if agents, ok := m.agentGroups[m.currentAgentGroup]; ok && len(agents) > 0 {
		return agents
	}
	return m.agents
}

func (m *Model) shouldShowAgentGroups() bool {
	return len(m.agentGroupNames) > 1
}

func (m *Model) showAgentGroupByName(name string) bool {
	if name == "" {
		return false
	}
	for _, group := range m.agentGroupNames {
		if strings.EqualFold(group, name) {
			m.showAgentMenuForGroup(group)
			return true
		}
	}
	return false
}

func (m *Model) findAgentInGroup(group, name string) *agentpkg.Agent {
	agents, ok := m.agentGroups[group]
	if !ok {
		return nil
	}
	for i := range agents {
		if strings.EqualFold(agents[i].Name, name) {
			return &agents[i]
		}
	}
	return nil
}

func splitAgentSelector(input string) (string, string) {
	separators := []string{"/", ":"}
	for _, sep := range separators {
		if parts := strings.SplitN(input, sep, 2); len(parts) == 2 {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
	}
	return "", ""
}

func (m *Model) resolveAgentSelection(selector string) *agentpkg.Agent {
	if m == nil {
		return nil
	}
	group, name := splitAgentSelector(selector)
	if group != "" {
		if ag := m.findAgentInGroup(group, name); ag != nil {
			return ag
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(selector)
	}
	if name == "" {
		return nil
	}
	if ag := m.findAgentInGroup(m.currentAgentGroup, name); ag != nil {
		return ag
	}
	var match *agentpkg.Agent
	for i := range m.agents {
		if strings.EqualFold(m.agents[i].Name, name) {
			if match != nil {
				return nil
			}
			match = &m.agents[i]
		}
	}
	return match
}

func (m *Model) agentNameCounts() map[string]int {
	counts := make(map[string]int)
	for _, ag := range m.agents {
		key := strings.ToLower(strings.TrimSpace(ag.Name))
		if key == "" {
			continue
		}
		counts[key]++
	}
	return counts
}

func (m *Model) applySkillByName(input string, announce bool) {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		m.showSkillMenu()
		return
	}
	for _, sk := range m.skills {
		if strings.EqualFold(sk.Name, input) {
			m.enableSkill(sk, announce)
			return
		}
	}
	m.showSystemNotice(fmt.Sprintf("Unknown skill %s", input))
}

func (m *Model) enableSkill(skill skillpkg.Skill, announce bool) {
	if m.activeSkills == nil {
		m.activeSkills = make(map[string]skillpkg.Skill)
	}
	key := strings.ToLower(strings.TrimSpace(skill.Name))
	if key == "" {
		return
	}
	if _, exists := m.activeSkills[key]; exists {
		return
	}
	m.activeSkills[key] = skill
	m.activeSkillOrder = append(m.activeSkillOrder, key)
	if announce {
		m.showSkillLoadDebug(skill)
	}
}

func (m *Model) showSkillLoadDebug(skill skillpkg.Skill) {
	if m == nil || skill.FilePath == "" {
		return
	}
	cmd := fmt.Sprintf("wc -c %s", shellQuote(skill.FilePath))
	result := m.runToolBashExec(cmd)
	indicator := formatToolIndicator(result)
	if indicator == "" {
		indicator = fmt.Sprintf("⏺ Bash(%s)", cmd)
	}
	m.appendMessage(NewUIOnlySystemMessage(indicator))
	m.updateViewportContent()
	m.viewportGotoBottom()
}

func shellQuote(input string) string {
	if input == "" {
		return "''"
	}
	if !strings.ContainsAny(input, " \t\n'\"\\$`") {
		return input
	}
	// POSIX-safe single-quote escaping.
	return "'" + strings.ReplaceAll(input, "'", `'\''`) + "'"
}

func (m *Model) switchModel(name string) tea.Cmd {
	id, ok := m.modelCommandMap[name]
	if !ok {
		m.hideCommandMenu()
		return nil
	}
	if id == m.currentModelID {
		m.hideCommandMenu()
		return nil
	}
	m.setCurrentModelFromID(id)
	m.hideCommandMenu()
	m.showSystemNotice(fmt.Sprintf("Switching to %s…", name))
	m.modelWarmStatus = fmt.Sprintf("Using %s", name)
	_ = m.persistDefaultModel(id)
	return nil
}

func (m *Model) switchModelByInput(input string) tea.Cmd {
	input = strings.TrimSpace(input)
	if input == "" {
		m.showModelMenu()
		return nil
	}
	for label := range m.modelCommandMap {
		if strings.EqualFold(label, input) {
			return m.switchModel(label)
		}
	}
	for label, id := range m.modelCommandMap {
		if strings.EqualFold(id, input) {
			return m.switchModel(label)
		}
	}
	m.showSystemNotice(fmt.Sprintf("Unknown model %s", input))
	return nil
}

func (m *Model) resumeSession(name string) {
	m.hideCommandMenu()
	summary, ok := m.sessionMap[name]
	if !ok {
		m.showSystemNotice(fmt.Sprintf("Session %s not found", name))
		return
	}
	m.loadSessionSummary(summary)
}

func (m *Model) resumeSessionByName(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		m.showSessionMenu()
		return
	}
	for key := range m.sessionMap {
		if strings.EqualFold(key, input) {
			m.resumeSession(key)
			return
		}
	}
	m.showSystemNotice(fmt.Sprintf("Session %s not found", input))
}

func (m *Model) showSystemNotice(message string) {
	m.appendMessage(NewSystemMessage(message))
	m.updateViewportContent()
	m.viewportGotoBottom()
}

func (m *Model) handleToolsCommand() {
	msg := m.toolsContextMessage()
	m.appendMessage(NewSystemMessage(msg))
	m.updateViewportContent()
	m.viewportGotoBottom()
}

func (m *Model) beginToolApprovalPrompt(note, target, content string) {
	if m == nil {
		return
	}
	tgt := strings.TrimSpace(target)
	hasContent := strings.TrimSpace(content) != ""
	m.pendingToolApproval = &toolApprovalRequest{
		kind:       "fs_write",
		target:     tgt,
		note:       strings.TrimSpace(note),
		content:    content,
		hasContent: hasContent,
	}
	if tgt != "" {
		m.input.Placeholder = fmt.Sprintf("Approve write to %s? (y/n)", tgt)
		if hasContent {
			m.showSystemNotice(fmt.Sprintf("Approval required for %s (%d bytes staged). Type y to approve or n to cancel.", tgt, len(content)))
		} else {
			m.showSystemNotice(fmt.Sprintf("Approval required for %s. Type y to approve or n to cancel.", tgt))
		}
		return
	}
	m.input.Placeholder = "Approve file write? (y/n)"
	m.showSystemNotice("Approval required for file write. Type y to approve or n to cancel.")
}

func (m *Model) clearPendingToolApproval() {
	if m == nil {
		return
	}
	m.pendingToolApproval = nil
	m.input.Placeholder = m.defaultPrompt
}

func (m *Model) consumePendingToolApproval(value string) (tea.Cmd, bool) {
	if m == nil || m.pendingToolApproval == nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, true
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "!") {
		return nil, false
	}
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case "y", "yes", "approve", "ok", "confirm":
		req := m.pendingToolApproval
		m.clearPendingToolApproval()
		m.toolRunnerPendingApproval = ""
		m.toolRunnerPendingWritePath = ""
		m.clearToolRunnerContext()
		if req.target == "" || !req.hasContent {
			if req.target != "" {
				m.showSystemNotice(fmt.Sprintf("Approved for %s, but no staged content was captured. Use /fs write %s, paste content, then /fs apply.", req.target, req.target))
				return nil, true
			}
			m.showSystemNotice("Approved. Provide a path with /fs write <path>, then paste content and run /fs apply.")
			return nil, true
		}
		abs, err := m.resolveFSPath(req.target)
		if err != nil {
			m.showSystemNotice(fmt.Sprintf("Approved write target is invalid: %v", err))
			return nil, true
		}
		m.pendingFSWrite = &fsWriteRequest{
			Path:    req.target,
			AbsPath: abs,
			Content: req.content,
			Ready:   true,
		}
		return m.fsApplyWrite(), true
	case "n", "no", "deny", "cancel":
		m.clearPendingToolApproval()
		m.toolRunnerPendingApproval = ""
		m.toolRunnerPendingWritePath = ""
		m.clearToolRunnerContext()
		m.showSystemNotice("Write request canceled.")
		return nil, true
	default:
		m.showSystemNotice("Pending write approval: type y to approve or n to cancel.")
		return nil, true
	}
}

func (m *Model) injectToolsContext() {
	msg := strings.TrimSpace(m.toolsContextMessage())
	if msg == "" {
		return
	}
	m.appendMessage(NewSystemMessage(msg))
}

func (m *Model) applyStoredSettings() {
	if m == nil || m.store == nil {
		return
	}
	settings, err := m.store.LoadSettings(storage.SettingScopeUser, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to load settings: %v\n", err)
		return
	}
	if raw, ok := settings[storage.SettingKeyShowTimestamps]; ok {
		m.showTimestamps = storage.ParseBoolSetting(raw, m.showTimestamps)
	}
	if raw, ok := settings[storage.SettingKeyDefaultAgent]; ok {
		m.setActiveAgent(raw, false)
	} else {
		// No stored preference — default to the "shinobi" agent for first-time users.
		// Falls back to agents[0] (already set) if shinobi isn't available.
		m.setActiveAgent("shinobi", false)
	}
}

func (m *Model) applyStoredModelPreference(modelID string) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return
	}
	// Allow any configured model to be set, regardless of availability check
	m.setCurrentModelFromID(modelID)
}

func (m *Model) persistAppearanceSetting(key string, value bool) error {
	return m.persistUserSetting(key, storage.BoolSettingValue(value))
}

func (m *Model) persistDefaultModel(id string) error {
	return m.persistUserSetting(storage.SettingKeyDefaultModel, strings.TrimSpace(id))
}

func (m *Model) persistDefaultAgent(name string) error {
	return m.persistUserSetting(storage.SettingKeyDefaultAgent, strings.TrimSpace(name))
}

func (m *Model) persistUserSetting(key, value string) error {
	if m == nil || m.store == nil {
		return nil
	}
	if err := m.store.SaveSetting(storage.SettingScopeUser, 0, key, value); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to save setting %s: %v\n", key, err)
		return err
	}
	return nil
}

func (m *Model) toggleHelpPanel() {
	if m.helpVisible {
		m.hideHelpPanel()
	} else {
		m.showHelpPanel()
	}
}

func (m *Model) togglePrinciplesPanel() {
	if m.principlesVisible {
		m.hidePrinciplesPanel()
	} else {
		m.showPrinciplesPanel()
	}
}

func (m *Model) hideHelpPanel() {
	if !m.helpVisible {
		return
	}
	m.helpVisible = false
	m.updateViewportContent()
}

func (m *Model) showHelpPanel() {
	if m.helpVisible {
		return
	}
	m.helpVisible = true
	m.hideCommandMenu()
	m.input.SetValue("")
	m.updateViewportContent()
}

func (m *Model) hidePrinciplesPanel() {
	if !m.principlesVisible {
		return
	}
	m.principlesVisible = false
	m.updateViewportContent()
}

func (m *Model) showPrinciplesPanel() {
	if m.principlesVisible {
		return
	}
	m.principlesVisible = true
	m.hideCommandMenu()
	m.input.SetValue("")
	m.updateViewportContent()
}

func (m *Model) buildAgentMenuCommands() []cmds.Command {
	agents := m.agentsForCurrentGroup()
	items := make([]cmds.Command, 0, len(agents))
	if m.agentMenuIndex != nil {
		for k := range m.agentMenuIndex {
			delete(m.agentMenuIndex, k)
		}
	}
	for _, ag := range agents {
		color := strings.TrimSpace(ag.Color)
		if groupColor := agentGroupColor(agentGroupForPath(ag.FilePath)); groupColor != "" {
			color = groupColor
		}
		if color == "" {
			color = "#cdd6f4"
		}
		items = append(items, cmds.Command{
			Name:        ag.Name,
			Description: ag.Description,
			Color:       color,
		})
		if m.agentMenuIndex != nil {
			m.agentMenuIndex[strings.ToLower(ag.Name)] = ag
		}
	}
	return items
}

func buildAgentGroupMenuCommands(groupNames []string, groups map[string][]agentpkg.Agent) []cmds.Command {
	items := make([]cmds.Command, 0, len(groupNames))
	for _, name := range groupNames {
		agents := groups[name]
		desc := fmt.Sprintf("%d agent(s)", len(agents))
		color := agentGroupColor(name)
		if color == "" {
			color = "#cdd6f4"
		}
		items = append(items, cmds.Command{
			Name:        name,
			Description: desc,
			Color:       color,
		})
	}
	return items
}

func (m *Model) buildSkillMenuCommands() []cmds.Command {
	items := make([]cmds.Command, 0, len(m.skills))
	for _, sk := range m.skills {
		desc := strings.TrimSpace(sk.Description)
		if desc != "" {
			desc = truncateMenuDescription(desc, 80)
		}
		items = append(items, cmds.Command{
			Name:            sk.Name,
			Description:     desc,
			HideDescription: true,
			Color:           "#f5c2e7",
		})
	}
	return items
}

func truncateMenuDescription(input string, limit int) string {
	if limit <= 0 || len(input) <= limit {
		return input
	}
	if limit < 3 {
		return input[:limit]
	}
	return input[:limit-3] + "..."
}

func (m *Model) buildModelMenuCommands() []cmds.Command {
	items := make([]cmds.Command, 0, len(m.modelOptions))
	for _, opt := range m.modelOptions {
		desc := m.modelMenuDescription(opt.ID, opt.ID == m.currentModelID)
		items = append(items, cmds.Command{
			Name:        opt.Label,
			Description: desc,
			Color:       "#cba6f7",
		})
	}
	return items
}

func (m *Model) enableAutoSkills() {
	if len(m.skills) == 0 || len(m.autoLoadSkills) == 0 {
		return
	}
	for _, name := range m.autoLoadSkills {
		sk, ok := m.skillMap[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "Warning: auto_load_skill %q not found\n", name)
			continue
		}
		m.enableSkill(sk, false)
	}
}

func (m *Model) buildSessionMenuCommands() []cmds.Command {
	items := make([]cmds.Command, 0, len(m.sessions))
	for i, s := range m.sessions {
		items = append(items, cmds.Command{
			Name:        s.Name,
			Description: s.Description,
			Color:       "#cdd6f4",
			Priority:    i + 1, // preserve DB order (newest first)
		})
	}
	return items
}

// reflowAssistantMessages re-renders cached markdown for assistant messages at the current width
func (m *Model) reflowAssistantMessages() {
	for i := range m.messages {
		if m.messages[i].Role != "assistant" {
			continue
		}

		if m.messages[i].Content == "" {
			continue
		}

		cache := renderMarkdownForWidth(m.messages[i].Content, m.width)
		m.messages[i].RenderedCache = cache
	}
}

// updateViewportContent renders all messages and updates the viewport
func (m *Model) updateViewportContent() {
	var content strings.Builder

	lastUserIndex := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "user" {
			lastUserIndex = i
			break
		}
	}

	for i, msg := range m.messages {
		if i > 0 {
			content.WriteString("\n\n") // Spacing between messages
		}
		content.WriteString(m.renderMessage(msg, i == len(m.messages)-1, i == lastUserIndex))
	}

	// Add thinking indicator while waiting for first token or while streaming
	// hasn't produced visible content yet.
	if m.isThinking() && (!m.isStreaming || m.streamBuffer.Len() == 0) {
		thinkingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorPrimary)).
			Italic(true)
		frames := []string{"thinking.  ", "thinking.. ", "thinking..."}
		frame := int(time.Now().UnixMilli()/400) % len(frames)
		content.WriteString("\n")
		content.WriteString(thinkingStyle.Render(frames[frame]))
	}

	// Add streaming content if currently streaming
	if m.isStreaming && m.streamBuffer.Len() > 0 {
		content.WriteString("\n\n")

		// Render the streaming message header with the active agent name
		// (to match completed messages) and optional timestamp.
		agentLabel := strings.ToLower(formatAgentDisplayName(m.currentAgentName()))
		header := agentLabel + ":"
		if m.showTimestamps {
			header = agentLabel + ": " + m.styles.Timestamp.Render(time.Now().Format("15:04"))
		}
		content.WriteString(m.styles.AssistantMessage.Render(header))
		content.WriteString("\n")

		// Get current plain text content (no markdown rendering during streaming for performance)
		// Markdown will render once when stream completes
		streamContent := m.markdownRenderer.GetCurrentPlainText()

		// Wrap text to the available content width so terminal wrapping stays in sync
		// Note: streamContent should already be cleaned by AppendToken()
		wrappedContent := wrapText(streamContent, m.contentAreaWidth())

		// Format the content with message box borders
		lines := strings.Split(wrappedContent, "\n")
		for i, line := range lines {
			if i == 0 {
				content.WriteString(m.styles.AssistantMessage.Render(m.styles.Icons.BranchStart + " " + line))
			} else {
				content.WriteString(m.styles.AssistantMessage.Render("\n" + m.styles.Icons.BranchCont + line))
			}
		}

		// Add animated streaming indicator
		streamIndicator := m.getStreamingIndicator()
		content.WriteString(m.styles.AssistantMessage.Render(streamIndicator))
	}

	finalContent := content.String()

	// Strip ALL ANSI codes for plain text rendering
	// This ensures scrolling works correctly without markdown styling
	cleanedContent := stripAllANSI(finalContent)

	m.viewportStyledLines = splitLinesPreserveHeight(finalContent)
	m.viewportLines = splitLinesPreserveHeight(cleanedContent)
	m.clampViewportOffset()

	// DEBUG: Log content stats to help diagnose viewport truncation
	if os.Getenv("OLLAMA_TUI_DEBUG") != "" {
		contentLines := strings.Count(finalContent, "\n")
		contentBytes := len(finalContent)
		fmt.Fprintf(os.Stderr, "[DEBUG] Viewport: %d msgs, %d lines, %d bytes | Streaming: %v | Messages: ",
			len(m.messages), contentLines, contentBytes, m.isStreaming)
		for i, msg := range m.messages {
			fmt.Fprintf(os.Stderr, "[%d:%s] ", i, msg.Role)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}

	if debugANSILoggingEnabled() {
		logANSI("viewport", []string{finalContent})
	}
	// Content intentionally keeps ANSI styling since we manage scroll math ourselves
}

// renderMessage renders a single message
func (m *Model) renderMessage(msg Message, isLast bool, isLastUser bool) string {
	if msg.Role == "system" && isUIOnlySystemMessage(msg.Content) {
		// Render tool indicators without a bordered box or extra padding.
		content := msg.VisibleContent()
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted))
		return style.Render(content)
	}
	var boxStyle lipgloss.Style
	var roleName string

	// Select box style and header based on role
	switch msg.Role {
	case "user":
		boxStyle = m.styles.UserMessageBox
		roleName = "you"
	case "assistant":
		boxStyle = m.styles.AssistantMessageBox
		roleName = strings.ToLower(formatAgentDisplayName(m.currentAgentName()))
	case "system":
		boxStyle = m.styles.SystemMessageBox
		roleName = "system"
	case "error":
		boxStyle = m.styles.ErrorMessageBox
		roleName = "error"
	default:
		boxStyle = m.styles.SystemMessageBox
		roleName = strings.ToLower(msg.Role)
	}

	// Build compact header: omit entirely for user messages
	header := roleName + ":"
	if msg.Role == "user" {
		header = ""
	} else if m.showTimestamps {
		header = roleName + ": " + m.styles.Timestamp.Render(msg.Timestamp.Format("15:04"))
	}

	// Wrap content
	var content string
	if msg.Role == "assistant" && msg.RenderedCache != "" {
		// Use rendered markdown (Glamour already handles wrapping)
		content = msg.RenderedCache
	} else if msg.Role == "system" && !m.showContext && isPinnedContextMessage(msg) {
		return ""
	} else {
		// For non-rendered messages, wrap as before
		content = wrapText(msg.VisibleContent(), m.contentAreaWidth()-4) // -4 for box padding
	}

	// Build message with header + thinking (if enabled) + content
	var messageContent string
	if m.showThinking && msg.Thinking != "" {
		// Render thinking in muted italic style
		thinkingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Italic(true)
		wrappedThinking := wrapText(msg.Thinking, m.contentAreaWidth()-4)
		thinking := thinkingStyle.Render(wrappedThinking)
		messageContent = header + "\n" + thinking + "\n---\n" + content
	} else {
		trimmed := strings.Trim(content, "\n")
		if header == "" {
			messageContent = trimmed
		} else {
			styledHeader := lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)).Render(" " + header)
			messageContent = styledHeader + "\n" + trimmed
		}
	}

	// Render in colored box
	return boxStyle.Width(m.contentAreaWidth()).Render(messageContent)
}

// reRenderCachedMessages invalidates and re-renders markdown for all assistant messages
// Called when terminal width changes to ensure proper word wrapping
func (m *Model) reRenderCachedMessages() {
	for i := range m.messages {
		msg := &m.messages[i]

		// Only re-render assistant messages with content
		if msg.Role != "assistant" || msg.Content == "" {
			continue
		}

		// Re-render using the updated width
		renderer, err := NewMarkdownRenderer(m.width)
		if err != nil {
			// On error, clear cache to fall back to plain text
			msg.RenderedCache = ""
			continue
		}

		renderer.AppendToken(msg.Content)
		rendered := renderer.GetFinalContent()
		msg.RenderedCache = rendered
	}
}

func (m *Model) setViewportHeight(height int) {
	if height < 1 {
		height = 1
	}
	if m.viewportHeight == height {
		return
	}
	m.viewportHeight = height
	m.onViewportHeightChanged()
}

func (m *Model) viewportView() string {
	if m.viewportHeight <= 0 {
		return ""
	}

	// Empty state — no user/assistant messages yet
	hasChatMessages := false
	for i := range m.messages {
		role := m.messages[i].Role
		if role == "user" || role == "assistant" {
			hasChatMessages = true
			break
		}
	}
	if !hasChatMessages {
		phraseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted))
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colorPrimary)).Bold(true)
		lines := []string{
			"      " + phraseStyle.Render("your files"),
			"      " + phraseStyle.Render("your brain"),
			"      " + phraseStyle.Render("your inference"),
			"",
			"      " + nameStyle.Render("shinobi"),
		}
		contentHeight := len(lines)
		padTop := (m.viewportHeight - contentHeight) / 2
		if padTop < 0 {
			padTop = 0
		}
		padBottom := m.viewportHeight - contentHeight - padTop
		if padBottom < 0 {
			padBottom = 0
		}
		rows := make([]string, 0, m.viewportHeight)
		for i := 0; i < padTop; i++ {
			rows = append(rows, "")
		}
		rows = append(rows, lines...)
		for i := 0; i < padBottom; i++ {
			rows = append(rows, "")
		}
		return strings.Join(rows, "\n")
	}

	plainLines := m.viewportLines
	if len(plainLines) == 0 {
		plainLines = []string{""}
	}
	styledLines := m.viewportStyledLines
	if len(styledLines) == 0 {
		styledLines = plainLines
	}
	m.clampViewportOffset()
	viewLines := make([]string, m.viewportHeight)
	for row := 0; row < m.viewportHeight; row++ {
		idx := m.viewportYOffset + row
		if idx >= 0 && idx < len(styledLines) {
			viewLines[row] = styledLines[idx]
		} else if idx >= 0 && idx < len(plainLines) {
			viewLines[row] = plainLines[idx]
		} else {
			viewLines[row] = ""
		}
	}
	return strings.Join(viewLines, "\n")
}

func (m *Model) onViewportHeightChanged() {
	m.clampViewportOffset()
	if !m.scrolledUp {
		m.viewportGotoBottom()
	}
}

// View renders the UI
func (m *Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	if m.setupOverlay != nil {
		m.setupOverlay.width = m.width
		m.setupOverlay.height = m.height
		return m.setupOverlay.View()
	}

	helpView := ""
	helpLines := 0
	if m.helpVisible {
		helpView = m.renderHelpPanel()
		helpLines = lipgloss.Height(helpView)
	}
	principlesView := ""
	principlesLines := 0
	if m.principlesVisible {
		principlesView = m.renderPrinciplesPanel()
		principlesLines = lipgloss.Height(principlesView)
	}
	paletteView := ""
	paletteHeight := 0
	if m.commandPalette != nil && m.commandMenuOn {
		paletteView = m.renderCommandPaletteInline()
		paletteHeight = lipgloss.Height(paletteView)
	}
	inputView := m.renderInputSection()
	reserved := lipgloss.Height(inputView) + helpLines + principlesLines + paletteHeight
	viewportHeight := m.height - reserved
	if viewportHeight < 3 {
		viewportHeight = 3
	}
	m.setViewportHeight(viewportHeight)

	sections := []string{m.viewportView()}
	if paletteView != "" {
		sections = append(sections, paletteView)
	}
	sections = append(sections, inputView)
	if principlesView != "" {
		sections = append(sections, principlesView)
	}
	if helpView != "" {
		sections = append(sections, helpView)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderInputSection renders the input area at the bottom
func (m *Model) renderInputSection() string {
	input := m.input.View()

	inputBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(colorMuted)).
		Width(m.width-2).
		Padding(0, 1)

	hints := []string{"Enter send", "/ commands"}
	if name := m.currentAgentName(); name != "" {
		hints = append(hints, "agent: "+strings.ToLower(name))
	}
	hintLine := m.styles.InputHint.Render(strings.Join(hints, "  "+m.styles.Icons.Dot+"  "))

	return inputBox.Render(input) + "\n" + hintLine
}

// renderStatusBar renders the status bar at the bottom
func (m *Model) renderStatusBar() string {
	if !m.showStatusBar {
		return ""
	}

	leftItems := []string{}

	// Agent badge - color based on agent group
	if m.activeAgent != nil {
		badgeColor := colorMauve
		if groupColor := agentGroupColor(agentGroupForPath(m.activeAgent.FilePath)); groupColor != "" {
			badgeColor = groupColor
		}
		badge := lipgloss.NewStyle().
			Background(lipgloss.Color(badgeColor)).
			Foreground(lipgloss.Color(colorBackground)).
			Bold(true).
			Padding(0, 1).
			Render(m.activeAgent.Name)
		leftItems = append(leftItems, badge)
		leftItems = append(leftItems, m.styles.StatusBarDivider.Render(m.styles.Icons.Bar))
	}

	// Context - green accent when loaded
	if m.loadedContextFile != "" {
		leftItems = append(leftItems, m.styles.StatusBarDivider.Render(m.styles.Icons.Bar))
		ctxStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorGreen)).
			Render(m.styles.Icons.Success + " " + m.loadedContextFile)
		leftItems = append(leftItems, ctxStyle)
	}

	// Skills count
	if len(m.activeSkillOrder) > 0 {
		leftItems = append(leftItems, m.styles.StatusBarDivider.Render(m.styles.Icons.Bar))
		leftItems = append(leftItems, m.styles.StatusBarMuted.Render(fmt.Sprintf("%d skills", len(m.activeSkillOrder))))
	}

	if limits := m.statusLimitsLabel(); limits != "" {
		leftItems = append(leftItems, m.styles.StatusBarDivider.Render(m.styles.Icons.Bar))
		leftItems = append(leftItems, m.styles.StatusBarMuted.Render(limits))
	}

	// Message count - muted
	leftItems = append(leftItems, m.styles.StatusBarDivider.Render(m.styles.Icons.Bar))
	leftItems = append(leftItems, m.styles.StatusBarMuted.Render(fmt.Sprintf("%d msgs", len(m.messages))))

	if m.modelWarmStatus != "" {
		leftItems = append(leftItems, m.styles.StatusBarDivider.Render(m.styles.Icons.Bar))
		leftItems = append(leftItems, m.styles.StatusBarActive.Render(m.modelWarmStatus))
	}

	left := lipgloss.JoinHorizontal(lipgloss.Left, leftItems...)

	right := ""
	if m.isThinking() {
		right = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMauve)).
			Render(m.spin.View())
	}

	if right == "" {
		return m.styles.StatusBar.Width(m.width).Render(left)
	}

	rightWidth := lipgloss.Width(right)
	if m.width <= rightWidth {
		bar := ansi.Truncate(right, m.width, "")
		return m.styles.StatusBar.Width(m.width).Render(bar)
	}

	availableLeft := m.width - rightWidth - 1
	leftTrunc := left
	if availableLeft < 0 {
		availableLeft = 0
		leftTrunc = ""
	} else if lipgloss.Width(left) > availableLeft {
		leftTrunc = ansi.Truncate(left, availableLeft, "...")
	}

	gap := m.width - lipgloss.Width(leftTrunc) - rightWidth
	if gap < 1 {
		gap = 1
	}
	bar := leftTrunc + strings.Repeat(" ", gap) + right
	return m.styles.StatusBar.Width(m.width).Render(bar)
}

func (m *Model) isThinking() bool {
	return m.isLoading || m.isStreaming
}

func (m *Model) beginThinking() {
	m.isLoading = true
	m.lastThinkingUpdate = time.Now()
}

// totalContextLen returns the total character count of all messages currently
// in the conversation. Used to estimate prefill time before sending a request.
func (m *Model) totalContextLen() int {
	total := 0
	for _, msg := range m.messages {
		total += len(msg.Content)
	}
	return total
}

func (m *Model) getStreamingIndicator() string {
	// Cycle through different indicators to show streaming activity
	// Similar to Claude Code's animated cursor
	indicators := []string{"▊", "▌", "▎", "▏", "▎", "▌"}
	idx := (time.Now().UnixNano() / 100000000) % int64(len(indicators))
	return " " + indicators[idx]
}

func (m *Model) statusLimitsLabel() string {
	history := historyExchangeLimit()
	var historyLabel string
	if history <= 0 {
		historyLabel = "ctx:all"
	} else {
		historyLabel = fmt.Sprintf("ctx:%d", history)
	}

	timeout := m.requestTimeout()
	var timeoutLabel string
	if timeout <= 0 {
		timeoutLabel = "t:off"
	} else {
		timeoutLabel = fmt.Sprintf("t:%ds", int(timeout.Seconds()))
	}

	return historyLabel + " " + timeoutLabel
}

func historyExchangeLimit() int {
	raw := strings.TrimSpace(os.Getenv("SHINOBI_MAX_HISTORY"))
	if raw == "" {
		return defaultMaxHistoryExchanges
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return defaultMaxHistoryExchanges
	}
	return limit
}

func (m *Model) requestTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("SHINOBI_REQUEST_TIMEOUT"))
	if raw == "" {
		return 120 * time.Second
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		return 120 * time.Second
	}
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// thinkingTimeouts returns the pre-first-token and between-tokens watchdog
// timeouts.
//
// preFirstToken scales automatically with the length of the last submitted
// prompt: 30s base + 10s per 1000 characters. This tolerates slow prefill on
// long prompts without needing manual tuning.
//
// betweenTokens stays tight (30s) so real mid-stream stalls are still caught
// quickly regardless of prompt size.
//
// OLLAMA_TUI_THINKING_TIMEOUT_SECONDS overrides both values when set (legacy).
func (m *Model) thinkingTimeouts() (time.Duration, time.Duration) {
	// Adaptive prefill timeout: 30s + 10s per 1000 chars of prompt.
	promptKB := float64(m.lastRequestPromptLen) / 1000.0
	preFirstToken := 30*time.Second + time.Duration(promptKB*10)*time.Second

	betweenTokens := 30 * time.Second

	// Legacy override: OLLAMA_TUI_THINKING_TIMEOUT_SECONDS replaces both.
	if raw := strings.TrimSpace(os.Getenv("OLLAMA_TUI_THINKING_TIMEOUT_SECONDS")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			base := time.Duration(secs) * time.Second
			preFirstToken = base
			betweenTokens = base / 2
			if betweenTokens > 30*time.Second {
				betweenTokens = 30 * time.Second
			}
		}
	}

	return preFirstToken, betweenTokens
}

// currentThinkingTimeout returns the appropriate watchdog timeout based on
// whether we've seen any tokens yet for the current request.
func (m *Model) currentThinkingTimeout() time.Duration {
	pre, between := m.thinkingTimeouts()
	if m.debugStreamTokenCount == 0 {
		return pre
	}
	return between
}

// cancelOngoingRequestWithReason force-resets UI state for an in-flight request,
// cancels the underlying context, and logs a compact debug entry when enabled.
// All paths that abort a request (Esc, Ctrl+C, watchdog) should go through this
// helper to keep behavior consistent across agents and backends.
func (m *Model) cancelOngoingRequestWithReason(reason string) {
	if m == nil {
		return
	}

	if reason == "" {
		reason = "unspecified"
	}

	// Record last cancel metadata so we can distinguish self-cancellation
	// from genuine backend errors in the errMsg handler.
	m.lastCancelReason = reason
	m.lastCancelTime = time.Now()

	if debugStreamLoggingEnabled() {
		logStreamDebug("cancelOngoingRequest reason=%s isLoading=%v isStreaming=%v tokens=%d", reason, m.isLoading, m.isStreaming, m.debugStreamTokenCount)
	}

	if m.cancelStream != nil {
		m.cancelStream()
		m.cancelStream = nil
	}

	// Reset streaming/loading state
	m.isLoading = false
	m.isStreaming = false
	m.streamBuffer.Reset()
	m.streamThinkingBuffer.Reset()
	m.tokenChan = nil
	m.responseModel = ""
	m.err = nil
	m.lastThinkingUpdate = time.Time{}
	m.debugStreamTokenCount = 0

	// Refresh viewport and clear any lingering tool context
	m.updateViewportContent()
	if !m.scrolledUp {
		m.viewportGotoBottom()
	}
	m.clearToolRunnerContext()
}

// cancelOngoingRequest is the legacy entry point used across the UI. It now
// delegates to cancelOngoingRequestWithReason with a generic reason.
func (m *Model) cancelOngoingRequest() {
	m.cancelOngoingRequestWithReason("generic")
}

func (m *Model) renderHelpPanel() string {
	shortcuts := []string{
		"Enter — Send message or confirm menus",
		"/ — Open the command palette",
		"Tab — Cycle active agent",
		"Esc — Close menus, prompts, or streaming",
		"↑/↓ or PgUp/PgDn — Scroll chat history",
		"Ctrl+C — Cancel streaming or quit",
	}
	helpCommands := []struct {
		Syntax      string
		Description string
	}{
		{"/agent", "Switch agent"},
		{"/skill", "Apply a skill"},
		{"/new", "Start a fresh chat"},
		{"/history", "Browse past conversations"},
		{"/principles", "Show design principles"},
		{"/help", "Toggle this panel"},
	}

	var builder strings.Builder
	builder.WriteString(m.styles.Help.Render("Keys"))
	builder.WriteString("\n")
	for _, line := range shortcuts {
		builder.WriteString("  " + m.styles.Icons.Dot + " " + line + "\n")
	}
	builder.WriteString("\n")
	builder.WriteString(m.styles.Help.Render("Commands"))
	builder.WriteString("\n")
	for _, cmd := range helpCommands {
		builder.WriteString(fmt.Sprintf("  %s %s — %s\n", m.styles.Icons.Dot, cmd.Syntax, cmd.Description))
	}
	panel := m.styles.Border.Width(m.width - 4).Render(strings.TrimSuffix(builder.String(), "\n"))
	return panel
}

func (m *Model) renderPrinciplesPanel() string {
	content := `file over app. point shinobi at your existing agent or skill markdown files —
no need to modify your filesystem or make duplicates. configure shinobi to any
folder or obsidian vault without need for mcp.`

	var builder strings.Builder
	builder.WriteString(m.styles.Help.Render("principles"))
	builder.WriteString("\n\n")
	builder.WriteString(content)

	panel := m.styles.Border.Width(m.width - 4).Render(strings.TrimSuffix(builder.String(), "\n"))
	return panel
}

func (m *Model) handleExportCommand(args string) tea.Cmd {
	m.showSystemNotice("Export is no longer built-in; copy text directly from the TUI or use your terminal tools to save transcripts.")
	return nil
}

func (m *Model) handlePrinciplesCommand() tea.Cmd {
	m.hideCommandMenu()
	m.togglePrinciplesPanel()
	return nil
}

func (m *Model) handleConfigCommand() tea.Cmd {
	m.hideCommandMenu()
	m.input.Blur()
	m.setupOverlay = NewSetupModelFromConfigWithBackend(m.lmStudioURL, m.ollamaURL, m.currentBackend, m.agentDirs, m.skillsDir, m.fsRoots, m.braveToken, m.tavilyKey, m.serpapiKey, m.ddgEnabled)
	return textinput.Blink
}

func (m *Model) applyAndSaveConfig(result SetupResult) {
	if result.LMStudioURL != "" {
		m.lmStudioURL = result.LMStudioURL
		m.llmClients[config.BackendLMStudio] = lmstudio.NewClient(m.lmStudioURL, "")
	}
	if result.OllamaURL != "" {
		m.ollamaURL = result.OllamaURL
		m.llmClients[config.BackendOllama] = lmstudio.NewClient(m.ollamaURL, "")
	}
	if result.ActiveBackend != "" {
		m.currentBackend = result.ActiveBackend
		if result.ActiveBackend == config.BackendLMStudio {
			m.backendURL = m.lmStudioURL
		} else {
			m.backendURL = m.ollamaURL
		}
	}
	if len(result.AgentDirs) > 0 {
		m.agentDirs = result.AgentDirs
	}
	if result.SkillsDir != "" {
		m.skillsDir = result.SkillsDir
	}
	if len(result.FSRoots) > 0 {
		m.fsRoots = result.FSRoots
		m.fsRoot = result.FSRoots[0]
	}
	// Apply search provider changes and rebuild the client
	m.braveToken = result.BraveKey
	m.tavilyKey = result.TavilyKey
	m.serpapiKey = result.SerpAPIKey
	m.ddgEnabled = result.DDGEnabled
	m.searchClient = search.NewFromConfig(m.braveToken, m.tavilyKey, m.serpapiKey, m.ddgEnabled)

	// Load existing config so we don't clobber unrelated fields
	// (auto_load_skills, context_paths, default_agent, etc.)
	cfg, _ := config.Load()
	cfg.LMStudioURL = m.lmStudioURL
	cfg.OllamaURL = m.ollamaURL
	cfg.ActiveBackend = string(m.currentBackend)
	cfg.AgentDirs = m.agentDirs
	cfg.SkillsDir = m.skillsDir
	if len(m.fsRoots) > 0 {
		cfg.FilesystemRoot = m.fsRoots[0]
		cfg.FilesystemRoots = m.fsRoots[1:]
	}
	cfg.BraveToken = m.braveToken
	cfg.TavilyAPIKey = m.tavilyKey
	cfg.SerpAPIKey = m.serpapiKey
	cfg.DDGEnabled = m.ddgEnabled

	if err := config.Save(cfg); err != nil {
		m.showSystemNotice(fmt.Sprintf("config save failed: %v", err))
	} else {
		m.showSystemNotice("config saved")
	}
}

func (m *Model) handleNewChatCommand(args string) tea.Cmd {
	// Start a fresh chat session
	if err := m.startNewChatInProject(m.currentProjectID); err != nil {
		m.showSystemNotice(fmt.Sprintf("Unable to start chat: %v", err))
		return nil
	}
	m.showSystemNotice("New chat started")
	return nil
}

func (m *Model) handleRenameCommand(args string) tea.Cmd {
	name := strings.TrimSpace(args)
	if name == "" {
		m.showSystemNotice("Usage: /rename <new name>")
		return nil
	}
	if m.currentSessionID == 0 {
		m.showSystemNotice("No active chat to rename")
		return nil
	}
	if err := m.renameSession(m.currentSessionID, name, true); err != nil {
		m.showSystemNotice(fmt.Sprintf("Rename failed: %v", err))
		return nil
	}
	m.showSystemNotice("Chat renamed")
	return nil
}

// webSearchConfigured reports whether a real web search client is available.
// It returns false when search is disabled or only the placeholder client is set.
func (m *Model) webSearchConfigured() bool {
	if m == nil || m.searchClient == nil {
		return false
	}
	if _, ok := m.searchClient.(search.DisabledClient); ok {
		return false
	}
	return true
}

var (
	// Common question words that strongly indicate an information-seeking
	// query, even when there is no trailing question mark.
	autoSearchQuestionWords = []string{
		"who",
		"what",
		"when",
		"where",
		"why",
		"how",
		"which",
		"is",
		"are",
		"can",
		"could",
		"would",
		"should",
		"does",
		"do",
	}

	// Keywords and phrases that usually indicate world-knowledge or
	// real‑time information (prices, news, weather, etc.).
	autoSearchInfoKeywords = []string{
		"price",
		"price of",
		"current",
		"today",
		"latest",
		"news",
		"weather",
		"rate",
		"live",
		"now",
		"recent",
		"update",
		"exchange rate",
		"market cap",
		"status of",
		"stock",
		"meaning of",
		"definition of",
	}

	// Phrases that should never trigger auto web search when the message
	// is essentially just an acknowledgement/backchannel.
	autoSearchAckPhrases = []string{
		"ok",
		"okay",
		"cool",
		"nice",
		"thanks",
		"thank you",
		"got it",
		"understood",
		"that's interesting",
		"thats interesting",
		"that’s interesting",
		"interesting",
		"lol",
		"haha",
		"yep",
		"yeah",
		"sure",
	}
)

// autoWebSearchEnabled checks the env flag controlling automatic web search.
// When unset, auto search is enabled as long as a real search client exists.
func (m *Model) autoWebSearchEnabled() bool {
	if !m.webSearchConfigured() {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("OLLAMA_TUI_AUTO_WEB_SEARCH")))
	if mode == "0" || mode == "false" || mode == "off" {
		return false
	}
	// Default: on when search is configured.
	return true
}

// shouldAutoWebSearch applies lightweight heuristics to decide whether the
// current user message should trigger an automatic web search before hitting
// the model. This favors short, factual, world-knowledge questions and avoids
// obvious code/local-only prompts.
func shouldAutoWebSearch(query, _ string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}

	// Avoid auto search for clearly code-centric or local file/path prompts.
	if isCodeOrPathQuery(q) {
		return false
	}

	words := strings.Fields(q)
	wordCount := len(words)
	if wordCount == 0 {
		return false
	}

	// Strong acknowledgement/backchannel filter: very short messages that
	// are either explicit acks ("ok", "thanks") or contain no clear
	// information-seeking signal should NOT trigger web search.
	if isAckLikeMessage(q, wordCount) {
		return false
	}

	// Re-check after filters in case the message became effectively empty.
	if q == "" {
		return false
	}

	hasQuestionMark := strings.Contains(q, "?")
	hasInfoKeyword := containsInfoKeyword(q)
	startsWithQWord := startsWithQuestionWord(q)

	// Messages with an explicit question mark are strong candidates for
	// web search (subject to the code/path guard above).
	if hasQuestionMark {
		return true
	}

	// Messages that start with a question word ("who", "what", etc.) are
	// also strong signals of information-seeking intent, even without "?".
	if startsWithQWord {
		return true
	}

	// Very long prompts are more likely general chat or task instructions.
	// Only auto-search if they explicitly contain an information keyword.
	if wordCount > 40 {
		return hasInfoKeyword
	}

	// For short messages (2–8 words) without a question mark, only trigger
	// auto-search when there is at least one clear information keyword.
	if wordCount >= 2 && wordCount <= 8 && !hasQuestionMark {
		return hasInfoKeyword
	}

	// For all other cases, require an explicit information keyword to avoid
	// firing on generic conversational turns.
	if hasInfoKeyword {
		return true
	}

	return false
}

// isCodeOrPathQuery returns true when the text looks like source code,
// a code snippet, or a local/relative file path. These should be handled
// by the model or filesystem tools rather than web search.
func isCodeOrPathQuery(q string) bool {
	// Obvious fenced/keyword-based code snippets.
	if strings.Contains(q, "```") ||
		strings.Contains(q, "package ") ||
		strings.Contains(q, "func ") ||
		strings.Contains(q, "class ") {
		return true
	}

	// Heuristic: treat backslashes as path indicators and most bare paths
	// (with "/") as local/relative paths, but allow full URLs.
	if strings.Contains(q, "\\") {
		return true
	}
	if strings.Contains(q, "/") &&
		!strings.Contains(q, "http://") &&
		!strings.Contains(q, "https://") {
		return true
	}

	// Common source/config/document file extensions.
	fileExts := []string{
		".go", ".ts", ".tsx", ".js", ".jsx", ".rs", ".py", ".java",
		".c", ".cpp", ".h", ".hpp", ".cs", ".rb", ".php", ".sh",
		".zsh", ".bash", ".ps1",
		".toml", ".yaml", ".yml", ".json",
		".md", ".txt",
	}
	for _, ext := range fileExts {
		if strings.Contains(q, ext) {
			return true
		}
	}

	return false
}

// isAckLikeMessage implements the "ack filter" for short messages. When the
// message is very short and either matches a known acknowledgement phrase or
// lacks any question mark / info keyword / question word, we treat it as a
// non-searchy backchannel turn.
func isAckLikeMessage(q string, wordCount int) bool {
	if wordCount == 0 {
		return false
	}

	// "Short" here intentionally captures very brief backchannels like
	// "ok", "thanks", "that's interesting", etc.
	if wordCount <= 3 {
		normalized := strings.Trim(q, " \t\r\n.!?,")
		for _, phrase := range autoSearchAckPhrases {
			if normalized == phrase {
				return true
			}
		}

		// If a very short message has no question mark, no info keyword,
		// and no leading question word, it is almost certainly not an
		// information-seeking query.
		if !strings.Contains(q, "?") &&
			!containsInfoKeyword(q) &&
			!startsWithQuestionWord(q) {
			return true
		}
	}

	return false
}

// startsWithQuestionWord reports whether the text begins with a typical
// question word like "who", "what", etc. It is robust to simple variants
// such as "what's" by checking prefixes.
func startsWithQuestionWord(q string) bool {
	if q == "" {
		return false
	}
	words := strings.Fields(q)
	if len(words) == 0 {
		return false
	}
	first := strings.Trim(words[0], " '\"")
	for _, w := range autoSearchQuestionWords {
		if strings.HasPrefix(first, w) {
			return true
		}
	}
	return false
}

// containsInfoKeyword checks whether the text includes any of the
// information-seeking keywords defined above (prices, latest, news, etc.).
func containsInfoKeyword(q string) bool {
	for _, kw := range autoSearchInfoKeywords {
		if strings.Contains(q, kw) {
			return true
		}
	}
	return false
}

// debugStreamLoggingEnabled reports whether streaming debug logs should be
// emitted to stderr for diagnostics. Controlled via OLLAMA_TUI_DEBUG_STREAM.
func debugStreamLoggingEnabled() bool {
	return strings.TrimSpace(os.Getenv("OLLAMA_TUI_DEBUG_STREAM")) != ""
}

// logStreamDebug writes a compact, single-line debug entry to stderr when
// streaming debug is enabled. It is safe to call frequently.
func logStreamDebug(format string, args ...interface{}) {
	if !debugStreamLoggingEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "[STREAM] "+format+"\n", args...)
}

func (m *Model) runSearch(query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		results, err := m.searchClient.Search(ctx, query, 5)
		return searchResultMsg{query: query, results: results, err: err}
	}
}

func (m *Model) runExec(command string) tea.Cmd {
	return func() tea.Msg {
		result := m.runToolBashExec(command)
		return execResultMsg{command: command, result: result}
	}
}

// loadSystemContextAuto injects startup context for new conversations:
// the filesystem map (if context_paths is configured).
func (m *Model) loadSystemContextAuto() {
	if len(m.contextPaths) > 0 {
		fsMap := buildFilesystemMap(m.contextPaths)
		if fsMap != "" {
			m.appendMessage(NewSystemMessage(fsMap))
		}
	}
}

func (m *Model) handleInjectCommand(args string) tea.Cmd {
	path := strings.TrimSpace(args)
	if path == "" {
		m.showSystemNotice("Usage: /inject <file_path>")
		return nil
	}

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}

	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		m.showSystemNotice(fmt.Sprintf("Failed to read file: %v", err))
		return nil
	}

	// Inject as system message
	fileName := filepath.Base(path)
	injectedMsg := fmt.Sprintf("=== INJECTED CONTEXT: %s ===\n%s\n=== END CONTEXT ===", fileName, string(content))
	m.appendMessage(NewSystemMessage(injectedMsg))
	m.updateViewportContent()
	m.viewportGotoBottom()

	m.showSystemNotice(fmt.Sprintf("Injected %s (%d bytes)", fileName, len(content)))
	return nil
}

func (m *Model) handleContextCommand(args string) tea.Cmd {
	preset := strings.TrimSpace(strings.ToLower(args))
	if preset == "" || preset == "system" {
		m.showSystemNotice("System context-docs are no longer used. Load a skill with /skill, or inject a file with /inject <path>.")
		return nil
	}

	home, _ := os.UserHomeDir()
	var path string

	switch preset {
	case "patterns":
		// Load all files from connection-patterns directory
		path = filepath.Join(home, "memory/connection-patterns")
		entries, err := os.ReadDir(path)
		if err != nil {
			m.showSystemNotice(fmt.Sprintf("Could not read patterns directory: %v", err))
			return nil
		}

		var combined strings.Builder
		combined.WriteString("=== CONNECTION PATTERNS ===\n\n")
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			filePath := filepath.Join(path, entry.Name())
			content, err := os.ReadFile(filePath)
			if err == nil {
				combined.WriteString(fmt.Sprintf("## %s\n%s\n\n", entry.Name(), string(content)))
			}
		}
		combined.WriteString("=== END PATTERNS ===")

		m.appendMessage(NewSystemMessage(combined.String()))
		m.updateViewportContent()
		m.viewportGotoBottom()
		m.showSystemNotice("Loaded connection patterns")
		return nil

	case "recent":
		// Load last 5 stream-of-consciousness entries
		path = filepath.Join(home, "memory/stream-of-consciousness")
		entries, err := os.ReadDir(path)
		if err != nil {
			m.showSystemNotice(fmt.Sprintf("Could not read stream directory: %v", err))
			return nil
		}

		// Sort by name (date-based filenames) and take last 5
		var recentFiles []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				recentFiles = append(recentFiles, entry.Name())
			}
		}
		if len(recentFiles) > 5 {
			recentFiles = recentFiles[len(recentFiles)-5:]
		}

		var combined strings.Builder
		combined.WriteString("=== RECENT STREAM-OF-CONSCIOUSNESS ===\n\n")
		for _, fileName := range recentFiles {
			filePath := filepath.Join(path, fileName)
			content, err := os.ReadFile(filePath)
			if err == nil {
				combined.WriteString(fmt.Sprintf("## %s\n%s\n\n", fileName, string(content)))
			}
		}
		combined.WriteString("=== END RECENT NOTES ===")

		m.appendMessage(NewSystemMessage(combined.String()))
		m.updateViewportContent()
		m.viewportGotoBottom()
		m.showSystemNotice(fmt.Sprintf("Loaded %d recent entries", len(recentFiles)))
		return nil

	default:
		m.showSystemNotice(fmt.Sprintf("Unknown preset: %s (try: system, patterns, recent)", preset))
		return nil
	}
}

func (m *Model) handleDeleteCommand(args string) tea.Cmd {
	if m.currentSessionID == 0 {
		m.showSystemNotice("No active chat to delete")
		return nil
	}
	if err := m.deleteSession(m.currentSessionID); err != nil {
		m.showSystemNotice(fmt.Sprintf("Delete failed: %v", err))
		return nil
	}
	m.showSystemNotice("Chat deleted")
	return nil
}

// Export helpers have been removed in favor of simpler, copy-based workflows.

// extractThinking separates thinking/reasoning from the actual answer in model output
// Returns (thinking, answer)
func extractThinking(content string) (string, string) {
	const (
		beginTag = "<|begin_of_box|>"
		endTag   = "<|end_of_box|>"
	)

	// Check for box tags first (GLM, DeepSeek R1, QwQ)
	if startIdx := strings.Index(content, beginTag); startIdx >= 0 {
		if endIdx := strings.Index(content, endTag); endIdx > startIdx {
			thinking := strings.TrimSpace(content[:startIdx])
			answer := strings.TrimSpace(content[startIdx+len(beginTag) : endIdx])
			return thinking, answer
		}
	}

	// Handle common explicit thinking/analysis tags.
	if thinkStart := strings.Index(strings.ToLower(content), "<think>"); thinkStart >= 0 {
		if thinkEnd := strings.Index(strings.ToLower(content), "</think>"); thinkEnd > thinkStart {
			thinking := strings.TrimSpace(content[thinkStart+len("<think>") : thinkEnd])
			answer := strings.TrimSpace(content[:thinkStart] + content[thinkEnd+len("</think>"):])
			if answer != "" {
				return thinking, answer
			}
		}
	}
	if analysisStart := strings.Index(strings.ToLower(content), "<analysis>"); analysisStart >= 0 {
		if analysisEnd := strings.Index(strings.ToLower(content), "</analysis>"); analysisEnd > analysisStart {
			thinking := strings.TrimSpace(content[analysisStart+len("<analysis>") : analysisEnd])
			answer := strings.TrimSpace(content[:analysisStart] + content[analysisEnd+len("</analysis>"):])
			if answer != "" {
				return thinking, answer
			}
		}
	}

	// Check for explicit answer markers and split on the last occurrence.
	lower := strings.ToLower(content)
	markers := []string{"final answer:", "final answer", "answer:", "final:", "response:"}
	for _, marker := range markers {
		if idx := strings.LastIndex(lower, marker); idx >= 0 {
			answer := strings.TrimSpace(content[idx+len(marker):])
			answer = strings.TrimLeft(answer, " .:-\n\t")
			if answer != "" {
				thinking := strings.TrimSpace(content[:idx])
				return thinking, answer
			}
		}
	}

	// No box tags - try to detect thinking by patterns
	// Common thinking phrases that indicate the start of reasoning
	thinkingPhrases := []string{
		"Got it, let's tackle this",
		"First, I need to",
		"Let me think",
		"So first,",
		"Looking at",
		"Wait,",
		"Since it's",
		"But wait",
		"Alternatively,",
		"Let's think about",
	}

	for _, phrase := range thinkingPhrases {
		if strings.Contains(lower, strings.ToLower(phrase)) {
			// Content looks like thinking - try to find where actual answer starts
			// Usually after the last occurrence of thinking patterns
			lines := strings.Split(content, "\n")

			// Find the last line that contains thinking patterns
			lastThinkingLine := -1
			for i, line := range lines {
				lineLower := strings.ToLower(line)
				for _, p := range thinkingPhrases {
					if strings.Contains(lineLower, strings.ToLower(p)) {
						lastThinkingLine = i
						break
					}
				}
			}

			if lastThinkingLine >= 0 && lastThinkingLine < len(lines)-1 {
				// Everything up to and including last thinking line is thinking
				thinking := strings.TrimSpace(strings.Join(lines[:lastThinkingLine+1], "\n"))
				answer := strings.TrimSpace(strings.Join(lines[lastThinkingLine+1:], "\n"))

				// Only split if we have a reasonable answer
				if len(answer) > 10 {
					return thinking, answer
				}
			}

			// If we can't find clear split, treat all as thinking since it contains thinking phrases
			// But this might be too aggressive - let's not split unclear cases
			break
		}
	}

	// Heuristic fallback: if the content starts with reasoning cues and ends
	// with a short final line, treat that final line as the answer.
	reasoningCues := []string{
		"we have a conversation",
		"the user says",
		"the user wants",
		"the user is asking",
		"the system expects",
		"as per",
		"we need to",
		"i should",
		"i will",
		"it doesn't explicitly",
		"it does not explicitly",
		"let me try",
		"however,",
		"however ",
		"we should",
		"probably best",
		"let's output",
		"let's answer",
		"i'll answer",
		"i'll output",
		"respond with",
		"answer should",
		"best:",
		"thus",
		"therefore",
		"final answer",
		"answer:",
	}
	for _, cue := range reasoningCues {
		if strings.Contains(lower, cue) {
			lines := strings.Split(content, "\n")
			lastLine := ""
			lastIdx := -1
			for i := len(lines) - 1; i >= 0; i-- {
				if strings.TrimSpace(lines[i]) == "" {
					continue
				}
				lastLine = strings.TrimSpace(lines[i])
				lastIdx = i
				break
			}
			if lastIdx > 0 && len(lastLine) > 0 && len(lastLine) <= 200 {
				thinking := strings.TrimSpace(strings.Join(lines[:lastIdx], "\n"))
				if thinking != "" {
					return thinking, lastLine
				}
			}
			break
		}
	}

	// Inline reasoning fallback for models that emit chain-of-thought in plain
	// text and then append a final answer sentence without tags.
	if thinking, answer, ok := splitInlineReasoningAnswer(content, reasoningCues); ok {
		return thinking, answer
	}

	// Paragraph-level fallback: if earlier paragraphs look like internal
	// reasoning and the final paragraph looks like a user-facing answer, split.
	if thinking, answer, ok := splitReasoningByParagraph(content, reasoningCues); ok {
		return thinking, answer
	}

	// No thinking detected - return empty thinking and full content as answer
	return "", content
}

func splitInlineReasoningAnswer(content string, reasoningCues []string) (string, string, bool) {
	lower := strings.ToLower(content)
	answerStarters := []string{
		"i'll ",
		"i will ",
		"sure",
		"here's ",
		"here is ",
		"the answer is",
		"you can ",
		"it is ",
		"it's ",
	}

	bestIdx := -1
	for _, starter := range answerStarters {
		searchStart := 0
		for searchStart < len(lower) {
			idxRel := strings.Index(lower[searchStart:], starter)
			if idxRel < 0 {
				break
			}
			idx := searchStart + idxRel
			if hasSentenceBoundaryBefore(lower, idx) {
				bestIdx = idx
			}
			searchStart = idx + len(starter)
		}
	}
	if bestIdx <= 0 {
		return "", "", false
	}

	thinking := strings.TrimSpace(content[:bestIdx])
	answer := strings.TrimSpace(content[bestIdx:])
	if len(thinking) < 40 || len(answer) < 8 {
		return "", "", false
	}
	if reasoningCueHits(strings.ToLower(thinking), reasoningCues) < 2 {
		return "", "", false
	}
	return thinking, answer, true
}

func splitReasoningByParagraph(content string, reasoningCues []string) (string, string, bool) {
	rawParas := strings.Split(content, "\n\n")
	paragraphs := make([]string, 0, len(rawParas))
	for _, para := range rawParas {
		trimmed := strings.TrimSpace(para)
		if trimmed != "" {
			paragraphs = append(paragraphs, trimmed)
		}
	}
	if len(paragraphs) < 2 {
		return "", "", false
	}

	answer := paragraphs[len(paragraphs)-1]
	thinking := strings.TrimSpace(strings.Join(paragraphs[:len(paragraphs)-1], "\n\n"))
	if len(answer) < 8 || len(thinking) < 40 {
		return "", "", false
	}

	answerLower := strings.ToLower(answer)
	thinkingLower := strings.ToLower(thinking)
	if reasoningCueHits(thinkingLower, reasoningCues) < 2 {
		return "", "", false
	}
	// Avoid over-splitting when the final paragraph still looks like internal reasoning.
	if reasoningCueHits(answerLower, reasoningCues) > 0 {
		return "", "", false
	}
	return thinking, answer, true
}

func hasSentenceBoundaryBefore(lower string, idx int) bool {
	if idx <= 0 || idx > len(lower) {
		return false
	}
	for i := idx - 1; i >= 0; i-- {
		ch := lower[i]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '"' || ch == '\'' {
			continue
		}
		switch ch {
		case '.', '!', '?', ':', ';', ')':
			return true
		default:
			return false
		}
	}
	return true
}

func reasoningCueHits(lower string, cues []string) int {
	hits := 0
	for _, cue := range cues {
		if cue == "" {
			continue
		}
		if strings.Contains(lower, cue) {
			hits++
		}
	}
	return hits
}
