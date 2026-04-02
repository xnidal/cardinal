package main

import (
	"fmt"
	"strings"
	"time"

	"cardinal/pkg/api"
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
	messages := append([]api.Message{{Role: "system", Content: systemPrompt}}, m.messages...)
	messages = m.compressMessages(messages)
	return messages
}

func (m Model) estimateTokens(messages []api.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4
		total += len(msg.Role) / 4
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
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		content = strings.ReplaceAll(content, "\n", " ")

		if msg.Role == "tool" {
			summary.WriteString(fmt.Sprintf("- %s: %s\n", msg.Role, msg.Name))
		} else {
			summary.WriteString(fmt.Sprintf("- %s: %s\n", msg.Role, content))
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
	systemPrompt := strings.TrimSpace(m.cfg.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = "You are Cardinal, a helpful coding assistant. Be concise and direct."
	}
	return systemPrompt + "\n\nWorking directory: " + m.working
}

func (m Model) beginStream() (tea.Model, tea.Cmd) {
	messages := m.buildMessages()
	m.streamCh = m.client.ChatStreamChannel(m.cfg.Model, messages, m.toolDefs)
	m.streaming = ""
	m.thinking = ""
	m.pendingToolCalls = nil
	m.busy = true
	m.err = nil
	m.status = thinkingMessages[0]
	m.thinkingIdx = 0
	return m, tea.Batch(waitForStreamEvent(m.streamCh), startSpinnerTicker())
}

func (m Model) handleStreamEvent(event api.StreamEvent) (tea.Model, tea.Cmd) {
	switch event.Type {
	case "content":
		m.streaming += event.Content
		m.status = "Receiving response"
		m.retryCount = 0
		return m, waitForStreamEvent(m.streamCh)

	case "thinking":
		m.thinking += event.Thinking
		m.status = "Thinking"
		return m, waitForStreamEvent(m.streamCh)

	case "tool_call_writing":
		if event.ToolCallName != "" {
			m.status = fmt.Sprintf("Writing %s (%d chars)...", event.ToolCallName, event.ToolCallArgsLen)
		} else {
			m.status = "Writing tool call..."
		}
		return m, waitForStreamEvent(m.streamCh)

	case "tool_call":
		if event.Tool != nil {
			m.pendingToolCalls = append(m.pendingToolCalls, *event.Tool)
			m.status = "Review tool permissions"
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
		if shouldRetry(event.Error, m.retryCount) {
			m.retryCount++
			m.status = fmt.Sprintf("Error: %s (retry %d/%d)", formatError(event.Error), m.retryCount, maxRetries)
			m.streamCh = nil
			return m, waitForRetry(m.retryCount)
		}
		m.streamCh = nil
		m.busy = false
		m.pendingToolCalls = nil
		m.err = event.Error
		m.status = fmt.Sprintf("Error: %s", formatError(event.Error))
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
	m.streamCh = m.client.ChatStreamChannel(m.cfg.Model, messages, m.toolDefs)
	m.streaming = ""
	m.thinking = ""
	m.pendingToolCalls = nil
	m.status = fmt.Sprintf("Retrying (attempt %d/%d)...", msg.attempt, maxRetries)
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
	if strings.TrimSpace(content) == "" && len(toolCalls) == 0 {
		return
	}

	// Check for duplicate
	if len(m.messages) > 0 {
		lastMsg := m.messages[len(m.messages)-1]
		if lastMsg.Role == "assistant" &&
			lastMsg.Content == content &&
			len(lastMsg.ToolCalls) == len(toolCalls) {
			return
		}
	}

	msg := api.Message{Role: "assistant", Content: content}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	m.messages = append(m.messages, msg)
	m.lastMessages = append([]api.Message(nil), m.messages...)
}

// addToolResult adds a tool result message to the history
func (m *Model) addToolResult(name, content string) {
	m.messages = append(m.messages, api.Message{
		Role:    "tool",
		Name:    name,
		Content: content,
	})
	m.lastMessages = append([]api.Message(nil), m.messages...)
}

// finalizeUIMessageHandling updates UI state after adding messages
func (m *Model) finalizeUIMessageHandling() {
	m.scrollOffset = 0
	m.useViewport = true
	m.updateViewportContent()
	m.viewport.GotoBottom()
}

func (m Model) finishAssistantTurn() (tea.Model, tea.Cmd) {
	m.streamCh = nil

	// Build assistant message content
	msgContent := m.streaming
	if m.thinking != "" {
		msgContent = m.thinking + "\n\n" + m.streaming
	}

	if len(m.pendingToolCalls) > 0 {
		toolCalls := append([]api.ToolCall(nil), m.pendingToolCalls...)
		m.streaming = ""
		m.thinking = ""
		m.pendingToolCalls = nil

		// If auto-approve is enabled, approve all tool calls
		if m.autoApprove {
			approvals := make([]bool, len(toolCalls))
			for i := range approvals {
				approvals[i] = true
			}
			m.busy = true
			m.status = "Running tools"
			return m, m.executeToolPlanCmd(msgContent, toolCalls, approvals)
		}

		if hasPendingApprovals(toolCalls) {
			m.mode = "permissions"
			m.modeData = newPermissionMode(msgContent, toolCalls)
			m.busy = false
			m.status = "Tool approval required"
			return m, nil
		}

		approvals := defaultToolApprovals(toolCalls)
		m.busy = true
		m.status = "Running tools"
		return m, m.executeToolPlanCmd(msgContent, toolCalls, approvals)
	}

	// Only add message if we have content
	if strings.TrimSpace(msgContent) != "" {
		m.addAssistantMessage(msgContent)
		m.finalizeUIMessageHandling()
	}

	m.streaming = ""
	m.thinking = ""
	m.busy = false
	m.status = "Ready"
	return m, nil
}

func (m Model) executeToolPlanCmd(assistantContent string, toolCalls []api.ToolCall, approvals []bool) tea.Cmd {
	working := m.working
	toolCallsCopy := append([]api.ToolCall(nil), toolCalls...)
	approvalsCopy := append([]bool(nil), approvals...)

	onEditSoul := func() {
		m.soul = loadSoul()
	}

	return func() tea.Msg {
		results := executeToolPlan(working, toolCallsCopy, approvalsCopy, onEditSoul)
		return toolExecutionMsg{
			assistantContent: assistantContent,
			toolCalls:        toolCallsCopy,
			results:          results,
		}
	}
}

func (m Model) handleToolExecution(msg toolExecutionMsg) (tea.Model, tea.Cmd) {
	// Add assistant message with tool calls
	m.addAssistantMessage(msg.assistantContent, msg.toolCalls...)

	// Add tool results
	for i, result := range msg.results {
		name := result.Name
		if i < len(msg.toolCalls) && msg.toolCalls[i].Function.Name != "" {
			name = msg.toolCalls[i].Function.Name
		}
		m.addToolResult(name, tools.FormatToolResult(result))
	}

	m.status = "Continuing after tool results"
	m.finalizeUIMessageHandling()
	return m.beginStream()
}
