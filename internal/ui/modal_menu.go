package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"shinobi/internal/storage"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// modalMenuMode tracks the active modal menu hierarchy.
type modalMenuMode int

const (
	modalMenuHidden modalMenuMode = iota
	modalMenuMain
	modalMenuChatActions
	modalMenuSelectModel
	modalMenuSelectAgent
	modalMenuHelp
)

// modalMenuItem defines a selectable row inside the menu overlay.
type modalMenuItem struct {
	Label       string
	Icon        string // Optional prefix icon (e.g., "+", "×")
	Description string
	Shortcut    string // Optional keyboard shortcut hint (e.g., "m", "a", "?")
	Action      func() tea.Cmd
	Disabled    bool
	Separator   bool
}

type menuPromptKind int

const (
	menuPromptRenameChat menuPromptKind = iota + 1
)

type menuPromptState struct {
	kind        menuPromptKind
	session     sessionSummary
	returnMode  modalMenuMode
	placeholder string
}

type menuConfirmState struct {
	prompt    string
	onConfirm func() tea.Cmd
}

// formatTableRow formats a table-like row with left-aligned label and right-aligned metadata
// Uses simple spacing and dot separators for readability
func formatTableRow(label string, metadata string, totalWidth int, styles Styles) string {
	const maxLabelWidth = 35
	const minMetadataSpace = 20

	// Truncate label if too long
	displayLabel := label
	if len(label) > maxLabelWidth {
		displayLabel = label[:maxLabelWidth-3] + "..."
	}

	// Calculate spacing
	availableSpace := totalWidth - len(displayLabel) - len(metadata) - 3 // -3 for separator
	if availableSpace < 2 {
		availableSpace = 2
	}

	// Build row with spacing
	separator := strings.Repeat(" ", availableSpace) + styles.Icons.Dot + " "

	return displayLabel + separator + metadata
}

func (m *Model) captureInputFocus() {
	if m == nil {
		return
	}
	if !m.inputFocusCaptured {
		m.inputFocusCaptured = true
	}
	m.input.Blur()
}

func (m *Model) releaseInputFocus() {
	if m == nil || !m.inputFocusCaptured {
		return
	}
	m.inputFocusCaptured = false
	m.input.Focus()
}

func (m *Model) menuVisible() bool {
	return false
}

func (m *Model) showModalMenu(mode modalMenuMode) {
	if m == nil {
		return
	}
	m.captureInputFocus()
	m.hideCommandMenu()
	m.hideHelpPanel()
	m.menuStack = nil
	m.menuMode = mode
	m.menuSelection = 0
	m.menuStatus = ""
	m.menuConfirm = nil
	m.rebuildModalMenuItems()
}

func (m *Model) closeModalMenu() {
	m.menuStack = nil
	m.menuMode = modalMenuHidden
	m.menuSelection = 0
	m.menuStatus = ""
	m.menuConfirm = nil
	m.releaseInputFocus()
}

func (m *Model) pushModalMenu(mode modalMenuMode) {
	m.menuStack = append(m.menuStack, m.menuMode)
	m.menuMode = mode
	m.menuSelection = 0
	m.menuStatus = ""
	m.menuConfirm = nil
	m.rebuildModalMenuItems()
}

func (m *Model) popModalMenu() {
	if len(m.menuStack) == 0 {
		m.closeModalMenu()
		return
	}
	last := m.menuStack[len(m.menuStack)-1]
	m.menuStack = m.menuStack[:len(m.menuStack)-1]
	m.menuMode = last
	m.menuSelection = 0
	m.menuConfirm = nil
	m.menuStatus = ""
	m.rebuildModalMenuItems()
}

