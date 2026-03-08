package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/liampierc3/shinobi-cli/internal/config"
	"github.com/liampierc3/shinobi-cli/internal/llm"
)

const (
	maxToolRunnerSteps              = 10
	defaultToolRunnerPlannerTimeout = 120 * time.Second
	toolRunnerPreviewLimit          = 600
)

const toolRunnerSystemPrompt = `You decide if tools are needed for the user's message.

TOOLS AVAILABLE:
- fs_read: Read files from the filesystem (preferred for personal notes and known paths)
- fs_write: Stage file writes for explicit user approval
- bash_exec: Execute bash commands (only when path is truly unknown)
- web_search: Search the public web for current/external information

You may explain your reasoning if helpful, then provide your decision in one of these formats:

1. If a NEW tool is needed, include this JSON anywhere in your response:
{
  "action": "fs_read",
  "input": "README.md",
  "reason": "User asked to read the README"
}

2. If you already gathered the needed information, include:
ACTION: RESPOND

3. If no tool is needed at all, include:
ACTION: none

CRITICAL RULES:
- Look at "Tool results so far" - if you already ran a tool and got results, use ACTION: RESPOND
- Do NOT run the same tool twice
- If user asks to create/write/edit/save a file, request approval first (ACTION: request_approval) and do not claim it is done
- If user asks for current prices / latest info / real-time data → you MUST use web_search (unless it's clearly about local files)
- If user asks to search the web or uses "search" without local-file context → use web_search
- If the user is asking about whether you searched or tool behavior → use ACTION: RESPOND
- If user asks to read a file AND the filesystem map below shows the path → use fs_read with the full absolute path. Do NOT use bash_exec to search for it first.
- If user asks to read files and no map is available → use fs_read if path is obvious, bash_exec only if path is truly unknown
- Always include an ACTION line if you are not returning JSON
- Do NOT wrap your reply in <think> or <analysis> tags
- Your response must contain either valid JSON or an ACTION directive`

type toolRunnerAction int

const (
	toolActionUnknown toolRunnerAction = iota
	toolActionPlan
	toolActionRespond
	toolActionNone
	toolActionRequestApproval
)

type toolRunnerDecision struct {
	kind     toolRunnerAction
	plan     *toolPlan
	note     string
	approval *toolWriteProposal
}

type toolPlan struct {
	Action   string          `json:"action"`
	InputRaw json.RawMessage `json:"input"`
	Reason   string          `json:"reason"`

	// Input is derived from InputRaw and used by tool executors that require
	// string arguments.
	Input string `json:"-"`
}

type toolWriteProposal struct {
	Target  string
	Content string
}

type toolResult struct {
	Tool  string
	Lines []string
	Error string
}

type toolRunnerTurn struct {
	userContent string
	events      []toolResult
}

func (m *Model) runToolRunnerTurn(userContent string) error {
	trimmed := strings.TrimSpace(userContent)
	m.toolRunnerEvents = nil
	m.toolRunnerGuidance = ""
	if trimmed == "" || m.llmClients == nil || len(m.llmClients) == 0 {
		return nil
	}

	turn := toolRunnerTurn{userContent: trimmed}

	for step := 0; step < maxToolRunnerSteps; step++ {
		decision, err := m.invokeToolRunner(turn)
		if err != nil {
			m.toolRunnerLastError = err.Error()
			return err
		}

		if forced := forceFilesystemDecision(m, turn, decision); forced != nil {
			decision = *forced
		}
		if forced := forceWebSearchDecision(turn, decision); forced != nil {
			decision = *forced
		}

		switch decision.kind {
		case toolActionNone:
			m.toolRunnerEvents = cloneToolResults(turn.events)
			m.toolRunnerGuidance = ""
			m.toolRunnerLastAction = "none"
			return nil
		case toolActionRespond:
			m.toolRunnerEvents = cloneToolResults(turn.events)
			m.toolRunnerGuidance = decision.note
			m.toolRunnerLastAction = "respond"
			return nil
		case toolActionRequestApproval:
			writeProposal := mergeWriteProposal(turn.userContent, decision.approval)
			note := strings.TrimSpace(decision.note)
			if note == "" {
				note = "Write approval required. I have not created or modified any files yet."
			}
			m.toolRunnerEvents = cloneToolResults(turn.events)
			m.toolRunnerGuidance = note
			m.toolRunnerPendingApproval = note
			m.toolRunnerPendingWritePath = strings.TrimSpace(writeProposal.Target)
			m.beginToolApprovalPrompt(note, writeProposal.Target, writeProposal.Content)
			m.toolRunnerLastAction = "request_approval"
			return nil
		case toolActionPlan:
			if decision.plan == nil {
				m.toolRunnerLastError = "tool plan missing"
				return errors.New("tool plan missing")
			}
			if strings.EqualFold(strings.TrimSpace(decision.plan.Action), "bash_exec") {
				if prev, ok := findPriorBashCommandEvent(turn.events, decision.plan.Input); ok {
					m.toolRunnerEvents = cloneToolResults(turn.events)
					if isBlockedBashEvent(prev) {
						m.toolRunnerGuidance = "The requested command was already blocked and not executed. Explain the block and suggest running it manually if needed."
					} else {
						m.toolRunnerGuidance = "The requested command already ran in this turn. Reuse the prior result instead of rerunning it."
					}
					m.toolRunnerLastAction = "respond"
					return nil
				}
			}
			if strings.EqualFold(strings.TrimSpace(decision.plan.Action), "web_search") {
				if hasToolResult(turn.events, "web_search") {
					m.toolRunnerEvents = cloneToolResults(turn.events)
					m.toolRunnerGuidance = "Web search already ran this turn. Use the results above to answer the question."
					m.toolRunnerLastAction = "respond"
					return nil
				}
			}
			evt := m.executeToolPlan(*decision.plan)
			turn.events = append(turn.events, evt)
			m.appendToolResult(evt)
			m.toolRunnerEvents = cloneToolResults(turn.events)
			if evt.Error != "" {
				m.toolRunnerLastError = evt.Error
				if strings.EqualFold(strings.TrimSpace(evt.Tool), "bash_exec") && strings.Contains(strings.ToLower(evt.Error), "blocked") {
					m.toolRunnerGuidance = "The requested command was blocked for safety and was not executed. Explain that it was blocked; do not guess command output."
					m.toolRunnerLastAction = "respond"
					return nil
				}
			}
			continue
		default:
			m.toolRunnerLastError = "unknown tool runner action"
			return fmt.Errorf("unknown tool runner action: %v", decision.kind)
		}
	}

	return fmt.Errorf("tool runner exceeded %d steps", maxToolRunnerSteps)
}

