package originchats

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ToolName constants for OriginChats tools
const (
	ToolSendMessage       = "originchats_send_message"
	ToolSendReply         = "originchats_send_reply"
	ToolEditMessage       = "originchats_edit_message"
	ToolGetMessages       = "originchats_get_messages"
	ToolSearchMessages    = "originchats_search_messages"
	ToolAddReaction       = "originchats_add_reaction"
	ToolSetTyping         = "originchats_set_typing"
	ToolListChannels      = "originchats_list_channels"
	ToolGetHistory        = "originchats_get_history"
)

// ToolResult represents the result of an OriginChats tool execution
type ToolResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// ToolManager manages OriginChats tool execution
type ToolManager struct {
	client  *Client
	history *MessageHistory
	mu      sync.Mutex
}

// NewToolManager creates a new tool manager
func NewToolManager(client *Client, history *MessageHistory) *ToolManager {
	return &ToolManager{
		client:  client,
		history: history,
	}
}

// Execute runs an OriginChats tool
func (tm *ToolManager) Execute(name, args string) ToolResult {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	switch name {
	case ToolSendMessage:
		return tm.executeSendMessage(args)
	case ToolSendReply:
		return tm.executeSendReply(args)
	case ToolEditMessage:
		return tm.executeEditMessage(args)
	case ToolGetMessages:
		return tm.executeGetMessages(args)
	case ToolSearchMessages:
		return tm.executeSearchMessages(args)
	case ToolAddReaction:
		return tm.executeAddReaction(args)
	case ToolSetTyping:
		return tm.executeSetTyping(args)
	case ToolListChannels:
		return tm.executeListChannels(args)
	case ToolGetHistory:
		return tm.executeGetHistory(args)
	default:
		return ToolResult{Name: name, Success: false, Error: fmt.Sprintf("unknown tool: %s", name)}
	}
}

// GetToolDefinitions returns the tool definitions for OriginChats
func GetToolDefinitions() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolSendMessage,
				"description": "Send a message to an OriginChats channel. Use this to communicate with users in chat.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel": map[string]any{"type": "string", "description": "The channel name to send the message to (e.g. 'general', 'random')"},
						"content": map[string]any{"type": "string", "description": "The message content to send"},
					},
					"required": []string{"channel", "content"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolSendReply,
				"description": "Reply to a specific message in an OriginChats channel. Use this when responding to a specific user message.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel":     map[string]any{"type": "string", "description": "The channel name"},
						"message_id":  map[string]any{"type": "string", "description": "The ID of the message to reply to"},
						"content":     map[string]any{"type": "string", "description": "The reply content"},
					},
					"required": []string{"channel", "message_id", "content"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolEditMessage,
				"description": "Edit a message you previously sent in OriginChats. Use this to correct mistakes or update information.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel":    map[string]any{"type": "string", "description": "The channel name"},
						"message_id": map[string]any{"type": "string", "description": "The ID of the message to edit"},
						"content":    map[string]any{"type": "string", "description": "The new message content"},
					},
					"required": []string{"channel", "message_id", "content"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolGetMessages,
				"description": "Fetch recent messages from an OriginChats channel. Use this to see recent conversation context.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel": map[string]any{"type": "string", "description": "The channel name"},
						"limit":   map[string]any{"type": "integer", "description": "Number of messages to fetch (default: 30, max: 100)"},
					},
					"required": []string{"channel"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolSearchMessages,
				"description": "Search for messages in an OriginChats channel by query. Use this to find specific messages or topics.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel": map[string]any{"type": "string", "description": "The channel name"},
						"query":   map[string]any{"type": "string", "description": "The search query"},
					},
					"required": []string{"channel", "query"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolAddReaction,
				"description": "Add an emoji reaction to a message in OriginChats.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel":    map[string]any{"type": "string", "description": "The channel name"},
						"message_id": map[string]any{"type": "string", "description": "The ID of the message to react to"},
						"emoji":      map[string]any{"type": "string", "description": "The emoji to react with (e.g. '👍', '❤️', '😂')"},
					},
					"required": []string{"channel", "message_id", "emoji"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolSetTyping,
				"description": "Send a typing indicator to an OriginChats channel. Use this before composing a longer response.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel": map[string]any{"type": "string", "description": "The channel name"},
					},
					"required": []string{"channel"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolListChannels,
				"description": "List all available channels on the OriginChats server. Use this to discover where to send messages.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        ToolGetHistory,
				"description": "Get the cached message history for a specific channel. This is faster than fetching from the server as it uses locally cached messages.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel": map[string]any{"type": "string", "description": "The channel name"},
						"limit":   map[string]any{"type": "integer", "description": "Maximum number of recent messages to return (default: 20)"},
					},
					"required": []string{"channel"},
				},
			},
		},
	}
}