func (m *Model) handleModalMenuKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !m.menuVisible() {
		return nil, false
	}
	if m.menuPrompt != nil {
		return m.handleMenuPromptKey(msg), true
	}
	key := msg.String()
	if m.menuConfirm != nil {
		return m.handleMenuConfirmKey(key), true
	}

	// Keyboard shortcuts for main menu
	if m.menuMode == modalMenuMain {
		switch key {
		case "n":
			m.closeModalMenu()
			return m.handleNewChatCommand(""), true
		case "a":
			m.pushModalMenu(modalMenuSelectAgent)
			return nil, true
		case "?":
			m.pushModalMenu(modalMenuHelp)
			return nil, true
		}
	}

	switch key {
	case "up", "k":
		m.moveModalSelection(-1)
		return nil, true
	case "down", "j":
		m.moveModalSelection(1)
		return nil, true
	case "enter":
		return m.activateModalSelection(), true
	case "/":
		m.closeModalMenu()
		return nil, true
	case "esc":
		if len(m.menuStack) > 0 {
			m.popModalMenu()
		} else {
			m.closeModalMenu()
		}
		return nil, true
	case "q":
		if m.menuMode == modalMenuMain {
			return tea.Quit, true
		}
		m.closeModalMenu()
		return nil, true
	}
	return nil, true
}

func (m *Model) handleMenuPromptKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "esc", "ctrl+c":
		m.cancelMenuPrompt(true)
		return nil
	case "enter":
		if m.menuPrompt == nil {
			return nil
		}
		value := m.menuPromptInput.Value()
		return m.handleMenuPromptSubmit(m.menuPrompt, value)
	}
	var cmd tea.Cmd
	m.menuPromptInput, cmd = m.menuPromptInput.Update(msg)
	return cmd
}

func (m *Model) handleMenuConfirmKey(key string) tea.Cmd {
	if m.menuConfirm == nil {
		return nil
	}
	switch key {
	case "y", "enter":
		confirm := m.menuConfirm
		m.menuConfirm = nil
		m.menuStatus = ""
		if confirm.onConfirm != nil {
			return confirm.onConfirm()
		}
		return nil
	case "n", "esc", "ctrl+c":
		m.menuConfirm = nil
		m.menuStatus = "Canceled"
		return nil
	}
	return nil
}

func (m *Model) moveModalSelection(delta int) {
	if len(m.menuItems) == 0 {
		return
	}
	next := m.menuSelection
	attempts := 0
	for attempts < len(m.menuItems) {
		next += delta
		if next < 0 {
			next = len(m.menuItems) - 1
		} else if next >= len(m.menuItems) {
			next = 0
		}
		if !m.menuItems[next].Disabled && !m.menuItems[next].Separator {
			m.menuSelection = next
			return
		}
		attempts++
	}
}

func (m *Model) activateModalSelection() tea.Cmd {
	if len(m.menuItems) == 0 {
		return nil
	}
	item := m.menuItems[m.menuSelection]
	if item.Disabled || item.Separator {
		return nil
	}
	if item.Action != nil {
		return item.Action()
	}
	return nil
}

func (m *Model) rebuildModalMenuItems() {
	m.menuItems = m.buildModalMenuItems(m.menuMode)
	if m.menuSelection >= len(m.menuItems) {
		m.menuSelection = len(m.menuItems) - 1
	}
	if m.menuSelection < 0 {
		m.menuSelection = 0
	}
}