func (m *Model) invokeToolRunner(turn toolRunnerTurn) (toolRunnerDecision, error) {
	if m.llmClients == nil || len(m.llmClients) == 0 {
		return toolRunnerDecision{kind: toolActionNone}, nil
	}
	timeout := m.toolRunnerTimeout()
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	prompt := buildToolRunnerPrompt(turn, m.toolRunnerPendingApproval)

	client := m.llmClients[config.BackendLMStudio]
	if client == nil {
		return toolRunnerDecision{kind: toolActionNone}, nil
	}
	modelID, err := m.resolveModelForRequest(ctx, client)
	if err != nil {
		return toolRunnerDecision{}, err
	}

	systemPrompt := toolRunnerSystemPrompt
	if len(m.contextPaths) > 0 {
		if fsMap := buildFilesystemMap(m.contextPaths); fsMap != "" {
			systemPrompt += "\n\n" + fsMap
		}
	}

	respContent, err := client.Chat(ctx, llm.ChatRequest{
		Model: modelID,
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return toolRunnerDecision{}, err
	}

	decision, err := parseToolRunnerDecision(respContent)
	if err != nil {
		if strings.Contains(err.Error(), "no valid JSON or ACTION directive") {
			return toolRunnerDecision{kind: toolActionNone}, nil
		}
		if strings.Contains(err.Error(), "tool plan missing action field") {
			return toolRunnerDecision{kind: toolActionNone}, nil
		}
		return toolRunnerDecision{}, err
	}

	return decision, nil
}

func (m *Model) toolRunnerTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("SHINOBI_TOOLRUNNER_TIMEOUT"))
	if raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil {
			if secs <= 0 {
				return 0
			}
			return time.Duration(secs) * time.Second
		}
	}

	timeout := defaultToolRunnerPlannerTimeout
	if m != nil {
		if req := m.requestTimeout(); req > 0 && req < timeout {
			timeout = req
		}
	}
	return timeout
}

func buildToolRunnerPrompt(turn toolRunnerTurn, pendingApproval string) string {
	var b strings.Builder
	b.WriteString("Latest user message:\n")
	b.WriteString(turn.userContent)
	b.WriteString("\n\nTool results so far:\n")
	if len(turn.events) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, evt := range turn.events {
			b.WriteString(formatToolResultBlock(evt))
			b.WriteString("\n\n")
		}
	}
	if strings.TrimSpace(pendingApproval) != "" {
		b.WriteString("Pending approval:\n")
		b.WriteString(strings.TrimSpace(pendingApproval))
		b.WriteString("\n\n")
	}
	b.WriteString("Remember to reply with either a JSON plan or ACTION directive.")
	return b.String()
}