// RequiresApproval returns whether a tool requires user approval
func RequiresApproval(name string) bool {
	switch name {
	case ToolGetMessages, ToolSearchMessages, ToolListChannels, ToolGetHistory, ToolSetTyping, ToolAddReaction:
		return false
	default:
		return true
	}
}

func (tm *ToolManager) executeSendMessage(args string) ToolResult {
	var params struct {
		Channel string `json:"channel"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolSendMessage, Success: false, Error: err.Error()}
	}

	msg, err := tm.client.SendMessage(params.Channel, params.Content)
	if err != nil {
		return ToolResult{Name: ToolSendMessage, Success: false, Error: err.Error()}
	}

	// Add to history
	tm.history.Add(params.Channel, *msg)

	return ToolResult{
		Name:    ToolSendMessage,
		Success: true,
		Output:  fmt.Sprintf("Message sent to #%s (id: %s)", params.Channel, msg.ID),
	}
}

func (tm *ToolManager) executeSendReply(args string) ToolResult {
	var params struct {
		Channel   string `json:"channel"`
		MessageID string `json:"message_id"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolSendReply, Success: false, Error: err.Error()}
	}

	msg, err := tm.client.SendReply(params.Channel, params.MessageID, params.Content)
	if err != nil {
		return ToolResult{Name: ToolSendReply, Success: false, Error: err.Error()}
	}

	tm.history.Add(params.Channel, *msg)

	return ToolResult{
		Name:    ToolSendReply,
		Success: true,
		Output:  fmt.Sprintf("Reply sent to #%s (id: %s, replying to: %s)", params.Channel, msg.ID, params.MessageID),
	}
}

func (tm *ToolManager) executeEditMessage(args string) ToolResult {
	var params struct {
		Channel   string `json:"channel"`
		MessageID string `json:"message_id"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolEditMessage, Success: false, Error: err.Error()}
	}

	err := tm.client.EditMessage(params.Channel, params.MessageID, params.Content)
	if err != nil {
		return ToolResult{Name: ToolEditMessage, Success: false, Error: err.Error()}
	}

	return ToolResult{
		Name:    ToolEditMessage,
		Success: true,
		Output:  fmt.Sprintf("Message %s edited in #%s", params.MessageID, params.Channel),
	}
}

func (tm *ToolManager) executeGetMessages(args string) ToolResult {
	var params struct {
		Channel string `json:"channel"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolGetMessages, Success: false, Error: err.Error()}
	}

	if params.Limit <= 0 {
		params.Limit = 30
	}
	if params.Limit > 100 {
		params.Limit = 100
	}

	msgs, err := tm.client.GetMessages(params.Channel, params.Limit)
	if err != nil {
		return ToolResult{Name: ToolGetMessages, Success: false, Error: err.Error()}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Last %d messages in #%s:\n", len(msgs), params.Channel))
	for _, msg := range msgs {
		username := msg.User
		if username == "" {
			username = "unknown"
		}
		content := msg.Content
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", formatTimestamp(msg.Timestamp), username, content))
	}

	// Add to history
	for _, msg := range msgs {
		tm.history.Add(params.Channel, msg)
	}

	return ToolResult{
		Name:    ToolGetMessages,
		Success: true,
		Output:  sb.String(),
	}
}

