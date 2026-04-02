package memory

import (
	"cardinal/pkg/api"
)

// Integration provides helper functions for integrating memory with the TUI
// This keeps the TUI code clean and separates concerns

// MemoryIntegration handles the integration between memory and TUI
type MemoryIntegration struct {
	store      *MemoryStore
	contextMgr *ContextManager
}

// NewIntegration creates a new memory integration
func NewIntegration() (*MemoryIntegration, error) {
	store, err := NewMemoryStore()
	if err != nil {
		return nil, err
	}

	contextMgr, err := NewContextManager(store)
	if err != nil {
		return nil, err
	}

	return &MemoryIntegration{
		store:      store,
		contextMgr: contextMgr,
	}, nil
}

// LoadContext builds the full context for the main model
func (mi *MemoryIntegration) LoadContext(currentPrompt, source string) ([]api.Message, error) {
	messages, err := mi.contextMgr.BuildContext(currentPrompt, source)
	if err != nil {
		return nil, err
	}

	// Convert memory.Message to api.Message
	var apiMessages []api.Message
	for _, msg := range messages {
		apiMsg := api.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}

		// Handle tool calls if present
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				toolCall := api.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
				}
				toolCall.Function.Name = tc.Function.Name
				toolCall.Function.Arguments = tc.Function.Arguments
				apiMsg.ToolCalls = append(apiMsg.ToolCalls, toolCall)
			}
		}

		apiMessages = append(apiMessages, apiMsg)
	}

	return apiMessages, nil
}

// LoadSubagentContext builds minimal context for subagent tasks
func (mi *MemoryIntegration) LoadSubagentContext(task string) []api.Message {
	msgs := mi.contextMgr.BuildSubagentContext(task)

	var apiMessages []api.Message
	for _, msg := range msgs {
		apiMessages = append(apiMessages, api.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	return apiMessages
}

// SaveMessage saves a message to the memory store
func (mi *MemoryIntegration) SaveMessage(role, content, source string) error {
	return mi.store.AddMessage(role, content, source)
}

// SaveInteraction saves a user-assistant pair
func (mi *MemoryIntegration) SaveInteraction(userMsg, assistantMsg, source string) error {
	return mi.contextMgr.SaveInteraction(userMsg, assistantMsg, source)
}

// GetRecentMessages returns recent messages for display
func (mi *MemoryIntegration) GetRecentMessages(n int) []Message {
	return mi.store.GetRecentMessages(n)
}

// GetStats returns memory statistics
func (mi *MemoryIntegration) GetStats() map[string]interface{} {
	return mi.store.GetStats()
}

// SearchHistory searches through past conversations
func (mi *MemoryIntegration) SearchHistory(query string) []Message {
	return mi.contextMgr.SearchPastConversations(query)
}