func (p *toolPlan) UnmarshalJSON(data []byte) error {
	var raw struct {
		Action string          `json:"action"`
		Input  json.RawMessage `json:"input"`
		Reason string          `json:"reason"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Action = strings.TrimSpace(raw.Action)
	p.InputRaw = append([]byte(nil), raw.Input...)
	p.Reason = strings.TrimSpace(raw.Reason)
	p.Input = coerceToolPlanInput(raw.Input)
	return nil
}

func coerceToolPlanInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	var asObject map[string]interface{}
	if err := json.Unmarshal(raw, &asObject); err == nil {
		for _, key := range []string{
			"query",
			"q",
			"command",
			"cmd",
			"path",
			"file",
			"filename",
			"target",
			"content",
			"text",
			"value",
			"input",
		} {
			if value := objectStringValue(asObject, key); value != "" {
				return value
			}
		}
		return ""
	}

	var asList []interface{}
	if err := json.Unmarshal(raw, &asList); err == nil {
		for _, item := range asList {
			if value := anyToString(item); value != "" {
				return value
			}
		}
	}

	return ""
}

func objectStringValue(raw map[string]interface{}, key string) string {
	if raw == nil {
		return ""
	}
	value, ok := raw[key]
	if !ok {
		return ""
	}
	return anyToString(value)
}

func anyToString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func parseToolRunnerDecision(raw string) (toolRunnerDecision, error) {
	rawTrimmed := strings.TrimSpace(raw)
	if rawTrimmed == "" {
		return toolRunnerDecision{}, errors.New("empty tool runner response")
	}

	// Parse GPT-OSS/Responses-style tool-call envelopes from the raw response
	// before any sanitization so we preserve metadata like "to=<tool>".
	if toolPlan, ok := parseToolRunnerToolCall(rawTrimmed); ok {
		return toolRunnerDecision{kind: toolActionPlan, plan: &toolPlan}, nil
	}
	if decision, ok := parseToolRunnerBareJSONDecision(rawTrimmed); ok {
		return decision, nil
	}

	// Re-run envelope/JSON parsing on delimiter/tag-sanitized text before
	// generic parsing so GPT-OSS responses don't collapse to empty.
	parsingText := strings.TrimSpace(stripToolRunnerTagsForParsing(rawTrimmed))
	if parsingText != "" {
		if toolPlan, ok := parseToolRunnerToolCall(parsingText); ok {
			return toolRunnerDecision{kind: toolActionPlan, plan: &toolPlan}, nil
		}
		if decision, ok := parseToolRunnerBareJSONDecision(parsingText); ok {
			return decision, nil
		}
	}

	trimmed := parsingText
	if trimmed == "" {
		return toolRunnerDecision{}, errors.New("empty tool runner response")
	}

	// Look for ACTION directives anywhere in the text (not just at the beginning)
	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "ACTION:") {
		// Find the ACTION line
		lines := strings.Split(trimmed, "\n")
		for i, line := range lines {
			lineUpper := strings.ToUpper(strings.TrimSpace(line))
			if strings.HasPrefix(lineUpper, "ACTION:") {
				action := strings.TrimSpace(line)
				if idx := strings.Index(strings.ToUpper(action), "ACTION:"); idx != -1 {
					action = strings.TrimSpace(action[idx+len("ACTION:"):])
				}
				action = strings.ToLower(strings.TrimSpace(action))

				// Collect any text after this line as context
				body := ""
				if i+1 < len(lines) {
					body = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
				}

				switch action {
				case "respond":
					return toolRunnerDecision{kind: toolActionRespond, note: body}, nil
				case "none":
					return toolRunnerDecision{kind: toolActionNone}, nil
				case "request_approval":
					return toolRunnerDecision{
						kind:     toolActionRequestApproval,
						note:     body,
						approval: parseWriteProposalFromFreeText(body),
					}, nil
				default:
					return toolRunnerDecision{}, fmt.Errorf("unknown ACTION directive: %s", action)
				}
			}
		}
	}

	// Look for JSON anywhere in the response
	// First, try to find JSON object by finding first { and last }
	jsonStart := strings.Index(trimmed, "{")
	jsonEnd := strings.LastIndex(trimmed, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonStart >= jsonEnd {
		// No valid JSON found
		preview := trimmed
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		return toolRunnerDecision{}, fmt.Errorf("no valid JSON or ACTION directive found. Got: %s", preview)
	}

	jsonContent := trimmed[jsonStart : jsonEnd+1]

	// Try to parse as JSON
	var plan toolPlan
	decoder := json.NewDecoder(strings.NewReader(jsonContent))
	if err := decoder.Decode(&plan); err != nil {
		// If JSON parsing fails, give helpful error
		preview := trimmed
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		return toolRunnerDecision{}, fmt.Errorf("failed to parse tool plan JSON. Got: %s", preview)
	}

	if plan.Action == "" {
		if fallback := applyToolPlanFallbacks(jsonContent); fallback.Action != "" {
			return toolRunnerDecision{kind: toolActionPlan, plan: &fallback}, nil
		}
		return toolRunnerDecision{}, errors.New("tool plan missing action field")
	}

	// Handle special actions that models sometimes output as JSON
	normalizeAction := func(value string) string {
		val := strings.ToLower(strings.TrimSpace(value))
		if strings.HasPrefix(val, "action:") {
			val = strings.TrimSpace(strings.TrimPrefix(val, "action:"))
		} else if strings.HasPrefix(val, "action ") {
			val = strings.TrimSpace(strings.TrimPrefix(val, "action "))
		}
		return val
	}

	action := normalizeAction(plan.Action)
	switch action {
	case "none", "no_tool", "skip":
		return toolRunnerDecision{kind: toolActionNone}, nil
	case "respond", "answer", "reply":
		return toolRunnerDecision{kind: toolActionRespond, note: plan.Reason}, nil
	case "request_approval":
		return toolRunnerDecision{
			kind:     toolActionRequestApproval,
			note:     plan.Reason,
			approval: parseWriteProposalFromPlan(&plan),
		}, nil
	case "action":
		inferred := normalizeAction(plan.Input)
		if inferred == "" {
			inferred = normalizeAction(plan.Reason)
		}
		switch inferred {
		case "none", "no_tool", "skip":
			return toolRunnerDecision{kind: toolActionNone}, nil
		case "respond", "answer", "reply":
			return toolRunnerDecision{kind: toolActionRespond, note: plan.Reason}, nil
		case "request_approval":
			return toolRunnerDecision{
				kind:     toolActionRequestApproval,
				note:     plan.Reason,
				approval: parseWriteProposalFromPlan(&plan),
			}, nil
		}
		if inferred == "" {
			return toolRunnerDecision{kind: toolActionNone}, nil
		}
		if normalized := normalizeToolRunnerTool(inferred); normalized != "" {
			if strings.TrimSpace(plan.Input) == "" {
				return toolRunnerDecision{kind: toolActionNone}, nil
			}
			plan.Action = normalized
			return toolRunnerDecision{kind: toolActionPlan, plan: &plan}, nil
		}
		return toolRunnerDecision{kind: toolActionNone}, nil
	}

	if normalized := normalizeToolRunnerTool(action); normalized != "" {
		plan.Action = normalized
		if strings.TrimSpace(plan.Input) == "" {
			return toolRunnerDecision{kind: toolActionNone}, nil
		}
		return toolRunnerDecision{kind: toolActionPlan, plan: &plan}, nil
	}

	if strings.TrimSpace(plan.Input) == "" {
		return toolRunnerDecision{kind: toolActionNone}, nil
	}

	return toolRunnerDecision{kind: toolActionPlan, plan: &plan}, nil
}

func parseToolRunnerBareJSONDecision(raw string) (toolRunnerDecision, bool) {
	trimmed := strings.TrimSpace(raw)
	if !isBareToolCallJSON(trimmed) {
		return toolRunnerDecision{}, false
	}

	var plan toolPlan
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	if err := decoder.Decode(&plan); err != nil {
		plan = applyToolPlanFallbacks(trimmed)
	}
	if strings.TrimSpace(plan.Action) == "" {
		if fallback := applyToolPlanFallbacks(trimmed); fallback.Action != "" {
			plan = fallback
		}
	}
	if strings.TrimSpace(plan.Action) == "" {
		return toolRunnerDecision{}, false
	}

	normalizeAction := func(value string) string {
		val := strings.ToLower(strings.TrimSpace(value))
		if strings.HasPrefix(val, "action:") {
			val = strings.TrimSpace(strings.TrimPrefix(val, "action:"))
		} else if strings.HasPrefix(val, "action ") {
			val = strings.TrimSpace(strings.TrimPrefix(val, "action "))
		}
		return val
	}

	action := normalizeAction(plan.Action)
	switch action {
	case "none", "no_tool", "skip":
		return toolRunnerDecision{kind: toolActionNone}, true
	case "respond", "answer", "reply":
		return toolRunnerDecision{kind: toolActionRespond, note: plan.Reason}, true
	case "request_approval":
		return toolRunnerDecision{
			kind:     toolActionRequestApproval,
			note:     plan.Reason,
			approval: parseWriteProposalFromPlan(&plan),
		}, true
	case "action":
		inferred := normalizeAction(plan.Input)
		if inferred == "" {
			inferred = normalizeAction(plan.Reason)
		}
		if normalized := normalizeToolRunnerTool(inferred); normalized != "" {
			plan.Action = normalized
			if strings.TrimSpace(plan.Input) == "" {
				return toolRunnerDecision{kind: toolActionNone}, true
			}
			return toolRunnerDecision{kind: toolActionPlan, plan: &plan}, true
		}
		return toolRunnerDecision{kind: toolActionNone}, true
	}

	if normalized := normalizeToolRunnerTool(action); normalized != "" {
		plan.Action = normalized
		if strings.TrimSpace(plan.Input) == "" {
			return toolRunnerDecision{kind: toolActionNone}, true
		}
		return toolRunnerDecision{kind: toolActionPlan, plan: &plan}, true
	}

	if strings.TrimSpace(plan.Input) == "" {
		return toolRunnerDecision{kind: toolActionNone}, true
	}
	return toolRunnerDecision{kind: toolActionPlan, plan: &plan}, true
}

func (m *Model) executeToolPlan(plan toolPlan) toolResult {
	m.showSystemNotice(fmt.Sprintf("ToolRunner executing %s (%s)", plan.Action, strings.TrimSpace(plan.Reason)))
	switch strings.ToLower(strings.TrimSpace(plan.Action)) {
	case "fs_read":
		return m.runToolFSRead(plan.Input)
	case "bash_exec":
		if isDangerousBashCommand(plan.Input) {
			cmd := strings.TrimSpace(plan.Input)
			if cmd == "" {
				cmd = "<empty>"
			}
			return toolResult{
				Tool:  "bash_exec",
				Lines: []string{fmt.Sprintf("Blocked command: %s", cmd), "Approval required. Run it manually with !<command> or /exec."},
				Error: "ToolRunner blocked a potentially destructive command.",
			}
		}
		return m.runToolBashExec(plan.Input)
	case "web_search":
		return m.runToolWebSearch(plan.Input)
	case "fs_write":
		note := fmt.Sprintf("ToolRunner requested filesystem write: %s", plan.Reason)
		m.toolRunnerPendingApproval = note
		return toolResult{
			Tool:  "fs_write",
			Lines: []string{fmt.Sprintf("Requested write target: %s", strings.TrimSpace(plan.Input)), note},
			Error: "Filesystem writes require explicit user approval (/fs write ... then /fs apply).",
		}
	default:
		return toolResult{
			Tool:  plan.Action,
			Lines: []string{fmt.Sprintf("Unsupported tool action %q", plan.Action)},
			Error: "ToolRunner attempted an unknown action.",
		}
	}
}

func (m *Model) runToolWebSearch(query string) toolResult {
	summary := toolResult{Tool: "web_search"}
	q := strings.TrimSpace(query)
	if q == "" {
		summary.Error = "Missing search query"
		summary.Lines = []string{"Provide a search query string."}
		return summary
	}
	if !m.webSearchConfigured() {
		summary.Error = "Web search is not configured; set SHINOBI_ENABLE_WEB_SEARCH=1"
		summary.Lines = []string{summary.Error}
		return summary
	}

	summary.Lines = append(summary.Lines, fmt.Sprintf("Searching web for %q", q))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := m.searchClient.Search(ctx, q, 5)
	if err != nil {
		summary.Error = err.Error()
		summary.Lines = append(summary.Lines, fmt.Sprintf("Search failed: %s", err.Error()))
		return summary
	}
	if len(results) == 0 {
		summary.Lines = append(summary.Lines, "No results found.")
		return summary
	}

	summary.Lines = append(summary.Lines, fmt.Sprintf("Found %d result(s)", len(results)))
	for i, result := range results {
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = "(untitled)"
		}
		url := strings.TrimSpace(result.URL)
		snippet := strings.TrimSpace(result.Snippet)
		if snippet != "" {
			snippet = truncateText(condenseSnippet(snippet), 300)
		}

		parts := []string{fmt.Sprintf("Result %d: %s", i+1, title)}
		if url != "" {
			parts = append(parts, fmt.Sprintf("URL: %s", url))
		}
		if snippet != "" {
			parts = append(parts, fmt.Sprintf("Snippet: %s", snippet))
		}
		summary.Lines = append(summary.Lines, strings.Join(parts, "\n"))
	}
	return summary
}

func (m *Model) runToolFSRead(path string) toolResult {
	summary := toolResult{Tool: "fs_read"}
	target := strings.TrimSpace(path)
	if target == "" {
		summary.Error = "Missing filesystem path"
		summary.Lines = []string{"Provide a relative path within the workspace."}
		return summary
	}
	abs, err := m.resolveFSPath(target)
	if err != nil {
		summary.Error = err.Error()
		summary.Lines = []string{fmt.Sprintf("Unable to resolve %s", target)}
		return summary
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		summary.Error = err.Error()
		summary.Lines = []string{fmt.Sprintf("Failed to read %s", target)}
		return summary
	}
	rel := m.relativeToRoot(abs)
	summary.Lines = append(summary.Lines, fmt.Sprintf("Read %s (%d bytes)", rel, len(data)))
	preview := truncateText(condenseSnippet(string(data)), toolRunnerPreviewLimit)
	if preview != "" {
		summary.Lines = append(summary.Lines, fmt.Sprintf("Preview: %s", preview))
	}
	truncated := string(data)
	if len(truncated) > fsPreviewLimit {
		truncated = truncated[:fsPreviewLimit] + "\n... (truncated)"
	}
	summary.Lines = append(summary.Lines, fmt.Sprintf("Full contents:\n```\n%s\n```", truncated))
	return summary
}

func (m *Model) runToolBashExec(command string) toolResult {
	summary := toolResult{Tool: "bash_exec"}
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		summary.Error = "Missing bash command"
		summary.Lines = []string{"Provide a bash command to execute."}
		return summary
	}

	summary.Lines = append(summary.Lines, fmt.Sprintf("Executing: %s", cmd))

	// Execute command with bash
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	execCmd := exec.CommandContext(ctx, "bash", "-c", cmd)
	// Set working directory to fsRoot if available
	if m.fsRoot != "" {
		execCmd.Dir = m.fsRoot
	}

	output, err := execCmd.CombinedOutput()
	if err != nil {
		summary.Error = err.Error()
		summary.Lines = append(summary.Lines, fmt.Sprintf("Command failed: %s", err.Error()))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr != "" {
		truncated := truncateText(outputStr, 2000)
		summary.Lines = append(summary.Lines, fmt.Sprintf("Output:\n```\n%s\n```", truncated))
	} else {
		summary.Lines = append(summary.Lines, "(no output)")
	}

	return summary
}

func formatToolResultBlock(evt toolResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("TOOL_RESULT(%s):\n", evt.Tool))
	for _, line := range evt.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(trimmed)
		b.WriteString("\n")
	}
	if evt.Error != "" {
		b.WriteString("- Error: ")
		b.WriteString(strings.TrimSpace(evt.Error))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func formatToolIndicator(evt toolResult) string {
	header := toolIndicatorHeader(evt)
	if header == "" {
		return ""
	}
	lines := toolIndicatorOutput(evt)
	var b strings.Builder
	b.WriteString("⏺ ")
	b.WriteString(header)
	if len(lines) == 0 {
		return b.String()
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString("  ⎿ ")
		b.WriteString(line)
	}
	return b.String()
}

func toolIndicatorHeader(evt toolResult) string {
	switch strings.ToLower(strings.TrimSpace(evt.Tool)) {
	case "bash_exec":
		if cmd := extractLinePrefix(evt.Lines, "Executing:"); cmd != "" {
			return fmt.Sprintf("Bash(%s)", quoteToolArg(cmd))
		}
		return "Bash"
	case "fs_read":
		if info := extractLinePrefix(evt.Lines, "Read "); info != "" {
			return fmt.Sprintf("Read(%s)", quoteToolArg(info))
		}
		return "Read"
	case "fs_write":
		if info := extractLinePrefix(evt.Lines, "Requested write target:"); info != "" {
			return fmt.Sprintf("Write(%s)", quoteToolArg(info))
		}
		return "Write"
	case "web_search":
		if info := extractLinePrefix(evt.Lines, "Searching web for"); info != "" {
			return fmt.Sprintf("Web Search(%s)", quoteToolArg(strings.Trim(info, "\"")))
		}
		return "Web Search"
	default:
		if evt.Tool != "" {
			return evt.Tool
		}
	}
	return ""
}

func toolIndicatorOutput(evt toolResult) []string {
	lines := make([]string, 0, 8)
	for _, line := range evt.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Executing:") || strings.HasPrefix(trimmed, "Read ") || strings.HasPrefix(trimmed, "Requested write target:") {
			continue
		}
		if strings.HasPrefix(trimmed, "Output:") {
			output := strings.TrimSpace(strings.TrimPrefix(trimmed, "Output:"))
			if fenced := extractCodeFence(output); fenced != "" {
				lines = append(lines, splitLimitedLines(fenced, 6)...)
				continue
			}
			lines = append(lines, splitLimitedLines(output, 6)...)
			continue
		}
		if strings.Contains(trimmed, "```") {
			if fenced := extractCodeFence(trimmed); fenced != "" {
				lines = append(lines, splitLimitedLines(fenced, 6)...)
				continue
			}
		}
		lines = append(lines, splitLimitedLines(trimmed, 6)...)
		if len(lines) >= 6 {
			break
		}
	}
	if strings.TrimSpace(evt.Error) != "" {
		lines = append(lines, "Error: "+strings.TrimSpace(evt.Error))
	}
	if len(lines) > 6 {
		lines = lines[:6]
	}
	return lines
}

