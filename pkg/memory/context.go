package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ContextManager handles loading and managing the full conversation context
type ContextManager struct {
	memoryStore    *MemoryStore
	soulPath       string
	maxContextSize int
}

// NewContextManager creates a new context manager
func NewContextManager(memoryStore *MemoryStore) (*ContextManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	cm := &ContextManager{
		memoryStore:    memoryStore,
		soulPath:       filepath.Join(home, ".cardinal", "SOUL.md"),
		maxContextSize: 100000, // ~100k characters default
	}

	return cm, nil
}

// LoadSOUL loads the SOUL.md content
func (cm *ContextManager) LoadSOUL() (string, error) {
	data, err := os.ReadFile(cm.soulPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "You are Cardinal, a helpful AI assistant.", nil
		}
		return "", err
	}
	return string(data), nil
}

// BuildContext builds the full context for the model
// This includes: SOUL.md + compressed history + recent messages
func (cm *ContextManager) BuildContext(currentPrompt string, source string) ([]Message, error) {
	var messages []Message

	// 1. Load SOUL.md as system message
	soul, err := cm.LoadSOUL()
	if err != nil {
		soul = "You are Cardinal, a helpful AI assistant."
	}

	// Add identity and context info to system prompt
	systemPrompt := fmt.Sprintf("%s\n\nYou have continuous memory across all conversations. You can remember past discussions and build on them.", soul)
	messages = append(messages, Message{
		Role:    "system",
		Content: systemPrompt,
	})

	// 2. Load compressed history
	fullHistory, err := cm.memoryStore.GetFullHistory()
	if err != nil {
		fullHistory = []Message{}
	}

	// 3. Compress if too large
	if cm.estimateSize(fullHistory) > cm.maxContextSize {
		compressed := cm.compressHistory(fullHistory)
		messages = append(messages, Message{
			Role:    "system",
			Content: compressed,
		})
	} else {
		// Add all history messages
		for _, msg := range fullHistory {
			messages = append(messages, Message{
				Role:    msg.Role,
				Content: msg.Content,
				Source:  msg.Source,
			})
		}
	}

	// 4. Add current prompt if provided
	if currentPrompt != "" {
		messages = append(messages, Message{
			Role:      "user",
			Content:   currentPrompt,
			Timestamp: time.Now(),
			Source:    source,
		})
	}

	return messages, nil
}

// BuildSubagentContext builds a minimal context for subagent tasks
// Subagents don't need full history - just the task
func (cm *ContextManager) BuildSubagentContext(task string) []Message {
	return []Message{
		{
			Role:    "system",
			Content: "You are a task-focused assistant. Complete the given task efficiently. You do not have access to conversation history - focus only on the task at hand.",
		},
		{
			Role:    "user",
			Content: task,
		},
	}
}

// compressHistory creates a summary of old messages
func (cm *ContextManager) compressHistory(messages []Message) string {
	var summary strings.Builder
	summary.WriteString("Previous conversation history (compressed):\n\n")

	// Group by day
	dayGroups := cm.groupByDay(messages)

	for day, dayMessages := range dayGroups {
		summary.WriteString(fmt.Sprintf("## %s\n", day))
		for _, msg := range dayMessages {
			content := msg.Content
			if len(content) > 150 {
				content = content[:150] + "..."
			}
			content = strings.ReplaceAll(content, "\n", " ")
			role := msg.Role
			if msg.Source != "" {
				role = fmt.Sprintf("%s (%s)", role, msg.Source)
			}
			summary.WriteString(fmt.Sprintf("- %s: %s\n", role, content))
		}
		summary.WriteString("\n")
	}

	return summary.String()
}

// groupByDay groups messages by day
func (cm *ContextManager) groupByDay(messages []Message) map[string][]Message {
	groups := make(map[string][]Message)

	for _, msg := range messages {
		day := msg.Timestamp.Format("2006-01-02")
		groups[day] = append(groups[day], msg)
	}

	return groups
}

// estimateSize estimates the total character count of messages
func (cm *ContextManager) estimateSize(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)
	}
	return total
}

// SaveInteraction saves a user-assistant interaction
func (cm *ContextManager) SaveInteraction(userMsg, assistantMsg, source string) error {
	if err := cm.memoryStore.AddMessage("user", userMsg, source); err != nil {
		return err
	}
	if err := cm.memoryStore.AddMessage("assistant", assistantMsg, source); err != nil {
		return err
	}
	return nil
}

// GetRecentContext returns recent messages for quick reference
func (cm *ContextManager) GetRecentContext(n int) []Message {
	return cm.memoryStore.GetRecentMessages(n)
}

// SearchPastConversations searches through all history
func (cm *ContextManager) SearchPastConversations(query string) []Message {
	return cm.memoryStore.Search(query)
}