func (m *Model) buildModalMenuItems(mode modalMenuMode) []modalMenuItem {
	switch mode {
	case modalMenuMain:
		items := []modalMenuItem{
			{
				Label:       "/agent",
				Description: m.currentAgentName(),
				Shortcut:    "a",
				Action: func() tea.Cmd {
					m.pushModalMenu(modalMenuSelectAgent)
					return nil
				},
			},
		}
		items = append(items,
			modalMenuItem{
				Label:       "/skill",
				Description: "Apply a skill",
				Action: func() tea.Cmd {
					m.closeModalMenu()
					m.showSkillMenu()
					return nil
				},
			},
			modalMenuItem{
				Label:       "/new",
				Description: "Start fresh chat",
				Shortcut:    "n",
				Action: func() tea.Cmd {
					m.closeModalMenu()
					return m.handleNewChatCommand("")
				},
			},
			modalMenuItem{
				Label:       "Current Chat",
				Description: "Rename, delete",
				Disabled:    m.currentSessionID == 0,
				Action: func() tea.Cmd {
					if m.currentSessionID == 0 {
						return nil
					}
					m.pushModalMenu(modalMenuChatActions)
					return nil
				},
			},
			modalMenuItem{
				Label:       "/help",
				Description: "Keyboard shortcuts",
				Shortcut:    "?",
				Action: func() tea.Cmd {
					m.pushModalMenu(modalMenuHelp)
					return nil
				},
			},
			modalMenuItem{
				Label:       "Close Menu",
				Description: "Return to chat",
				Action: func() tea.Cmd {
					m.closeModalMenu()
					return nil
				},
			},
		)
		return items
	case modalMenuChatActions:
		return []modalMenuItem{
			{
				Label:       "Rename Chat",
				Description: "Update the chat title",
				Disabled:    m.currentSessionID == 0 || m.store == nil,
				Action: func() tea.Cmd {
					if m.currentSessionID == 0 || m.store == nil {
						return nil
					}
					summary := sessionSummary{ID: m.currentSessionID, Title: m.currentSessionLabel, ProjectID: m.currentProjectID}
					return m.beginMenuPrompt(menuPromptRenameChat, "New chat title…", &summary, modalMenuChatActions)
				},
			},
			{
				Label:       "Delete Chat",
				Description: "Remove chat history",
				Disabled:    m.currentSessionID == 0 || m.store == nil,
				Action: func() tea.Cmd {
					if m.currentSessionID == 0 || m.store == nil {
						return nil
					}
					id := m.currentSessionID
					m.beginMenuConfirm("Delete this chat?", func() tea.Cmd {
						if err := m.deleteSession(id); err != nil {
							m.showSystemNotice(fmt.Sprintf("Delete failed: %v", err))
							return nil
						}
						m.showSystemNotice("Chat deleted")
						m.closeModalMenu()
						return nil
					})
					return nil
				},
			},
			{
				Label:       "Export Chat",
				Description: "Save transcript to file",
				Disabled:    m.currentSessionID == 0,
				Action: func() tea.Cmd {
					if m.currentSessionID == 0 {
						return nil
					}
					m.closeModalMenu()
					return m.handleExportCommand(fmt.Sprintf("session:%d", m.currentSessionID))
				},
			},
		}
	case modalMenuSelectModel:
		return m.buildModelSelectionItems()
	case modalMenuSelectAgent:
		return m.buildAgentSelectionItems()
	case modalMenuHelp:
		// Non-interactive help overview; Esc backs out.
		lines := []string{
			"Enter — Send message or confirm menus",
			"Shift+Enter — Insert a newline",
			"/ — Open command palette",
			"Tab — Cycle active agent",
			"Esc — Close menus or cancel streaming",
			"↑/↓ or PgUp/PgDn — Scroll chat history",
			"Ctrl+C — Cancel streaming or quit",
			"",
			"/agent — Switch between agents",
			"/menu — Open main menu",
			"/new — Start a fresh chat",
		}
		items := make([]modalMenuItem, 0, len(lines)+1)
		for _, line := range lines {
			label := line
			if strings.TrimSpace(label) == "" {
				label = " "
			}
			items = append(items, modalMenuItem{
				Label:    label,
				Disabled: true,
			})
		}
		items = append(items, modalMenuItem{
			Separator: true,
		})
		items = append(items, modalMenuItem{
			Label:       "Close Help",
			Description: "Return to chat",
			Action: func() tea.Cmd {
				m.closeModalMenu()
				return nil
			},
		})
		return items
	}
	return nil
}

func (m *Model) menuTitle() string {
	switch m.menuMode {
	case modalMenuMain:
		return "Main Menu"
	case modalMenuChatActions:
		return "Current Chat"
	case modalMenuSelectModel:
		return "Select Default Model"
	case modalMenuSelectAgent:
		return "Select Default Agent"
	case modalMenuHelp:
		return "Help"
	default:
		return "Menu"
	}
}

func (m *Model) menuHint() string {
	if m.menuPrompt != nil {
		return "Enter save  Esc cancel"
	}
	if m.menuConfirm != nil {
		return "y: confirm  n: cancel"
	}
	base := "↑/↓ navigate  Enter select  Esc back"
	if m.menuMode == modalMenuMain {
		base = "↑/↓ navigate  Enter select  Q quit"
	}
	return base
}