func extractLinePrefix(lines []string, prefix string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return ""
}

func extractCodeFence(input string) string {
	start := strings.Index(input, "```")
	if start == -1 {
		return ""
	}
	rest := input[start+3:]
	end := strings.Index(rest, "```")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func splitLimitedLines(input string, limit int) []string {
	if input == "" {
		return nil
	}
	parts := strings.Split(strings.ReplaceAll(input, "\r", ""), "\n")
	if limit > 0 && len(parts) > limit {
		parts = parts[:limit]
	}
	return parts
}

func quoteToolArg(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "''"
	}
	if strings.ContainsAny(trimmed, " \t\n") {
		return fmt.Sprintf("%q", trimmed)
	}
	return trimmed
}

func cloneToolResults(events []toolResult) []toolResult {
	if len(events) == 0 {
		return nil
	}
	cloned := make([]toolResult, len(events))
	for i, evt := range events {
		lines := make([]string, len(evt.Lines))
		copy(lines, evt.Lines)
		cloned[i] = toolResult{Tool: evt.Tool, Lines: lines, Error: evt.Error}
	}
	return cloned
}

func condenseSnippet(input string) string {
	cleaned := strings.TrimSpace(input)
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")
	cleaned = strings.ReplaceAll(cleaned, "\t", " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return truncateText(cleaned, toolRunnerPreviewLimit)
}

func truncateText(input string, limit int) string {
	if limit <= 0 || len(input) <= limit {
		return input
	}
	if limit < 3 {
		return input[:limit]
	}
	return input[:limit-3] + "..."
}

func (m *Model) appendToolResult(evt toolResult) {
	indicator := formatToolIndicator(evt)
	if indicator == "" {
		return
	}
	m.appendMessage(NewUIOnlySystemMessage(indicator))
	m.updateViewportContent()
	if !m.scrolledUp {
		m.viewportGotoBottom()
	}
}

func (m *Model) toolSummarySystemPrompt() string {
	if len(m.toolRunnerEvents) == 0 && strings.TrimSpace(m.toolRunnerGuidance) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("ToolRunner Summary:\n")
	b.WriteString("Base your answer on the tool outputs below. If they are insufficient, explain what else is required instead of guessing.\n")
	b.WriteString("Do not claim any file was created, modified, or deleted unless a tool result below explicitly confirms it.\n")
	if latestBlockedBashCommand(m.toolRunnerEvents) != "" {
		b.WriteString("If a command was blocked, explicitly state it was blocked and not executed. Do not guess or fabricate command output.\n")
	}
	if strings.TrimSpace(m.toolRunnerGuidance) != "" {
		b.WriteString("ToolRunner note: ")
		b.WriteString(strings.TrimSpace(m.toolRunnerGuidance))
		b.WriteString("\n")
	}
	for _, evt := range m.toolRunnerEvents {
		b.WriteString("\n")
		b.WriteString(formatToolResultBlock(evt))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (m *Model) clearToolRunnerContext() {
	m.toolRunnerEvents = nil
	m.toolRunnerGuidance = ""
	m.toolRunnerPendingWritePath = ""
}

func forceWebSearchDecision(turn toolRunnerTurn, decision toolRunnerDecision) *toolRunnerDecision {
	if decision.kind == toolActionPlan {
		return nil
	}
	if len(turn.events) > 0 && hasToolResult(turn.events, "web_search") {
		return nil
	}
	if !shouldForceWebSearch(turn.userContent) {
		return nil
	}
	plan := toolPlan{
		Action: "web_search",
		Input:  strings.TrimSpace(turn.userContent),
		Reason: "Query appears time-sensitive or explicitly requests web lookup.",
	}
	return &toolRunnerDecision{kind: toolActionPlan, plan: &plan}
}

func forceFilesystemDecision(m *Model, turn toolRunnerTurn, decision toolRunnerDecision) *toolRunnerDecision {
	if m == nil {
		return nil
	}
	if decision.kind == toolActionPlan || decision.kind == toolActionRequestApproval {
		return nil
	}
	if len(turn.events) > 0 && (hasToolResult(turn.events, "fs_read") || hasToolResult(turn.events, "bash_exec")) {
		return nil
	}

	input := strings.TrimSpace(turn.userContent)
	if input == "" {
		return nil
	}
	if shouldForceFilesystemWriteApproval(input) {
		target := inferWriteTargetPath(input)
		note := "Write approval required. I have not created or modified any files yet."
		if target != "" {
			note = fmt.Sprintf("Write approval required for %s. I have not created or modified any files yet.", target)
		}
		note += " Wait for explicit user approval before writing."
		return &toolRunnerDecision{kind: toolActionRequestApproval, note: note}
	}
	if !barePathAutoReadEnabled() {
		return nil
	}
	if strings.ContainsAny(input, " \t\n") {
		return nil
	}
	lower := strings.ToLower(input)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return nil
	}

	abs, err := m.resolveFSPath(input)
	if err != nil {
		return nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	rel := strings.TrimSpace(m.relativeToRoot(abs))
	if rel == "" {
		rel = "."
	}

	if info.IsDir() {
		cmd := fmt.Sprintf("ls -la %s", toolRunnerShellQuote(rel))
		plan := toolPlan{
			Action: "bash_exec",
			Input:  cmd,
			Reason: "User provided a directory path; list its contents.",
		}
		return &toolRunnerDecision{kind: toolActionPlan, plan: &plan}
	}

	plan := toolPlan{
		Action: "fs_read",
		Input:  rel,
		Reason: "User provided a file path; read it before responding.",
	}
	return &toolRunnerDecision{kind: toolActionPlan, plan: &plan}
}

func shouldForceWebSearch(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	if isSearchMetaQuestion(q) {
		return false
	}
	if isLocalCommandQuery(q) {
		return false
	}
	words := strings.Fields(q)
	if isAckLikeMessage(q, len(words)) {
		return false
	}

	if strings.Contains(q, "search the web") || strings.Contains(q, "search web") || strings.Contains(q, "look it up online") || strings.Contains(q, "browse the web") {
		return true
	}

	strongPhrases := []string{
		"current price",
		"latest price",
		"price of",
		"market cap",
		"exchange rate",
		"stock price",
		"latest news",
		"breaking news",
		"weather forecast",
		"current weather",
	}
	for _, phrase := range strongPhrases {
		if strings.Contains(q, phrase) {
			return true
		}
	}

	if !(strings.Contains(q, "latest") || strings.Contains(q, "current") || strings.Contains(q, "real-time") || strings.Contains(q, "right now") || strings.Contains(q, "today")) {
		return false
	}
	return containsTimeSensitiveTopic(q)
}

func containsTimeSensitiveTopic(q string) bool {
	topics := []string{
		"price",
		"market cap",
		"exchange rate",
		"stock",
		"btc",
		"bitcoin",
		"eth",
		"ethereum",
		"weather",
		"forecast",
		"news",
		"headline",
		"score",
		"scores",
	}
	for _, topic := range topics {
		if strings.Contains(q, topic) {
			return true
		}
	}
	return false
}

func toolRunnerShellQuote(input string) string {
	if input == "" {
		return "''"
	}
	if !strings.ContainsAny(input, " \t\n'\"\\$`") {
		return input
	}
	return "'" + strings.ReplaceAll(input, "'", `'\''`) + "'"
}

func isSearchMetaQuestion(q string) bool {
	metaPhrases := []string{
		"are you searching",
		"did you search",
		"did you run a web search",
		"did you actually run a web search",
		"did you use web search",
		"actually searching",
		"searching or no",
		"did you use the web",
		"did you browse",
		"are you browsing",
		"did you run search",
		"did you actually search",
	}
	for _, phrase := range metaPhrases {
		if strings.Contains(q, phrase) {
			return true
		}
	}
	if strings.Contains(q, "web search") && (strings.Contains(q, "did you") || strings.Contains(q, "are you") || strings.Contains(q, "have you")) {
		return true
	}
	return false
}

func isLocalCommandQuery(q string) bool {
	if strings.Contains(q, "http://") || strings.Contains(q, "https://") {
		return false
	}
	if strings.Contains(q, "/") || strings.Contains(q, "\\") {
		return true
	}
	localKeywords := []string{
		"file",
		"files",
		"folder",
		"folders",
		"dir",
		"directory",
		"path",
		"repo",
		"read",
		"open",
		"show",
		"list",
		"ls",
		"find",
		"grep",
		"rg",
		"cat",
		"chmod",
		"chown",
	}
	for _, kw := range localKeywords {
		if strings.Contains(q, kw) {
			return true
		}
	}
	return false
}

func shouldForceFilesystemWriteApproval(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	writeKeywords := []string{
		"create file",
		"write file",
		"save file",
		"edit file",
		"update file",
		"append file",
		"new file",
		"touch ",
		" >",
		">>",
	}
	for _, kw := range writeKeywords {
		if strings.Contains(q, kw) {
			return true
		}
	}
	return false
}

func inferWriteTargetPath(query string) string {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 0 {
		return ""
	}
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == ">" || fields[i] == ">>" {
			return strings.Trim(fields[i+1], "\"'`.,")
		}
	}
	for _, field := range fields {
		candidate := strings.Trim(field, "\"'`.,")
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, "/") || strings.Contains(candidate, ".") {
			return candidate
		}
	}
	return ""
}

func inferWriteContent(query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	markers := []string{
		"with content",
		"with the content",
		"content:",
		"contents:",
		"text:",
		"saying",
		"that says",
	}
	for _, marker := range markers {
		idx := strings.Index(lower, marker)
		if idx == -1 {
			continue
		}
		segment := strings.TrimSpace(trimmed[idx+len(marker):])
		segmentLower := strings.ToLower(segment)
		for _, prefix := range []string{"is ", "of ", "to be "} {
			if strings.HasPrefix(segmentLower, prefix) {
				segment = strings.TrimSpace(segment[len(prefix):])
				segmentLower = strings.ToLower(segment)
			}
		}
		if quoted := extractLastQuotedSegment(segment); quoted != "" {
			return quoted
		}
		return strings.Trim(segment, "\"'`")
	}

	return extractLastQuotedSegment(trimmed)
}

func extractLastQuotedSegment(input string) string {
	last := ""
	for i := 0; i < len(input); i++ {
		quote := input[i]
		if quote != '"' && quote != '\'' && quote != '`' {
			continue
		}
		for j := i + 1; j < len(input); j++ {
			if input[j] != quote {
				continue
			}
			candidate := strings.TrimSpace(input[i+1 : j])
			if candidate != "" {
				last = candidate
			}
			i = j
			break
		}
	}
	return last
}

func parseWriteProposalFromPlan(plan *toolPlan) *toolWriteProposal {
	if plan == nil {
		return nil
	}

	proposal := &toolWriteProposal{}
	if fromInput := parseWriteProposalFromRawInput(plan.InputRaw); fromInput != nil {
		proposal.Target = fromInput.Target
		proposal.Content = fromInput.Content
	}

	if proposal.Target == "" {
		proposal.Target = strings.TrimSpace(plan.Input)
	}
	if proposal.Content == "" {
		proposal.Content = inferWriteContent(plan.Reason)
	}

	if strings.TrimSpace(proposal.Target) == "" && strings.TrimSpace(proposal.Content) == "" {
		return nil
	}
	proposal.Target = strings.TrimSpace(proposal.Target)
	return proposal
}

func parseWriteProposalFromRawInput(raw json.RawMessage) *toolWriteProposal {
	if len(raw) == 0 {
		return nil
	}

	var asObject map[string]interface{}
	if err := json.Unmarshal(raw, &asObject); err != nil {
		return nil
	}

	target := ""
	for _, key := range []string{"filename", "file", "path", "target"} {
		if value := objectStringValue(asObject, key); value != "" {
			target = value
			break
		}
	}
	content := ""
	for _, key := range []string{"content", "text", "body", "data", "value"} {
		if value := objectStringValue(asObject, key); value != "" {
			content = value
			break
		}
	}

	if strings.TrimSpace(target) == "" && strings.TrimSpace(content) == "" {
		return nil
	}
	return &toolWriteProposal{
		Target:  strings.TrimSpace(target),
		Content: content,
	}
}

func parseWriteProposalFromFreeText(text string) *toolWriteProposal {
	target := inferWriteTargetPath(text)
	content := inferWriteContent(text)
	if strings.TrimSpace(target) == "" && strings.TrimSpace(content) == "" {
		return nil
	}
	return &toolWriteProposal{
		Target:  strings.TrimSpace(target),
		Content: content,
	}
}

func mergeWriteProposal(userContent string, proposal *toolWriteProposal) toolWriteProposal {
	merged := toolWriteProposal{}
	if proposal != nil {
		merged.Target = strings.TrimSpace(proposal.Target)
		merged.Content = proposal.Content
	}
	merged.Target = normalizeWriteTarget(merged.Target, userContent)
	if merged.Target == "" {
		merged.Target = inferWriteTargetPath(userContent)
	}
	if strings.TrimSpace(merged.Content) == "" {
		merged.Content = inferWriteContent(userContent)
	}
	return merged
}

func normalizeWriteTarget(target, userContent string) string {
	candidate := strings.TrimSpace(strings.Trim(target, "\"'`"))
	if candidate == "" {
		return ""
	}
	if looksLikeWriteInstruction(candidate) {
		if inferred := inferWriteTargetPath(candidate); inferred != "" {
			return inferred
		}
		if inferred := inferWriteTargetPath(userContent); inferred != "" {
			return inferred
		}
		return ""
	}
	return candidate
}

func looksLikeWriteInstruction(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}
	phrases := []string{
		"create file",
		"write file",
		"save file",
		"edit file",
		"update file",
		"append file",
		"with content",
		"content '",
		"content \"",
		"named ",
		"called ",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func hasToolResult(events []toolResult, tool string) bool {
	for _, evt := range events {
		if strings.EqualFold(strings.TrimSpace(evt.Tool), strings.TrimSpace(tool)) {
			return true
		}
	}
	return false
}

func findPriorBashCommandEvent(events []toolResult, command string) (toolResult, bool) {
	target := normalizeBashCommand(command)
	if target == "" {
		return toolResult{}, false
	}
	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		if !strings.EqualFold(strings.TrimSpace(evt.Tool), "bash_exec") {
			continue
		}
		executed := normalizeBashCommand(extractBashCommandFromEvent(evt))
		if executed == "" {
			continue
		}
		if executed == target {
			return evt, true
		}
	}
	return toolResult{}, false
}

func extractBashCommandFromEvent(evt toolResult) string {
	if cmd := extractLinePrefix(evt.Lines, "Blocked command:"); cmd != "" {
		return cmd
	}
	if cmd := extractLinePrefix(evt.Lines, "Executing:"); cmd != "" {
		return cmd
	}
	return ""
}

func normalizeBashCommand(command string) string {
	trimmed := strings.TrimSpace(strings.ToLower(command))
	if trimmed == "" {
		return ""
	}
	return strings.Join(strings.Fields(trimmed), " ")
}

func isBlockedBashEvent(evt toolResult) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(evt.Error)), "blocked") {
		return true
	}
	return strings.TrimSpace(extractLinePrefix(evt.Lines, "Blocked command:")) != ""
}

