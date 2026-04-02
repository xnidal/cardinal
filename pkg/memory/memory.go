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
	Name      string    `json:"name,omitempty"`
	Source    string    `json:"source,omitempty"`    // where this message came from (discord, cli, tui)
	Timestamp time.Time `json:"timestamp"`
}

// Summary represents a compressed summary of a conversation segment
type Summary struct {
	Content   string    `json:"content"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	MessageCount int    `json:"message_count"`
	Topics    []string  `json:"topics,omitempty"`
}

// MemoryStore manages persistent conversation history
type MemoryStore struct {
	configDir string
	messages  []Message
	summaries []Summary
}

// NewMemoryStore creates a new memory store
func NewMemoryStore() (*MemoryStore, error) {
	configDir := getConfigPath()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	ms := &MemoryStore{
		configDir: configDir,
		messages:  make([]Message, 0),
		summaries: make([]Summary, 0),
	}

	if err := ms.load(); err != nil {
		// If file doesn't exist, that's fine - start fresh
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load memory: %w", err)
		}
	}

	return ms, nil
}

func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cardinal"
	}
	return filepath.Join(home, ".cardinal")
}

// load reads existing memory from disk
func (ms *MemoryStore) load() error {
	messagesPath := filepath.Join(ms.configDir, "history.jsonl")
	data, err := os.ReadFile(messagesPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // skip malformed lines
		}
		ms.messages = append(ms.messages, msg)
	}

	// Load summaries if they exist
	summariesPath := filepath.Join(ms.configDir, "summaries.json")
	if data, err := os.ReadFile(summariesPath); err == nil {
		json.Unmarshal(data, &ms.summaries)
	}

	return nil
}

// AddMessage adds a new message to the history
func (ms *MemoryStore) AddMessage(role, content, source string) error {
	msg := Message{
		Role:      role,
		Content:   content,
		Source:    source,
		Timestamp: time.Now(),
	}
	ms.messages = append(ms.messages, msg)
	return ms.appendMessage(msg)
}

// appendMessage appends a single message to the history file
func (ms *MemoryStore) appendMessage(msg Message) error {
	messagesPath := filepath.Join(ms.configDir, "history.jsonl")

	// Open file in append mode
	f, err := os.OpenFile(messagesPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = f.WriteString(string(data) + "\n")
	return err
}

// GetMessages returns all messages, optionally filtered
func (ms *MemoryStore) GetMessages(since time.Duration) []Message {
	if since == 0 {
		return ms.messages
	}

	cutoff := time.Now().Add(-since)
	var result []Message
	for _, msg := range ms.messages {
		if msg.Timestamp.After(cutoff) {
			result = append(result, msg)
		}
	}
	return result
}

// GetRecentMessages returns the last N messages
func (ms *MemoryStore) GetRecentMessages(n int) []Message {
	if n <= 0 || len(ms.messages) == 0 {
		return nil
	}

	start := len(ms.messages) - n
	if start < 0 {
		start = 0
	}
	return ms.messages[start:]
}

// Grep searches through message history for a pattern
func (ms *MemoryStore) Grep(pattern string, caseInsensitive bool) []Message {
	var results []Message
	searchPattern := pattern
	if caseInsensitive {
		searchPattern = strings.ToLower(pattern)
	}

	for _, msg := range ms.messages {
		content := msg.Content
		if caseInsensitive {
			content = strings.ToLower(content)
		}
		if strings.Contains(content, searchPattern) {
			results = append(results, msg)
		}
	}
	return results
}

// Compress creates a summary of old messages to reduce context size
func (ms *MemoryStore) Compress(olderThan time.Duration) (int, error) {
	if len(ms.messages) == 0 {
		return 0, nil
	}

	cutoff := time.Now().Add(-olderThan)

	// Find messages to compress
	var toCompress []Message
	var keep []Message
	for _, msg := range ms.messages {
		if msg.Timestamp.Before(cutoff) {
			toCompress = append(toCompress, msg)
		} else {
			keep = append(keep, msg)
		}
	}

	if len(toCompress) == 0 {
		return 0, nil
	}

	// Create summary
	summary := Summary{
		StartTime: toCompress[0].Timestamp,
		EndTime:   toCompress[len(toCompress)-1].Timestamp,
		MessageCount: len(toCompress),
		Content:   generateSummary(toCompress),
	}

	ms.summaries = append(ms.summaries, summary)
	ms.messages = keep

	// Persist summaries
	if err := ms.saveSummaries(); err != nil {
		return 0, err
	}

	// Rewrite messages file with remaining messages
	if err := ms.rewriteMessages(); err != nil {
		return 0, err
	}

	return len(toCompress), nil
}

// GetSummaries returns all compressed summaries
func (ms *MemoryStore) GetSummaries() []Summary {
	return ms.summaries
}

// GetContextForPrompt builds context from recent messages and summaries
func (ms *MemoryStore) GetContextForPrompt(maxMessages int, maxSummaryChars int) string {
	var sb strings.Builder

	// Add summaries first (compressed older context)
	if len(ms.summaries) > 0 {
		sb.WriteString("=== Previous Context (Summarized) ===\n")
		totalSummaryLen := 0
		for _, sum := range ms.summaries {
			if totalSummaryLen + len(sum.Content) > maxSummaryChars {
				break
			}
			sb.WriteString(fmt.Sprintf("[%s] %s\n", sum.StartTime.Format("2006-01-02"), sum.Content))
			totalSummaryLen += len(sum.Content)
		}
		sb.WriteString("\n")
	}

	// Add recent messages
	recent := ms.GetRecentMessages(maxMessages)
	if len(recent) > 0 {
		sb.WriteString("=== Recent Messages ===\n")
		for _, msg := range recent {
			source := msg.Source
			if source == "" {
				source = "unknown"
			}
			sb.WriteString(fmt.Sprintf("[%s][%s] %s\n", source, msg.Role, truncate(msg.Content, 200)))
		}
	}

	return sb.String()
}

func (ms *MemoryStore) saveSummaries() error {
	summariesPath := filepath.Join(ms.configDir, "summaries.json")
	data, err := json.MarshalIndent(ms.summaries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(summariesPath, data, 0644)
}

func (ms *MemoryStore) rewriteMessages() error {
	messagesPath := filepath.Join(ms.configDir, "history.jsonl")

	f, err := os.Create(messagesPath)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, msg := range ms.messages {
		data, _ := json.Marshal(msg)
		f.WriteString(string(data) + "\n")
	}
	return nil
}

func generateSummary(messages []Message) string {
	// Simple summary for now - could be enhanced with LLM
	var roles []string
	var sources []string
	contentLen := 0

	for _, msg := range messages {
		roles = append(roles, msg.Role)
		if msg.Source != "" {
			sources = append(sources, msg.Source)
		}
		contentLen += len(msg.Content)
	}

	uniqueRoles := unique(roles)
	uniqueSources := unique(sources)

	return fmt.Sprintf("%d messages from %s via %s. Total content: %d chars.",
		len(messages),
		strings.Join(uniqueRoles, ", "),
		strings.Join(uniqueSources, ", "),
		contentLen)
}

func unique(slice []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
