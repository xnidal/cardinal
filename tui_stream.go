package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"cardinal/pkg/api"
	"cardinal/pkg/parser"
	"cardinal/pkg/personality"
	"cardinal/pkg/prompt"
	"cardinal/pkg/storage"
	"cardinal/pkg/tools"

	tea "github.com/charmbracelet/bubbletea"
)

var thinkingMessages = []string{
	"Thinking",
	"Hmmming",
	"Pondering",
	"Contemplating",
	"Musing",
	"Ruminating",
	"Processing",
	"Computing",
	"Deliberating",
	"Reflecting",
	"Cogitating",
	"Meditating on your request",
	"Trying to be helpful",
	"Asking the digital oracle",
	"Consulting the neural networks",
	"Crunching the tokens",
	"Wiggling neurons",
	"Searching through weights",
	"Feeding forward",
	"Attention mechanisms activated",
	"Gradient descent in progress",
	"Loading knowledge...",
	"Accessing training data...",
	"Inference mode: engaged",
	"Decoding your intent",
	"Synthesizing response...",
	"Making it up as I go",
	"Hoping this is right",
	"Double-checking...",
	"Just a moment...",
	"Almost there...",
	"Almost ready...",
}

const (
	maxRetries = 5
	baseDelay  = 1 * time.Second
)

type streamEventMsg struct {
	event api.StreamEvent
}

type streamClosedMsg struct{}

type streamRetryMsg struct {
	attempt int
	err     error
}

type toolExecutionMsg struct {
	assistantContent string
	thinkingContent  string
	toolCalls        []api.ToolCall
	results          []tools.ToolResult
}

type modelsFetchedMsg struct {
	models []api.Model
	err    error
}

type thinkingMsg struct {
	thinking string
}

type goalEvalMsg struct {
	passed   bool
	feedback string
	err      error
}

func (m Model) fetchModels() tea.Cmd {
	return func() tea.Msg {
		models, err := m.client.ListModels()
		return modelsFetchedMsg{models: models, err: err}
	}
}

func waitForStreamEvent(ch <-chan api.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return streamEventMsg{event: event}
	}
}

func waitForRetry(attempt int) tea.Cmd {
	return tea.Tick(calculateBackoff(attempt), func(t time.Time) tea.Msg {
		return streamRetryMsg{attempt: attempt}
	})
}

func calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: 1s, 2s, 4s, 8s, 16s (max)
	delay := baseDelay * time.Duration(1<<uint(attempt))
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

func (m Model) buildMessages() []api.Message {
	systemPrompt := m.getSystemPrompt()
	if m.goalMode && strings.TrimSpace(m.goalText) != "" {
		systemPrompt += "\n\nGOAL MODE ACTIVE: Keep working on this goal until an external verifier passes it. Goal: " + m.goalText + ". Do not stop early. If verifier feedback appears, address it directly and continue."
	}
	messages := append([]api.Message{{Role: "system", Content: systemPrompt}}, m.messages...)
	messages = m.compressMessages(messages)
	return messages
}

func (m Model) getMaxTokens(messages []api.Message) int {
	contextLimit := m.contextLimit
	if contextLimit == 0 {
		contextLimit = 128000
	}
	maxTokens := min(api.CalculateMaxTokens(messages, m.toolDefs, contextLimit), 16384)
	return maxTokens
}

func (m Model) estimateTokens(messages []api.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4
		total += len(msg.Role) / 4
		// Account for thinking content - it gets wrapped in <thinking> tags when sent
		if msg.Thinking != "" {
			total += len(msg.Thinking) / 4
			total += 5 // for <thinking> and </thinking> tags + newlines
		}
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name) / 4
			total += len(tc.Function.Arguments) / 4
		}
		total += 10
	}
	return total
}

func (m Model) compressMessages(messages []api.Message) []api.Message {
	contextLimit := m.contextLimit
	if contextLimit == 0 {
		contextLimit = 128000
	}

	currentTokens := m.estimateTokens(messages)
	threshold := int(float64(contextLimit) * 0.7)

	if currentTokens < threshold {
		return messages
	}

	for {
		compressed := m.doCompress(messages)
		newTokens := m.estimateTokens(compressed)

		if newTokens < threshold || newTokens >= currentTokens {
			break
		}

		messages = compressed
		currentTokens = newTokens
	}

	return messages
}