func latestBlockedBashCommand(events []toolResult) string {
	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		if !strings.EqualFold(strings.TrimSpace(evt.Tool), "bash_exec") {
			continue
		}
		if !isBlockedBashEvent(evt) {
			continue
		}
		if cmd := strings.TrimSpace(extractBashCommandFromEvent(evt)); cmd != "" {
			return cmd
		}
	}
	return ""
}

func isDangerousBashCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	lower := strings.ToLower(cmd)

	dangerTokens := []string{
		"rm",
		"rmdir",
		"mv",
		"dd",
		"mkfs",
		"shutdown",
		"reboot",
		"poweroff",
		"kill",
		"killall",
		"pkill",
		"sudo",
		"chmod",
		"chown",
		"truncate",
		"mount",
		"umount",
	}

	fields := strings.Fields(lower)
	for _, field := range fields {
		field = strings.Trim(field, " \t\r\n;")
		for _, token := range dangerTokens {
			if field == token || strings.HasPrefix(field, token+" ") {
				return true
			}
		}
	}

	// Output redirection is always destructive.
	if strings.Contains(lower, " >") || strings.Contains(lower, ">>") {
		return true
	}

	// Pipes are only destructive if any segment contains a danger token.
	if strings.Contains(lower, "|") {
		safeCommands := map[string]bool{
			"awk": true, "cat": true, "cut": true, "echo": true, "find": true,
			"grep": true, "head": true, "jq": true, "less": true, "ls": true,
			"more": true, "rg": true, "sed": true, "sort": true, "tail": true,
			"tr": true, "uniq": true, "wc": true, "xargs": true,
		}
		for _, segment := range strings.Split(lower, "|") {
			first := strings.Fields(strings.TrimSpace(segment))
			if len(first) == 0 {
				continue
			}
			bin := strings.TrimLeft(first[0], " \t")
			if !safeCommands[bin] {
				return true
			}
		}
	}

	return false
}