func (m *Model) renderModalMenu() string {
	if len(m.menuItems) == 0 {
		return ""
	}
	width := m.width - 6
	if width < 40 {
		width = 40
	}
	if width > 80 {
		width = 80
	}
	bodyLines := []string{m.styles.MenuTitle.Render(strings.ToUpper(m.menuTitle()))}
	for idx, item := range m.menuItems {
		if item.Separator {
			bodyLines = append(bodyLines, strings.Repeat("─", 32))
			continue
		}
		// Build label with optional icon
		label := item.Label
		if item.Icon != "" {
			label = item.Icon + " " + label
		}

		// Selection indicator
		prefix := m.styles.Icons.Unselected
		style := m.styles.MenuItem
		if idx == m.menuSelection {
			prefix = m.styles.Icons.Selected + " "
			style = m.styles.MenuItemSelected
		}
		if item.Disabled {
			style = m.styles.MenuItem.Copy().Foreground(lipgloss.Color(colorMuted))
		}
		renderedLabel := style.Render(prefix + label)

		// Description
		desc := ""
		if item.Description != "" {
			desc = m.styles.MenuItemDescription.Render("  " + item.Description)
		}

		// Shortcut hint (only show on selected item)
		shortcut := ""
		if item.Shortcut != "" && idx == m.menuSelection {
			shortcut = m.styles.MenuHint.Render("  [" + item.Shortcut + "]")
		}

		bodyLines = append(bodyLines, lipgloss.JoinHorizontal(lipgloss.Top, renderedLabel, desc, shortcut))
	}
	if m.menuPrompt != nil {
		promptWidth := width - 6
		if promptWidth < 20 {
			promptWidth = 20
		}
		m.menuPromptInput.Width = promptWidth
		bodyLines = append(bodyLines, "")
		bodyLines = append(bodyLines, m.menuPromptInput.View())
	}
	if m.menuStatus != "" {
		bodyLines = append(bodyLines, m.styles.MenuHint.Render(m.menuStatus))
	}
	bodyLines = append(bodyLines, m.styles.MenuHint.Render(m.menuHint()))
	content := lipgloss.JoinVertical(lipgloss.Left, bodyLines...)

	// Select appropriate border style based on menu type
	var containerStyle lipgloss.Style
	switch m.menuMode {
	case modalMenuMain, modalMenuSelectModel, modalMenuSelectAgent:
		containerStyle = m.styles.MenuBoxPrimary
	case modalMenuHelp:
		containerStyle = m.styles.MenuBoxInfo
	case modalMenuChatActions:
		// Use action style if menu has destructive operations
		hasDestructive := false
		for _, item := range m.menuItems {
			if strings.Contains(item.Label, "Delete") {
				hasDestructive = true
				break
			}
		}
		if hasDestructive {
			containerStyle = m.styles.MenuBoxAction
		} else {
			containerStyle = m.styles.MenuBoxSecondary
		}
	default:
		containerStyle = m.styles.MenuBox // Fallback to existing style
	}

	return containerStyle.Width(width).Render(content)
}

func (m *Model) applyModalOverlay(base string) string {
	if m.width == 0 || m.height == 0 {
		return base
	}

	var overlay string
	switch {
	case m.menuVisible():
		overlay = m.renderModalMenu()
	case m.commandPalette != nil && m.commandMenuOn:
		overlay = m.renderCommandPaletteOverlay()
	default:
		return base
	}

	if overlay == "" {
		return base
	}
	baseBlock := lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, base, lipgloss.WithWhitespaceChars(" "))
	overlayBlock := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay, lipgloss.WithWhitespaceChars(" "))
	baseLines := strings.Split(baseBlock, "\n")
	overlayLines := strings.Split(overlayBlock, "\n")
	maxLines := len(baseLines)
	if len(overlayLines) > maxLines {
		maxLines = len(overlayLines)
	}
	result := make([]string, maxLines)
	for i := 0; i < maxLines; i++ {
		var baseLine, overlayLine string
		if i < len(baseLines) {
			baseLine = baseLines[i]
		}
		if i < len(overlayLines) {
			overlayLine = overlayLines[i]
		}
		if strings.TrimSpace(overlayLine) != "" {
			result[i] = overlayLine
		} else {
			result[i] = baseLine
		}
	}
	return strings.Join(result, "\n")
}