func (m Model) doCompress(messages []api.Message) []api.Message {
	if len(messages) <= 4 {
		return messages
	}

	keepRecent := 4
	summaryEnd := len(messages) - keepRecent

	if summaryEnd <= 1 {
		return messages
	}

	var summary strings.Builder
	summary.WriteString("Previous conversation summary:\n")

	for i := 1; i < summaryEnd; i++ {
		msg := messages[i]
		content := msg.Content
		// Include thinking in the summary if present
		if msg.Thinking != "" {
			content = "<thinking>" + msg.Thinking + "</thinking> " + content
		}
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		content = strings.ReplaceAll(content, "\n", " ")

		if msg.Role == "tool" {
			summary.WriteString(fmt.Sprintf("> %s: %s\n", msg.Role, msg.Name))
		} else {
			summary.WriteString(fmt.Sprintf("> %s: %s\n", msg.Role, content))
		}
	}

	compressed := []api.Message{
		messages[0],
		{Role: "system", Content: summary.String()},
	}
	compressed = append(compressed, messages[summaryEnd:]...)

	return compressed
}

func (m Model) getSystemPrompt() string {
	builder := prompt.NewPromptBuilder(m.working, m.soul, personality.Load())
	defaultPrompt := "You are Cardinal, a helpful coding assistant. Be concise and direct."
	if strings.TrimSpace(m.cfg.SystemPrompt) != "" && m.cfg.SystemPrompt != defaultPrompt {
		builder.SetSectionContent("identity", m.cfg.SystemPrompt)
	}
	return builder.Build()
}

func (m Model) beginStream() (tea.Model, tea.Cmd) {
	messages := m.buildMessages()
	maxTokens := m.getMaxTokens(messages)
	m.streamCh = m.client.ChatStreamChannel(m.cfg.Model, messages, m.toolDefs, maxTokens)
	m.streaming = ""
	m.thinking = ""
	m.pendingToolCalls = nil
	m.streamChars = 0
	m.thinkingChars = 0
	m.toolCallChars = 0
	m.toolCallName = ""
	m.busy = true
	m.err = nil
	m.status = thinkingMessages[0]
	m.thinkingIdx = 0
	return m, tea.Batch(waitForStreamEvent(m.streamCh), startSpinnerTicker(), startScrollbackTicker())
}

func (m *Model) setStatus(newStatus string) {
	if m.errorStatus != "" && time.Since(m.errorStatusTime) < 3*time.Second {
		return
	}
	m.errorStatus = ""
	// priority: higher priority statuses override lower ones
	// [riority levels:
	//   0 - error (always shown)
	//   1 - writing tool call, Retrying (very specific)
	//   2 - receiving response (specific)
	//   3 - thinking (generic, lowest priority)
	newPriority := getStatusPriority(newStatus)
	currentPriority := getStatusPriority(m.status)
	if newPriority <= currentPriority {
		m.status = newStatus
	}
}

func getStatusPriority(status string) int {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "error"):
		return 0
	case strings.Contains(lower, "validating") || strings.Contains(lower, "review"):
		return 1
	case strings.Contains(lower, "writing") || strings.Contains(lower, "retrying"):
		return 2
	case strings.Contains(lower, "receiving"):
		return 3
	case strings.Contains(lower, "thinking") || strings.Contains(lower, "hmm") || strings.Contains(lower, "ponder"):
		return 4
	default:
		return 2
	}
}