func stripToolRunnerTags(input string) string {
	out := stripToolRunnerTagsForParsing(input)
	if isBareToolCallJSON(strings.TrimSpace(out)) {
		return ""
	}
	return out
}

func stripToolRunnerTagsForParsing(input string) string {
	out := input
	out = stripGPTOSSDelimiters(out)
	for _, tag := range []string{"think", "analysis"} {
		lower := strings.ToLower(out)
		open := "<" + tag + ">"
		close := "</" + tag + ">"
		for {
			start := strings.Index(lower, open)
			if start == -1 {
				break
			}
			end := strings.Index(lower[start+len(open):], close)
			if end == -1 {
				break
			}
			end = start + len(open) + end + len(close)
			out = out[:start] + out[end:]
			lower = strings.ToLower(out)
		}
	}
	return stripGPTOSSDelimiters(out)
}

func isBareToolCallJSON(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false
	}
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return false
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return false
	}
	if len(raw) == 0 {
		return false
	}

	has := func(key string) bool {
		_, ok := raw[key]
		return ok
	}
	if has("query") || has("q") || has("cmd") || has("command") {
		return true
	}
	if has("action") {
		var action string
		if err := json.Unmarshal(raw["action"], &action); err == nil {
			action = strings.ToLower(strings.TrimSpace(action))
			if strings.HasPrefix(action, "action:") {
				action = strings.TrimSpace(strings.TrimPrefix(action, "action:"))
			}
			if normalizeToolRunnerTool(action) != "" {
				return true
			}
			switch action {
			case "request_approval", "respond", "none", "no_tool", "skip", "action":
				return true
			}
		}
		// Non-string action is still treated as tool-call-like for safety.
		return true
	}
	return false
}