func (tm *ToolManager) executeSearchMessages(args string) ToolResult {
	var params struct {
		Channel string `json:"channel"`
		Query   string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolSearchMessages, Success: false, Error: err.Error()}
	}

	msgs, err := tm.client.SearchMessages(params.Channel, params.Query)
	if err != nil {
		return ToolResult{Name: ToolSearchMessages, Success: false, Error: err.Error()}
	}

	if len(msgs) == 0 {
		return ToolResult{
			Name:    ToolSearchMessages,
			Success: true,
			Output:  fmt.Sprintf("No messages found matching \"%s\" in #%s", params.Query, params.Channel),
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d message(s) matching \"%s\" in #%s:\n", len(msgs), params.Query, params.Channel))
	for _, msg := range msgs {
		username := msg.User
		if username == "" {
			username = "unknown"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", formatTimestamp(msg.Timestamp), username, truncate(msg.Content, 150)))
	}

	return ToolResult{
		Name:    ToolSearchMessages,
		Success: true,
		Output:  sb.String(),
	}
}

func (tm *ToolManager) executeAddReaction(args string) ToolResult {
	var params struct {
		Channel   string `json:"channel"`
		MessageID string `json:"message_id"`
		Emoji     string `json:"emoji"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolAddReaction, Success: false, Error: err.Error()}
	}

	err := tm.client.AddReaction(params.Channel, params.MessageID, params.Emoji)
	if err != nil {
		return ToolResult{Name: ToolAddReaction, Success: false, Error: err.Error()}
	}

	return ToolResult{
		Name:    ToolAddReaction,
		Success: true,
		Output:  fmt.Sprintf("Reacted with %s to message %s in #%s", params.Emoji, params.MessageID, params.Channel),
	}
}

func (tm *ToolManager) executeSetTyping(args string) ToolResult {
	var params struct {
		Channel string `json:"channel"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolSetTyping, Success: false, Error: err.Error()}
	}

	err := tm.client.SetTyping(params.Channel)
	if err != nil {
		return ToolResult{Name: ToolSetTyping, Success: false, Error: err.Error()}
	}

	return ToolResult{
		Name:    ToolSetTyping,
		Success: true,
		Output:  fmt.Sprintf("Typing indicator sent to #%s", params.Channel),
	}
}

func (tm *ToolManager) executeListChannels(args string) ToolResult {
	channels := tm.client.GetChannels()

	if len(channels) == 0 {
		return ToolResult{
			Name:    ToolListChannels,
			Success: true,
			Output:  "No channels available. The server may not have sent the channel list yet.",
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Channels on %s (%d):\n", tm.client.ServerName(), len(channels)))
	for _, ch := range channels {
		chType := ch.Type
		if chType == "" {
			chType = "text"
		}
		sb.WriteString(fmt.Sprintf("  #%s", ch.Name))
		if ch.Description != "" {
			sb.WriteString(fmt.Sprintf(" - %s", ch.Description))
		}
		sb.WriteString(fmt.Sprintf(" [%s]\n", chType))
	}

	return ToolResult{
		Name:    ToolListChannels,
		Success: true,
		Output:  sb.String(),
	}
}

func (tm *ToolManager) executeGetHistory(args string) ToolResult {
	var params struct {
		Channel string `json:"channel"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: ToolGetHistory, Success: false, Error: err.Error()}
	}

	if params.Limit <= 0 {
		params.Limit = 20
	}

	history := tm.history.FormatHistory(params.Channel, params.Limit)
	if history == "" {
		return ToolResult{
			Name:    ToolGetHistory,
			Success: true,
			Output:  fmt.Sprintf("No cached history for #%s", params.Channel),
		}
	}

	return ToolResult{
		Name:    ToolGetHistory,
		Success: true,
		Output:  fmt.Sprintf("Cached history for #%s (last %d messages):\n%s", params.Channel, params.Limit, history),
	}
}

// FormatToolResult formats a tool result for the AI
func FormatToolResult(result ToolResult) string {
	if !result.Success {
		return fmt.Sprintf("[%s] Error: %s", result.Name, result.Error)
	}
	return result.Output
}

func formatTimestamp(ts float64) string {
	if ts == 0 {
		return "unknown"
	}
	t := time.Unix(int64(ts), 0)
	return t.Format("15:04:05")
}
