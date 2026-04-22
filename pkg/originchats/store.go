package originchats

import (
	"fmt"
	"strings"
	"sync"

	"cardinal/pkg/storage"
)

// TokenStore manages persistent storage of the Rotur token
// It uses the shared ~/.cardinal settings file
type TokenStore struct {
	mu sync.Mutex
}

// NewTokenStore creates a new token store
func NewTokenStore() *TokenStore {
	return &TokenStore{}
}

// Load retrieves the saved token from the cardinal settings file
func (ts *TokenStore) Load() (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	token, _, err := storage.LoadRoturToken()
	if err != nil {
		return "", fmt.Errorf("no saved Rotur token: %w", err)
	}
	return token, nil
}

// Save persists the token in the cardinal settings file
func (ts *TokenStore) Save(token, username string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	return storage.SaveRoturToken(token, username)
}

// Delete removes the saved token
func (ts *TokenStore) Delete() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	return storage.SaveRoturToken("", "")
}

// MessageHistory maintains a rolling buffer of recent messages per channel
type MessageHistory struct {
	mu       sync.RWMutex
	channels map[string][]OriginMessage
	maxPerCh int
}

// NewMessageHistory creates a new message history buffer
func NewMessageHistory(maxPerChannel int) *MessageHistory {
	return &MessageHistory{
		channels: make(map[string][]OriginMessage),
		maxPerCh: maxPerChannel,
	}
}

// Add adds a message to the channel history
func (mh *MessageHistory) Add(channel string, msg OriginMessage) {
	mh.mu.Lock()
	defer mh.mu.Unlock()

	msgs := mh.channels[channel]
	msgs = append(msgs, msg)

	// Trim to max size
	if len(msgs) > mh.maxPerCh {
		msgs = msgs[len(msgs)-mh.maxPerCh:]
	}

	mh.channels[channel] = msgs
}

// Get returns recent messages for a channel
func (mh *MessageHistory) Get(channel string, limit int) []OriginMessage {
	mh.mu.RLock()
	defer mh.mu.RUnlock()

	msgs := mh.channels[channel]
	if limit <= 0 || limit > len(msgs) {
		return msgs
	}
	return msgs[len(msgs)-limit:]
}

// FormatHistory returns a formatted string of recent messages for the AI
func (mh *MessageHistory) FormatHistory(channel string, limit int) string {
	msgs := mh.Get(channel, limit)
	if len(msgs) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, msg := range msgs {
		username := msg.User
		if username == "" {
			username = "unknown"
		}
		content := msg.Content
		if content == "" {
			continue
		}

		sb.WriteString(fmt.Sprintf("%s: %s", username, content))

		if msg.ReplyTo != nil {
			sb.WriteString(fmt.Sprintf(" (reply to %s: \"%s\")", msg.ReplyTo.User, msg.ReplyTo.Content))
		}

		if msg.Webhook != nil && msg.Webhook.Name != "" {
			sb.WriteString(fmt.Sprintf(" [via webhook: %s]", msg.Webhook.Name))
		}

		sb.WriteString(fmt.Sprintf(" [id:%s]\n", msg.ID))
	}

	return sb.String()
}

// Compress compresses message content for token efficiency
func Compress(content string) string {
	// Remove excessive whitespace
	content = strings.ReplaceAll(content, "  ", " ")
	content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
	content = strings.TrimSpace(content)

	// Truncate very long messages to a reasonable size
	if len(content) > 4000 {
		content = content[:3997] + "..."
	}

	return content
}

// FormatMessageForAI formats an incoming message for the AI context
func FormatMessageForAI(msg OriginMessage) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("[%s in #%s]", msg.User, msg.Channel))

	if msg.ReplyTo != nil {
		sb.WriteString(fmt.Sprintf(" (replying to %s: \"%s\")", msg.ReplyTo.User, truncate(msg.ReplyTo.Content, 100)))
	}

	if msg.Webhook != nil && msg.Webhook.Name != "" {
		sb.WriteString(fmt.Sprintf(" [webhook: %s]", msg.Webhook.Name))
	}

	sb.WriteString(fmt.Sprintf(": %s", msg.Content))
	return sb.String()
}

// truncate shortens a string
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