func stripGPTOSSDelimiters(input string) string {
	out := input
	const (
		channelMarker = "<|channel|>"
		messageMarker = "<|message|>"
	)
	for {
		lower := strings.ToLower(out)
		channelIdx := strings.Index(lower, channelMarker)
		if channelIdx == -1 {
			break
		}
		channelEnd := channelIdx + len(channelMarker)
		messageRel := strings.Index(lower[channelEnd:], messageMarker)
		if messageRel == -1 {
			// If there's no matching message marker, just remove the channel delimiter.
			out = out[:channelIdx] + out[channelEnd:]
			continue
		}
		messageIdx := channelEnd + messageRel
		messageEnd := messageIdx + len(messageMarker)
		// Remove the full "<|channel|>...<|message|>" envelope and keep payload.
		out = out[:channelIdx] + out[messageEnd:]
	}

	// Clean up any stray delimiter tokens left behind.
	out = strings.ReplaceAll(out, channelMarker, "")
	out = strings.ReplaceAll(out, messageMarker, "")
	return out
}

func parseToolRunnerToolCall(raw string) (toolPlan, bool) {
	lower := strings.ToLower(raw)
	tool := ""
	for _, candidate := range []string{"search_brave", "web_search", "bash_exec", "fs_read", "fs_write"} {
		if strings.Contains(lower, "to="+candidate) {
			tool = candidate
			break
		}
	}
	if tool == "" {
		return toolPlan{}, false
	}

	payload := raw
	if idx := strings.Index(lower, "<|message|>"); idx != -1 {
		payload = raw[idx+len("<|message|>"):]
	}

	jsonContent := extractFirstJSONObject(payload)
	if jsonContent == "" {
		return toolPlan{}, false
	}

	var plan toolPlan
	decoder := json.NewDecoder(strings.NewReader(jsonContent))
	if err := decoder.Decode(&plan); err != nil {
		plan = applyToolPlanFallbacks(jsonContent)
	} else {
		if fallback := applyToolPlanFallbacks(jsonContent); fallback.Action != "" {
			if strings.TrimSpace(plan.Action) == "" {
				plan.Action = fallback.Action
			}
			if strings.TrimSpace(plan.Input) == "" {
				plan.Input = fallback.Input
				plan.InputRaw = fallback.InputRaw
			}
			if strings.TrimSpace(plan.Reason) == "" {
				plan.Reason = fallback.Reason
			}
		}
	}

	if plan.Action == "" {
		plan.Action = normalizeToolRunnerTool(tool)
	}
	if plan.Action == "" {
		return toolPlan{}, false
	}
	if plan.Input == "" {
		return toolPlan{}, false
	}
	return plan, true
}

