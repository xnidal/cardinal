package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message represents a single message in the conversation history
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Name      string    `json:"name,omitempty"`      // for tool messages
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"` // where this message came from (discord, terminal, etc)
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// Session represents a conversation session
type Session struct {
	ID        string    `json:"id"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Messages  []Message `json:"messages"`
	Summary   string    `json:"summary,omitempty"`
}

// MemoryStore handles persistent conversation storage
type MemoryStore struct {
	dataDir string
	current *Session
}

// NewMemoryStore creates a new memory store
func NewMemoryStore() (*MemoryStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dataDir := filepath.Join(home, ".cardinal", "memory")

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	ms := &MemoryStore{
		dataDir: dataDir,
	}

	// Load or create current session
	if err := ms.loadCurrentSession(); err != nil {
		// Create new session if none exists
		ms.current = &Session{
			ID:        generateID(),
			StartTime: time.Now(),
			Messages:  []Message{},
		}
		ms.saveCurrentSession()
	}

	return ms, nil
}

// loadCurrentSession loads the most recent session
func (ms *MemoryStore) loadCurrentSession() error {
	// Find the most recent session file
	files, err := os.ReadDir(ms.dataDir)
	if err != nil {
		return err
	}

	var latestFile string
	var latestTime time.Time

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		info, err := file.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestFile = file.Name()
		}
	}

	if latestFile == "" {
		return fmt.Errorf("no session files found")
	}

	data, err := os.ReadFile(filepath.Join(ms.dataDir, latestFile))
	if err != nil {
		return err
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return err
	}

	ms.current = &session
	return nil
}

// saveCurrentSession saves the current session to disk
func (ms *MemoryStore) saveCurrentSession() error {
	if ms.current == nil {
		return nil
	}

	filename := fmt.Sprintf("%s.json", ms.current.ID)
	path := filepath.Join(ms.dataDir, filename)

	data, err := json.MarshalIndent(ms.current, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// AddMessage adds a message to the current session
func (ms *MemoryStore) AddMessage(role, content, source string) error {
	msg := Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
		Source:    source,
	}

	ms.current.Messages = append(ms.current.Messages, msg)
	return ms.saveCurrentSession()
}

// AddMessageWithToolCalls adds a message with tool calls
func (ms *MemoryStore) AddMessageWithToolCalls(role, content, source string, toolCalls []ToolCall) error {
	msg := Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
		Source:    source,
		ToolCalls: toolCalls,
	}

	ms.current.Messages = append(ms.current.Messages, msg)
	return ms.saveCurrentSession()
}

// GetRecentMessages returns the last n messages
func (ms *MemoryStore) GetRecentMessages(n int) []Message {
	if n <= 0 || n > len(ms.current.Messages) {
		return ms.current.Messages
	}
	return ms.current.Messages[len(ms.current.Messages)-n:]
}

// GetAllMessages returns all messages in the current session
func (ms *MemoryStore) GetAllMessages() []Message {
	return ms.current.Messages
}

// GetFullHistory returns all messages from all sessions (for context loading)
func (ms *MemoryStore) GetFullHistory() ([]Message, error) {
	var allMessages []Message

	files, err := os.ReadDir(ms.dataDir)
	if err != nil {
		return nil, err
	}

	// Sort files by name (which includes timestamp)
	var sortedFiles []string
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			sortedFiles = append(sortedFiles, file.Name())
		}
	}

	// Read each session
	for _, filename := range sortedFiles {
		data, err := os.ReadFile(filepath.Join(ms.dataDir, filename))
		if err != nil {
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		allMessages = append(allMessages, session.Messages...)
	}

	return allMessages, nil
}

// Search searches for messages containing the query
func (ms *MemoryStore) Search(query string) []Message {
	var results []Message
	query = strings.ToLower(query)

	for _, msg := range ms.current.Messages {
		if strings.Contains(strings.ToLower(msg.Content), query) {
			results = append(results, msg)
		}
	}

	return results
}

// CompressOldMessages creates a summary of old messages to save context space
func (ms *MemoryStore) CompressOldMessages(keepRecent int) (string, error) {
	if len(ms.current.Messages) <= keepRecent {
		return "", nil
	}

	oldMessages := ms.current.Messages[:len(ms.current.Messages)-keepRecent]
	var summary strings.Builder

	summary.WriteString("Previous conversation summary:\n")
	for _, msg := range oldMessages {
		content := msg.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		content = strings.ReplaceAll(content, "\n", " ")
		summary.WriteString(fmt.Sprintf("- [%s] %s\n", msg.Role, content))
	}

	return summary.String(), nil
}

// GetStats returns memory statistics
func (ms *MemoryStore) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"session_id":       ms.current.ID,
		"message_count":    len(ms.current.Messages),
		"session_start":    ms.current.StartTime,
		"total_characters": ms.countCharacters(),
	}
}

func (ms *MemoryStore) countCharacters() int {
	total := 0
	for _, msg := range ms.current.Messages {
		total += len(msg.Content)
	}
	return total
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