func (m *Model) renderCommandPaletteOverlay() string {
	if m.commandPalette == nil || !m.commandMenuOn {
		return ""
	}
	width := m.width - 8
	if width < 40 {
		width = m.width - 4
	}
	if width < 20 {
		width = 20
	}
	return m.commandPalette.ViewInline(width)
}

func (m *Model) renderCommandPaletteInline() string {
	if m.commandPalette == nil || !m.commandMenuOn {
		return ""
	}
	width := m.width - 4
	if width < 20 {
		width = 20
	}
	return m.commandPalette.ViewInline(width)
}

func (m *Model) applyModelSelection(label string) tea.Cmd {
	id, ok := m.modelCommandMap[label]
	if !ok {
		m.menuStatus = fmt.Sprintf("Model %s unavailable", label)
		return nil
	}
	if id == m.currentModelID {
		m.menuStatus = fmt.Sprintf("%s already active", label)
		return nil
	}
	route := m.resolveRouteForModelID(id)
	m.setCurrentModelFromID(id)
	m.menuStatus = fmt.Sprintf("Switching to %s…", label)
	m.modelWarmStatus = fmt.Sprintf("Using %s", label)
	if err := m.persistDefaultModel(id); err != nil {
		m.menuStatus = fmt.Sprintf("Save failed: %v", err)
	}
	if m.menuVisible() {
		m.rebuildModalMenuItems()
	}
	_ = route // currently unused; kept for forward compatibility with multiple backends
	return nil
}

func (m *Model) applyAgentSelection(name string) tea.Cmd {
	if strings.EqualFold(m.currentAgentName(), name) {
		m.menuStatus = fmt.Sprintf("%s already active", name)
		return nil
	}
	agent := m.resolveAgentSelection(name)
	if agent == nil {
		m.menuStatus = fmt.Sprintf("Agent %s unavailable", name)
		return nil
	}
	if !m.setActiveAgentByPath(agent.FilePath, false) {
		m.menuStatus = fmt.Sprintf("Agent %s unavailable", agent.Name)
		return nil
	}
	m.menuStatus = fmt.Sprintf("Agent set to %s", agent.Name)
	_ = m.persistDefaultAgent(agent.Name)
	if m.menuVisible() {
		m.rebuildModalMenuItems()
	}
	return nil
}

func (m *Model) toggleStatusBarSetting() tea.Cmd {
	m.showStatusBar = !m.showStatusBar
	m.menuStatus = fmt.Sprintf("Status bar %s", toggleStateLabel(m.showStatusBar))
	if err := m.persistAppearanceSetting(storage.SettingKeyShowStatusBar, m.showStatusBar); err != nil {
		m.menuStatus = fmt.Sprintf("Save failed: %v", err)
	}
	if m.menuVisible() {
		m.rebuildModalMenuItems()
	}
	return nil
}

func (m *Model) toggleTimestampSetting() tea.Cmd {
	m.showTimestamps = !m.showTimestamps
	m.menuStatus = fmt.Sprintf("Timestamps %s", toggleStateLabel(m.showTimestamps))
	m.updateViewportContent()
	if err := m.persistAppearanceSetting(storage.SettingKeyShowTimestamps, m.showTimestamps); err != nil {
		m.menuStatus = fmt.Sprintf("Save failed: %v", err)
	}
	if m.menuVisible() {
		m.rebuildModalMenuItems()
	}
	return nil
}

func (m *Model) buildModelSelectionItems() []modalMenuItem {
	if len(m.modelCommandMap) == 0 {
		return []modalMenuItem{{Label: "No models available", Disabled: true}}
	}
	items := make([]modalMenuItem, 0, len(m.modelCommandMap))
	added := map[string]bool{}
	for _, opt := range m.modelOptions {
		if _, ok := m.modelCommandMap[opt.Label]; !ok {
			continue
		}
		items = append(items, m.modelSelectionItem(opt.Label, opt.ID))
		added[opt.Label] = true
	}
	for label, id := range m.modelCommandMap {
		if added[label] {
			continue
		}
		items = append(items, m.modelSelectionItem(label, id))
	}
	return items
}