func (m Model) handleStreamEvent(event api.StreamEvent) (tea.Model, tea.Cmd) {
	switch event.Type {
	case "content":
		event.Content = stripThinkingTags(event.Content)
		m.streaming += event.Content
		m.streamChars += len(event.Content)
		m.status = fmt.Sprintf("Writing response · %d chars", m.streamChars)
		m.retryCount = 0
		return m, waitForStreamEvent(m.streamCh)

	case "thinking":
		m.thinking += event.Thinking
		m.thinkingChars += len(event.Thinking)
		
		
		m.status = fmt.Sprintf("Thinking · %d chars", m.thinkingChars)
		return m, waitForStreamEvent(m.streamCh)

	case "tool_call_writing":
		toolCount := len(m.pendingToolCalls) + 1 // +1 for the one being written now
		if event.ToolCallName != "" {
			m.toolCallName = event.ToolCallName
		}
		if event.ToolCallArgsLen > 0 {
			m.toolCallChars = event.ToolCallArgsLen
		}
		name := m.toolCallName
		if name == "" {
			name = "tool"
		}
		if toolCount > 1 {
			m.status = fmt.Sprintf("Writing %d tool calls · %s · %d chars", toolCount, name, m.toolCallChars)
		} else {
			m.status = fmt.Sprintf("Writing tool call · %s · %d chars", name, m.toolCallChars)
		}
		return m, waitForStreamEvent(m.streamCh)

	case "tool_call":
		if event.Tool != nil {
			m.pendingToolCalls = append(m.pendingToolCalls, *event.Tool)
			toolCount := len(m.pendingToolCalls)
			if toolCount > 1 {
				m.setStatus(fmt.Sprintf("Review permissions (%d tool calls)", toolCount))
			} else {
				m.setStatus("Review tool permissions")
			}
		}
		return m, waitForStreamEvent(m.streamCh)

	case "usage":
		if event.Usage != nil {
			m.contextUsed = event.Usage.PromptTokens
		}
		return m, waitForStreamEvent(m.streamCh)

	case "done":
		m.retryCount = 0
		return m.finishAssistantTurn()

	case "error":
		logAPIError(event.Error, m.cfg.Model, m.buildMessages(), m.toolDefs)

		if shouldRetry(event.Error, m.retryCount) {
			m.retryCount++
			errorMsg := fmt.Sprintf("Error: %s (retry %d/%d)", formatError(event.Error), m.retryCount, maxRetries)
			m.status = errorMsg
			m.errorStatus = errorMsg
			m.errorStatusTime = time.Now()
			m.streamCh = nil
			return m, waitForRetry(m.retryCount)
		}
		m.streamCh = nil
		m.busy = false
		m.pendingToolCalls = nil
		m.err = event.Error
		errorMsg := fmt.Sprintf("Error: %s", formatError(event.Error))
		m.status = errorMsg
		m.errorStatus = errorMsg
		m.errorStatusTime = time.Now()
		return m, nil

	default:
		return m, waitForStreamEvent(m.streamCh)
	}
}

func (m Model) handleRetry(msg streamRetryMsg) (tea.Model, tea.Cmd) {
	if m.lastMessages != nil {
		m.messages = append([]api.Message(nil), m.lastMessages...)
	}

	messages := m.buildMessages()
	maxTokens := m.getMaxTokens(messages)
	m.streamCh = m.client.ChatStreamChannel(m.cfg.Model, messages, m.toolDefs, maxTokens)
	m.streaming = ""
	m.thinking = ""
	m.pendingToolCalls = nil
	m.errorStatus = ""
	m.setStatus(fmt.Sprintf("Retrying (attempt %d/%d)...", msg.attempt, maxRetries))
	return m, waitForStreamEvent(m.streamCh)
}

func shouldRetry(err error, retryCount int) bool {
	if err == nil || retryCount >= maxRetries {
		return false
	}

	// Check for retryable status codes
	if apiErr, ok := err.(*api.APIError); ok {
		switch apiErr.StatusCode {
		case 408, // Request Timeout
			429, // Too Many Requests
			500, // Internal Server Error
			502, // Bad Gateway
			503, // Service Unavailable
			504: // Gateway Timeout
			return true
		case 403: // Rate limit - retry with backoff
			return true
		}
	}

	// Also retry on network errors (no status code)
	return true
}

func formatError(err error) string {
	if err == nil {
		return ""
	}

	if apiErr, ok := err.(*api.APIError); ok {
		switch apiErr.StatusCode {
		case 400:
			return fmt.Sprintf("Bad request (400): %s", apiErr.Message)
		case 401:
			return "Unauthorized (401): Invalid API key"
		case 403:
			return "Forbidden (403): Access denied or rate limited"
		case 404:
			return "Not found (404): Endpoint or model not found"
		case 408:
			return "Request timeout (408): Server took too long"
		case 429:
			return "Rate limited (429): Too many requests"
		case 500:
			return "Server error (500): Internal server error"
		case 502:
			return "Bad gateway (502): Upstream server error"
		case 503:
			return "Unavailable (503): Service temporarily unavailable"
		case 504:
			return "Gateway timeout (504): Upstream server timeout"
		default:
			return fmt.Sprintf("API error (%d): %s", apiErr.StatusCode, apiErr.Message)
		}
	}

	return err.Error()
}

