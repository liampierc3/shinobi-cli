package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/liampierc3/shinobi-cli/internal/commands"

	"github.com/charmbracelet/lipgloss"
)

// CommandPalette renders the slash-command popup.
type CommandPalette struct {
	visible  bool
	filter   string
	matches  []commandMatch
	commands []commands.Command
	selected int
	maxItems int
	styles   commandPaletteStyles
	icons    Icons
}

type commandMatch struct {
	command                commands.Command
	highlightedName        string
	highlightedDescription string
	score                  int
}

func newCommandPalette(cmds []commands.Command, styles Styles) *CommandPalette {
	p := &CommandPalette{
		commands: cmds,
		maxItems: 200,
		styles: commandPaletteStyles{
			Container:    styles.MenuBox,
			Title:        styles.MenuTitle,
			Filter:       styles.InputPrompt,
			Item:         styles.MenuItem,
			SelectedItem: styles.MenuItemSelected,
			Description:  styles.MenuItemDescription,
			Hint:         styles.MenuHint,
			Empty:        styles.MenuItemDescription,
		},
		icons: styles.Icons,
	}
	p.refreshMatches()
	return p
}

func (p *CommandPalette) Visible() bool { return p.visible }

func (p *CommandPalette) SetCommands(cmds []commands.Command) {
	p.commands = cmds
	p.refreshMatches()
}

func (p *CommandPalette) Show() {
	p.visible = true
	p.selected = 0
	p.refreshMatches()
}

func (p *CommandPalette) Hide() {
	p.visible = false
}

func (p *CommandPalette) SetFilter(filter string) {
	p.filter = filter
	p.selected = 0
	p.refreshMatches()
}

func (p *CommandPalette) MoveSelection(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.selected = (p.selected + delta + len(p.matches)) % len(p.matches)
}

func (p *CommandPalette) SelectedCommand() *commands.Command {
	if len(p.matches) == 0 {
		return nil
	}
	cmd := p.matches[p.selected].command
	return &cmd
}

func (p *CommandPalette) refreshMatches() {
	filter := strings.ToLower(strings.TrimSpace(p.filter))
	matches := make([]commandMatch, 0, len(p.commands))

	for _, cmd := range p.commands {
		match, ok := buildMatch(cmd, filter)
		if ok {
			matches = append(matches, match)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		// If no filter (empty search), sort by priority then name
		if filter == "" {
			if matches[i].command.Priority != matches[j].command.Priority {
				return matches[i].command.Priority < matches[j].command.Priority
			}
			return matches[i].command.Name < matches[j].command.Name
		}
		// When filtering, sort by match score then priority
		if matches[i].score == matches[j].score {
			if matches[i].command.Priority != matches[j].command.Priority {
				return matches[i].command.Priority < matches[j].command.Priority
			}
			return matches[i].command.Name < matches[j].command.Name
		}
		return matches[i].score < matches[j].score
	})

	if len(matches) > p.maxItems {
		matches = matches[:p.maxItems]
	}
	p.matches = matches
	if p.selected >= len(p.matches) {
		p.selected = len(p.matches) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}
}

func buildMatch(cmd commands.Command, filter string) (commandMatch, bool) {
	match := commandMatch{command: cmd}
	if filter == "" {
		match.highlightedName = cmd.Name
		match.highlightedDescription = cmd.Description
		return match, true
	}

	lname := strings.ToLower(cmd.Name)
	ldesc := strings.ToLower(cmd.Description)

	indexName := strings.Index(lname, filter)
	indexDesc := strings.Index(ldesc, filter)

	if indexName == -1 && indexDesc == -1 {
		// allow loose sequential match across name
		if !hasSequentialMatch(lname, filter) && !hasSequentialMatch(ldesc, filter) {
			return commandMatch{}, false
		}
	}

	match.highlightedName = highlightSegment(cmd.Name, indexName, len(filter))
	match.highlightedDescription = highlightSegment(cmd.Description, indexDesc, len(filter))

	bestIndex := indexName
	if bestIndex == -1 || (indexDesc != -1 && indexDesc < bestIndex) {
		bestIndex = indexDesc
	}
	if bestIndex == -1 {
		bestIndex = sequentialIndex(lname, filter)
	}
	if bestIndex == -1 {
		bestIndex = sequentialIndex(ldesc, filter)
	}
	match.score = bestIndex
	if match.score < 0 {
		match.score = len(cmd.Name) + len(cmd.Description)
	}

	return match, true
}

func hasSequentialMatch(source, filter string) bool {
	_, ok := sequentialMatch(source, filter)
	return ok
}

func sequentialMatch(source, filter string) ([]int, bool) {
	if filter == "" {
		return nil, true
	}

	positions := make([]int, 0, len(filter))
	searchIdx := 0
	for _, r := range filter {
		found := false
		for searchIdx < len(source) {
			if source[searchIdx] == byte(r) {
				positions = append(positions, searchIdx)
				searchIdx++
				found = true
				break
			}
			searchIdx++
		}
		if !found {
			return nil, false
		}
	}
	return positions, true
}

func sequentialIndex(source, filter string) int {
	positions, ok := sequentialMatch(source, filter)
	if !ok || len(positions) == 0 {
		return -1
	}
	return positions[0]
}

func highlightSegment(text string, start, length int) string {
	if start < 0 || length <= 0 {
		return text
	}
	if start+length > len(text) {
		length = len(text) - start
	}
	prefix := text[:start]
	segment := text[start : start+length]
	suffix := text[start+length:]
	return prefix + highlightStyle.Render(segment) + suffix
}

var highlightStyle = lipgloss.NewStyle().Bold(true)

const paletteMaxVisible = 9

func (p *CommandPalette) ViewInline(width int) string {
	if !p.visible || len(p.matches) == 0 {
		return ""
	}
	if width <= 0 {
		width = 40
	}

	// Compute a sliding window so the selected item is always visible.
	total := len(p.matches)
	windowSize := paletteMaxVisible
	if windowSize > total {
		windowSize = total
	}
	start := p.selected - windowSize + 1
	if start < 0 {
		start = 0
	}
	end := start + windowSize
	if end > total {
		end = total
		start = end - windowSize
		if start < 0 {
			start = 0
		}
	}

	content := strings.Builder{}
	for i := start; i < end; i++ {
		match := p.matches[i]
		name := fmt.Sprintf("/%s", match.highlightedName)
		style := p.styles.Item
		if i == p.selected {
			style = p.styles.SelectedItem
		}
		line := style.Render(name)
		content.WriteString(line)
		if i < end-1 {
			content.WriteString("\n")
		}
	}
	box := p.styles.Container.Width(width).Render(content.String())
	return box
}

type commandPaletteStyles struct {
	Container    lipgloss.Style
	Title        lipgloss.Style
	Filter       lipgloss.Style
	Item         lipgloss.Style
	SelectedItem lipgloss.Style
	Description  lipgloss.Style
	Hint         lipgloss.Style
	Empty        lipgloss.Style
}