func (m *Model) modelSelectionItem(label, id string) modalMenuItem {
	active := id == m.currentModelID
	desc := m.modelMenuDescription(id, active)

	// Visually highlight the active model with a leading dot so it's
	// immediately obvious which model/backend is currently in use.
	rawLabel := label
	optionLabel := label
	if active {
		optionLabel = m.styles.Icons.Active + " " + label
	} else {
		optionLabel = m.styles.Icons.Inactive + " " + label
	}

	return modalMenuItem{
		Label:       optionLabel,
		Description: desc,
		Disabled:    active,
		Action: func() tea.Cmd {
			// Use the raw label (without icon prefix) for lookups.
			return m.applyModelSelection(rawLabel)
		},
	}
}

func (m *Model) buildAgentSelectionItems() []modalMenuItem {
	if len(m.agents) == 0 {
		return []modalMenuItem{{Label: "No agents installed", Disabled: true}}
	}
	items := make([]modalMenuItem, 0, len(m.agents))
	current := m.currentAgentName()
	nameCounts := m.agentNameCounts()
	for _, agent := range m.agents {
		agCopy := agent
		label := agCopy.Name
		if nameCounts[strings.ToLower(agCopy.Name)] > 1 {
			group := agentGroupForPath(agCopy.FilePath)
			if group != "" {
				label = fmt.Sprintf("%s/%s", group, agCopy.Name)
			}
		}
		parts := []string{}
		if strings.TrimSpace(agent.Description) != "" {
			parts = append(parts, agent.Description)
		}
		if agCopy.Model != "" {
			parts = append(parts, fmt.Sprintf("Model: %s", agCopy.Model))
		}
		if strings.EqualFold(agCopy.Name, current) {
			parts = append(parts, "Active")
		}
		if len(parts) == 0 {
			parts = append(parts, "No description")
		}
		desc := strings.Join(parts, " "+m.styles.Icons.Dot+" ")
		items = append(items, modalMenuItem{
			Label:       label,
			Description: desc,
			Disabled:    strings.EqualFold(agCopy.Name, current),
			Action: func() tea.Cmd {
				return m.applyAgentSelection(label)
			},
		})
	}
	return items
}

func toggleStateLabel(enabled bool) string {
	if enabled {
		return "On"
	}
	return "Off"
}

func (m *Model) sessionsForProject(projectID int64) []sessionSummary {
	if m.store != nil && projectID != 0 {
		if sessions, err := m.store.ConversationsByProject(projectID, 50); err == nil {
			summaries := make([]sessionSummary, 0, len(sessions))
			for _, sess := range sessions {
				summaries = append(summaries, summarizeSession(sess))
			}
			return summaries
		} else {
			fmt.Fprintf(os.Stderr, "Warning: unable to load project conversations: %v\n", err)
		}
	}
	if projectID == 0 {
		return m.sessions
	}
	matches := []sessionSummary{}
	for _, sess := range m.sessions {
		if sess.ProjectID == projectID {
			matches = append(matches, sess)
		}
	}
	return matches
}

func humanizeTime(ts time.Time) string {
	if ts.IsZero() {
		return "recently"
	}
	diff := time.Since(ts)
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	default:
		return ts.Format("Jan 02")
	}
}

func (m *Model) beginMenuPrompt(kind menuPromptKind, placeholder string, session *sessionSummary, returnMode modalMenuMode) tea.Cmd {
	m.menuPrompt = &menuPromptState{
		kind:        kind,
		returnMode:  returnMode,
		placeholder: placeholder,
	}
	if session != nil {
		m.menuPrompt.session = *session
	}
	m.menuStatus = promptInstruction(kind)
	initialValue := ""
	switch kind {
	case menuPromptRenameChat:
		initialValue = strings.TrimSpace(m.menuPrompt.session.Title)
		if initialValue == "" {
			initialValue = strings.TrimSpace(m.menuPrompt.session.Name)
		}
	}
	m.captureInputFocus()
	m.menuPromptInput.Placeholder = placeholder
	m.menuPromptInput.Prompt = ""
	m.menuPromptInput.SetValue(initialValue)
	if initialValue != "" {
		m.menuPromptInput.CursorEnd()
	}
	return m.menuPromptInput.Focus()
}