func normalizeToolRunnerTool(tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "search_brave", "web_search":
		return "web_search"
	case "bash_exec":
		return "bash_exec"
	case "fs_read":
		return "fs_read"
	case "fs_write":
		return "fs_write"
	default:
		return ""
	}
}

func extractFirstJSONObject(input string) string {
	start := strings.Index(input, "{")
	end := strings.LastIndex(input, "}")
	if start == -1 || end == -1 || start >= end {
		return ""
	}
	return input[start : end+1]
}

func applyToolPlanFallbacks(jsonContent string) toolPlan {
	trimmed := strings.TrimSpace(jsonContent)
	if trimmed == "" {
		return toolPlan{}
	}

	var raw map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	if err := decoder.Decode(&raw); err != nil {
		return toolPlan{}
	}

	getString := func(key string) string {
		val, ok := raw[key]
		if !ok {
			return ""
		}
		if str := anyToString(val); str != "" {
			return str
		}
		return coerceToolPlanInput(rawMessageFromAny(val))
	}

	action := getString("action")
	inputRaw := rawMessageFromAny(raw["input"])
	input := coerceToolPlanInput(inputRaw)
	reason := getString("reason")
	if reason == "" {
		reason = getString("note")
	}

	if action == "" {
		if query := getString("query"); query != "" {
			action = "web_search"
			input = query
			inputRaw = rawMessageFromAny(raw["query"])
		} else if q := getString("q"); q != "" {
			action = "web_search"
			input = q
			inputRaw = rawMessageFromAny(raw["q"])
		} else if cmd := getString("command"); cmd != "" {
			action = "bash_exec"
			input = cmd
			inputRaw = rawMessageFromAny(raw["command"])
		} else if cmd := getString("cmd"); cmd != "" {
			action = "bash_exec"
			input = cmd
			inputRaw = rawMessageFromAny(raw["cmd"])
		}
	}

	if action == "" {
		return toolPlan{}
	}
	actionNorm := strings.ToLower(strings.TrimSpace(action))
	if input == "" && actionNorm != "request_approval" && actionNorm != "respond" && actionNorm != "none" {
		return toolPlan{}
	}
	return toolPlan{
		Action:   action,
		Input:    input,
		InputRaw: inputRaw,
		Reason:   reason,
	}
}

func rawMessageFromAny(value interface{}) json.RawMessage {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}