// addAssistantMessage adds an assistant message to the history, avoiding duplicates
func (m *Model) addAssistantMessage(content string, toolCalls ...api.ToolCall) {
	m.addAssistantMessageWithThinking(content, "", toolCalls...)
}

// addAssistantMessageWithThinking adds an assistant message with separate thinking content
func (m *Model) addAssistantMessageWithThinking(content, thinking string, toolCalls ...api.ToolCall) {
	if strings.TrimSpace(content) == "" && strings.TrimSpace(thinking) == "" && len(toolCalls) == 0 {
		return
	}

	// Skip if the last message has the exact same content
	if len(m.messages) > 0 {
		lastMsg := m.messages[len(m.messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Content == content && lastMsg.Thinking == thinking {
			return
		}
	}

	msg := api.Message{Role: "assistant", Content: content, Thinking: thinking}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	m.messages = append(m.messages, msg)
	m.lastMessages = append([]api.Message(nil), m.messages...)
}

func (m *Model) addToolResult(name, content, toolCallID string, args ...string) {
	msg := api.Message{
		Role:       "tool",
		Name:       name,
		Content:    content,
		ToolCallID: toolCallID,
	}
	if len(args) > 0 {
		msg.ToolArgs = args[0]
	}
	m.messages = append(m.messages, msg)
	m.lastMessages = append([]api.Message(nil), m.messages...)
}

// finalizeUIMessageHandling updates UI state after adding messages
func (m *Model) finalizeUIMessageHandling() {
	m.scrollOffset = 0
	m.useViewport = false
}

func (m Model) finishAssistantTurn() (tea.Model, tea.Cmd) {
	m.streamCh = nil

	// Check for text-formatted tool calls in content. Tool use must come through the API.
	if parser.ContainsToolCalls(m.thinking) || parser.ContainsToolCalls(m.streaming) {
		m.addAssistantMessageWithThinking(m.streaming, m.thinking)
		m.addToolResult("format_error", "Error: Your message was ignored because it looked like a tool call written as text. You MUST use the function calling API. Do not output JSON arguments, XML tool calls, or raw tool-call text. If you need a tool, call it through the tool interface; otherwise answer normally.", "")
		m.finalizeUIMessageHandling()
		return m.beginStream()
	}

	if len(m.pendingToolCalls) > 0 {
		toolCalls := append([]api.ToolCall(nil), m.pendingToolCalls...)
		streamingContent := m.streaming
		thinkingContent := m.thinking
		m.streaming = ""
		m.thinking = ""
		m.pendingToolCalls = nil

		// Separate tools into auto-approved and needs-approval
		var autoApproved []api.ToolCall
		var needsApproval []api.ToolCall
		var needsApprovalIndices []int

		for i, tc := range toolCalls {
			if m.autoApprove || !tools.RequiresApproval(tc.Function.Name) {
				autoApproved = append(autoApproved, tc)
			} else {
				needsApproval = append(needsApproval, tc)
				needsApprovalIndices = append(needsApprovalIndices, i)
			}
		}

		// If all tools are auto-approved, execute them
		if len(needsApproval) == 0 {
			approvals := make([]bool, len(toolCalls))
			for i := range approvals {
				approvals[i] = true
			}
			m.busy = true
			toolCount := len(toolCalls)
			if toolCount > 1 {
				m.setStatus(fmt.Sprintf("Running %d tools in parallel", toolCount))
			} else {
				m.setStatus("Running tool")
			}
			return m, m.executeToolPlanCmd(streamingContent, thinkingContent, toolCalls, approvals)
		}

		// If we have auto-approved tools, execute them first
		if len(autoApproved) > 0 {
			approvals := make([]bool, len(toolCalls))
			for i := range toolCalls {
				approvals[i] = m.autoApprove || !tools.RequiresApproval(toolCalls[i].Function.Name)
			}
			m.busy = true
			toolCount := approvedToolCount(approvals)
			if toolCount > 1 {
				m.setStatus(fmt.Sprintf("Running %d tools in parallel", toolCount))
			} else {
				m.setStatus("Running tool")
			}
			return m, m.executeToolPlanCmd(streamingContent, thinkingContent, toolCalls, approvals)
		}

		// All tools need approval — show inline approval card (no
		// separate full-screen mode).
		m.pendingApproval = newPermissionMode(streamingContent, thinkingContent, toolCalls)
		m.pendingApproval.selected = 0
		m.busy = false
		m.setStatus("Tool approval required")
		return m, nil
	}

	// Only add message if we have content
	// Clear streaming/thinking BEFORE adding to avoid duplicates
	streamingContent := strings.TrimSpace(m.streaming)
	thinkingContent := strings.TrimSpace(m.thinking)
	m.streaming = ""
	m.thinking = ""

	if streamingContent != "" || thinkingContent != "" {
		if streamingContent == "" && thinkingContent != "" {
			m.addAssistantMessageWithThinking("", thinkingContent)
		} else {
			m.addAssistantMessageWithThinking(streamingContent, thinkingContent)
		}
		m.finalizeUIMessageHandling()
	}

	if m.needsCompletionCheck() {
		m.completionChecks++
		m.busy = true
		m.setStatus("Validating response")
		return m, m.evaluateGoalCmd()
	}

	if m.needsTodoCheck() {
		m.todoChecks++
		m.busy = true
		m.setStatus("Checking todos")
		return m, m.evaluateTodoCmd()
	}

	m.busy = false
	m.setStatus("Ready")
	return m, nil
}

func (m Model) needsCompletionCheck() bool {
	if !m.goalMode || strings.TrimSpace(m.goalText) == "" {
		return false
	}
	if m.completionChecks >= 5 {
		return false
	}
	if len(m.messages) == 0 {
		return false
	}
	last := m.messages[len(m.messages)-1]
	if last.Role == "system" && strings.Contains(last.Content, "Goal verifier feedback:") {
		return false
	}
	return true
}

func (m Model) evaluateGoalCmd() tea.Cmd {
	goal := m.goalText
	conversation := compactGoalTranscript(m.messages)
	client := m.client
	model := m.cfg.Model
	return func() tea.Msg {
		messages := []api.Message{
			{Role: "system", Content: "You are a strict completion verifier. Decide if the assistant has fully satisfied the user's goal. Reply with exactly one JSON object: {\"passed\":true|false,\"feedback\":\"short reason or next required action\"}. Be strict: pass only if the goal is actually satisfied."},
			{Role: "user", Content: "Goal:\n" + goal + "\n\nConversation transcript:\n" + conversation},
		}
		msg, _, err := client.Chat(model, messages, nil, 512)
		if err != nil {
			return goalEvalMsg{err: err}
		}
		passed, feedback := parseGoalEval(msg.Content)
		return goalEvalMsg{passed: passed, feedback: feedback}
	}
}

func compactGoalTranscript(messages []api.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		if msg.Role == "system" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if msg.Role == "tool" {
			content = msg.Name + ": " + content
		}
		if content == "" && len(msg.ToolCalls) > 0 {
			var names []string
			for _, tc := range msg.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			content = "tool calls: " + strings.Join(names, ", ")
		}
		if content == "" {
			continue
		}
		if len(content) > 1200 {
			content = content[:1200] + "..."
		}
		b.WriteString(msg.Role + ": " + content + "\n")
	}
	return b.String()
}

func parseGoalEval(content string) (bool, string) {
	var out struct {
		Passed   bool   `json:"passed"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &out); err == nil {
		return out.Passed, strings.TrimSpace(out.Feedback)
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, "\"passed\":true") || strings.Contains(lower, "passed: true") {
		return true, "Goal verified."
	}
	return false, strings.TrimSpace(content)
}

func (m Model) handleGoalEval(msg goalEvalMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		logAPIError(msg.err, m.cfg.Model, nil, nil)
		m.err = msg.err
		m.busy = false
		m.setStatus("Verifier failed")
		return m, nil
	}
	feedback := strings.TrimSpace(msg.feedback)
	if feedback == "" {
		feedback = "No feedback."
	}
	if msg.passed {
		m.goalMode = false
		m.goalText = ""
		m.completionChecks = 0
		m.todoChecks = 0
		m.finalizeUIMessageHandling()
		m.setStatus("Ready")
		return m, nil
	}
	m.messages = append(m.messages, api.Message{Role: "system", Content: "Goal verifier feedback: " + feedback + "\nContinue working until this feedback is satisfied. Do not stop until the verifier passes."})
	m.finalizeUIMessageHandling()
	m.setStatus("Continuing goal")
	return m.beginStream()
}

func (m Model) executeToolPlanCmd(assistantContent, thinkingContent string, toolCalls []api.ToolCall, approvals []bool) tea.Cmd {
	working := m.working
	toolCallsCopy := append([]api.ToolCall(nil), toolCalls...)
	approvalsCopy := append([]bool(nil), approvals...)

	onEditSoul := func() {
		m.soul = loadSoul()
	}

	todos := m.todoStore

	return func() tea.Msg {
		results := executeToolPlan(working, todos, toolCallsCopy, approvalsCopy, onEditSoul)
		return toolExecutionMsg{
			assistantContent: assistantContent,
			thinkingContent:  thinkingContent,
			toolCalls:        toolCallsCopy,
			results:          results,
		}
	}
}

func (m Model) handleToolExecution(msg toolExecutionMsg) (tea.Model, tea.Cmd) {
	m.addAssistantMessageWithThinking(msg.assistantContent, msg.thinkingContent, msg.toolCalls...)

	completed := false
	for i, result := range msg.results {
		name := result.Name
		var args string
		var toolCallID string
		if i < len(msg.toolCalls) {
			if msg.toolCalls[i].Function.Name != "" {
				name = msg.toolCalls[i].Function.Name
			}
			args = msg.toolCalls[i].Function.Arguments
			toolCallID = msg.toolCalls[i].ID
		}
		if name == "yes" && result.Success {
			completed = true
		}

		if len(result.Data) > 0 {
			// Emit one tool result message per sub-result (e.g. per-file for read_files).
			for _, sub := range result.Data {
				m.addToolResult(name, tools.FormatToolResult(sub), toolCallID, args)
			}
			// Attach the top-level metadata (e.g. per-file line ranges) to the last
			// sub-message so the UI can render the tree.
			if result.Lines != "" {
				if n := len(m.messages); n > 0 {
					m.messages[n-1].MetaLines = result.Lines
				}
			}
		} else {
			m.addToolResult(name, tools.FormatToolResult(result), toolCallID, args)
			if result.Lines != "" {
				if n := len(m.messages); n > 0 {
					m.messages[n-1].MetaLines = result.Lines
				}
			}
		}
	}

	m.finalizeUIMessageHandling()
	if completed {
		m.completionChecks = 0
		m.todoChecks = 0
		m.busy = false
		m.setStatus("Ready")
		return m, nil
	}

	m.setStatus("Continuing after tool results")
	return m.beginStream()
}

var thinkingTagRE = regexp.MustCompile(`(?s)<thinking[^>]*>.*?</thinking>`)

func stripThinkingTags(s string) string {
	return thinkingTagRE.ReplaceAllString(s, "")
}

// needsTodoCheck reports whether the assistant just produced a natural text
// reply (no tool calls, no pending goal verification) while the todo list
// still contains unchecked items. If so the model may have actually completed
// the work and just forgotten to check off the boxes — we ask an external
// verifier to look and silently mark anything that's genuinely done.
func (m Model) needsTodoCheck() bool {
	if m.busy {
		return false
	}
	if m.goalMode && strings.TrimSpace(m.goalText) != "" {
		// Goal-mode verifier (if any) takes priority.
		return false
	}
	if m.todoChecks >= 5 {
		return false
	}
	if m.todoStore == nil {
		return false
	}
	if len(m.todoStore.List("pending")) == 0 && len(m.todoStore.List("in_progress")) == 0 {
		return false
	}
	// The most recent message must be an assistant message — we only nudge
	// after the model explicitly stopped, never in the middle of a turn.
	if len(m.messages) == 0 {
		return false
	}
	last := m.messages[len(m.messages)-1]
	if last.Role != "assistant" {
		return false
	}
	// Avoid re-checking immediately after verifier feedback was just sent.
	if strings.Contains(last.Content, "Todo verifier feedback:") {
		return false
	}
	return true
}

func (m Model) evaluateTodoCmd() tea.Cmd {
	client := m.client
	model := m.cfg.Model
	pending := m.todoStore.List("pending")
	inProgress := m.todoStore.List("in_progress")
	todos := append(append([]storage.TodoItem{}, pending...), inProgress...)
	todoSnapshot := tools.FormatTodoList(todos)
	transcript := compactGoalTranscript(m.messages)
	return func() tea.Msg {
		// Cap the transcript so we don't blow the verifier's context.
		if len(transcript) > 4000 {
			transcript = transcript[len(transcript)-4000:]
		}
		// System prompt asks for strict JSON; user prompt supplies the state.
		// Note "tick off" rather than "update" matches the simplistic tool
		// surface available (todo_write + status: completed).
		verifierSystem := "You are a strict todo-list verifier. Given the assistant's " +
			"recent work and the current todo list, decide which unchecked items " +
			"have actually been completed but not yet marked done. Reply with exactly " +
			"one JSON object: {\"ticked\":[\"id1\",\"id2\"],\"remaining\":[\"id3\"]}. " +
			"Only tick an item if the transcript clearly shows the work was done " +
			"(file edited, command run, output produced). When in doubt, leave it " +
			"unchecked. If everything is genuinely done, return {\"ticked\":[],\"remaining\":[]}."
		verifierUser := "Unchecked todos (id — title):\n" + todoSnapshot +
			"\n\nRecent transcript:\n" + transcript
		messages := []api.Message{
			{Role: "system", Content: verifierSystem},
			{Role: "user", Content: verifierUser},
		}
		resp, _, err := client.Chat(model, messages, nil, 512)
		if err != nil {
			return todoEvalMsg{err: err}
		}
		ticked, remaining := parseTodoEval(resp.Content)
		return todoEvalMsg{ticked: ticked, remaining: remaining}
	}
}

type todoEvalMsg struct {
	ticked    []string
	remaining []string
	err       error
}

// handleTodoEval applies the verifier's verdict: silently tick off completed
// items (with a one-line system note so the user can see what happened), and
// if anything is still genuinely outstanding, feed it back as a system message
// so the assistant keeps going. We deliberately do NOT mark items completed
// ourselves — the model has to do it via todo_write so the assistant's
// in-conversation state stays consistent.
func (m Model) handleTodoEval(msg todoEvalMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		logAPIError(msg.err, m.cfg.Model, nil, nil)
		m.err = msg.err
		m.busy = false
		m.setStatus("Todo verifier failed")
		return m, nil
	}
	for _, id := range msg.ticked {
		completed := storage.TodoCompleted
		if _, err := m.todoStore.Update(id, &completed, nil); err == nil {
			m.addSystemNote("todo verifier: ticked off #" + id)
		}
	}
	if len(msg.remaining) == 0 {
		m.todoChecks = 0
		m.finalizeUIMessageHandling()
		m.busy = false
		m.setStatus("Ready")
		return m, nil
	}
	// Build a short, specific reminder — names the items so the model knows
	// exactly what still needs attention.
	var b strings.Builder
	b.WriteString("Todo verifier feedback: these items still look genuinely unfinished:\n")
	for _, id := range msg.remaining {
		b.WriteString("- #" + id + "\n")
	}
	b.WriteString("Continue working on them. When each is actually complete, call todo_write with action=update and status=completed so the list reflects reality.")
	m.messages = append(m.messages, api.Message{Role: "system", Content: strings.TrimRight(b.String(), "\n")})
	m.finalizeUIMessageHandling()
	m.setStatus("Continuing todos")
	return m.beginStream()
}

func parseTodoEval(content string) (ticked, remaining []string) {
	var out struct {
		Ticked    []string `json:"ticked"`
		Remaining []string `json:"remaining"`
	}
	body := strings.TrimSpace(content)
	if body == "" {
		return nil, nil
	}
	// Strip ```json fences if the verifier wrapped its answer.
	if strings.HasPrefix(body, "```") {
		body = strings.TrimPrefix(body, "```")
		if i := strings.Index(body, "\n"); i >= 0 {
			body = body[i+1:]
		}
		if strings.HasSuffix(body, "```") {
			body = strings.TrimSuffix(body, "```")
		}
	}
	if err := json.Unmarshal([]byte(body), &out); err == nil {
		return out.Ticked, out.Remaining
	}
	return nil, nil
}

// See tools.FormatTodoList for checklist rendering; this verifier reuses it
// directly so we don't keep two visual formats in sync.