func (m *Model) cancelMenuPrompt(reopen bool) {
	returnMode := modalMenuMain
	if m.menuPrompt != nil && m.menuPrompt.returnMode != modalMenuHidden {
		returnMode = m.menuPrompt.returnMode
	} else if m.menuMode != modalMenuHidden {
		returnMode = m.menuMode
	}
	m.menuPrompt = nil
	m.menuPromptInput.Blur()
	m.menuPromptInput.SetValue("")
	m.menuStatus = ""
	if reopen && m.menuMode == modalMenuHidden {
		m.showModalMenu(returnMode)
	} else {
		m.rebuildModalMenuItems()
	}
}

func (m *Model) handleMenuPromptSubmit(prompt *menuPromptState, value string) tea.Cmd {
	if prompt == nil {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		m.menuStatus = "Please enter a value"
		return nil
	}
	if err := m.validateMenuPromptInput(prompt, trimmed); err != nil {
		m.menuStatus = err.Error()
		return nil
	}
	success, err := m.executeMenuPrompt(prompt, trimmed)
	if err != nil {
		m.menuStatus = err.Error()
		return nil
	}
	m.menuPrompt = nil
	m.menuPromptInput.Blur()
	m.menuPromptInput.SetValue("")
	m.menuStatus = success
	if prompt.returnMode != modalMenuHidden && m.menuMode == modalMenuHidden {
		m.showModalMenu(prompt.returnMode)
	} else {
		m.rebuildModalMenuItems()
	}
	return nil
}

func promptInstruction(kind menuPromptKind) string {
	switch kind {
	case menuPromptRenameChat:
		return "Type the new chat title"
	default:
		return "Enter text and press Enter"
	}
}

func (m *Model) validateMenuPromptInput(prompt *menuPromptState, value string) error {
	if prompt == nil {
		return errors.New("no prompt active")
	}
	normalized := normalizeMenuValue(value)
	switch prompt.kind {
	case menuPromptRenameChat:
		if prompt.session.ID == 0 {
			return errors.New("Select a chat to rename")
		}
		current := normalizeMenuValue(prompt.session.Title)
		if current == "" {
			current = normalizeMenuValue(prompt.session.Name)
		}
		if current != "" && strings.EqualFold(current, normalized) {
			return errors.New("Chat already uses that title")
		}
	}
	return nil
}

func (m *Model) executeMenuPrompt(prompt *menuPromptState, value string) (string, error) {
	switch prompt.kind {
	case menuPromptRenameChat:
		if err := m.renameSession(prompt.session.ID, value, true); err != nil {
			return "", fmt.Errorf("Rename failed: %w", err)
		}
		return "Chat renamed", nil
	}
	return "", errors.New("Unknown prompt")
}

func normalizeMenuValue(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func (m *Model) beginMenuConfirm(message string, fn func() tea.Cmd) {
	m.menuConfirm = &menuConfirmState{prompt: message, onConfirm: fn}
	m.menuStatus = message + " (Enter=yes, Esc=no)"
}

func (m *Model) renameSession(id int64, title string, userInitiated bool) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title is required")
	}
	if userInitiated {
		m.sessionTitleUserSet = true
	} else if m.sessionTitleUserSet {
		return nil
	}
	m.currentSessionLabel = title
	if m.store == nil || id == 0 {
		return nil
	}
	if err := m.store.UpdateSessionTitle(id, title); err != nil {
		return err
	}
	m.refreshSessionMenu()
	return nil
}

func (m *Model) deleteSession(id int64) error {
	if m.store == nil {
		return fmt.Errorf("store unavailable")
	}
	if err := m.store.DeleteSession(id); err != nil {
		return err
	}
	if m.currentSessionID == id {
		m.currentSessionID = 0
		m.messages = nil
		m.viewportLines = []string{""}
		m.updateViewportContent()
	}
	m.refreshSessionMenu()
	return nil
}
