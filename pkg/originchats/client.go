package originchats

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Server URLs for OriginChats
const (
	ChatsServer  = "wss://chats.mistium.com"
	DMsServer    = "wss://dms.mistium.com"
	RoturAPIBase = "https://api.rotur.dev"
)

// Client represents an OriginChats WebSocket client
type Client struct {
	conn          *websocket.Conn
	serverURL     string
	token         string
	username      string
	userID        string
	validatorKey  string
	validator     string
	authenticated bool
	ready         bool

	// Single read loop routes messages here
	inboundCh     chan map[string]any
	done          chan struct{}
	closeOnce     sync.Once

	// Message handling callbacks
	onMessage        func(msg OriginMessage)
	onUserConnect    func(username string)
	onUserDisconnect func(username string)
	onReady          func(user OriginUser)

	// Pending listener tracking (for request-response patterns like send, get)
	pendingMu sync.Mutex
	pending   map[string]chan map[string]any

	// Channels cache
	channelsMu sync.RWMutex
	channels   []OriginChannel

	// Connection management
	reconnect bool
	closed    bool

	// Server info
	serverName   string
	capabilities []string
}

// OriginMessage represents a chat message
type OriginMessage struct {
	ID        string         `json:"id"`
	Channel   string         `json:"channel"`
	User      string         `json:"user"`
	Content   string         `json:"content"`
	Timestamp float64        `json:"timestamp"`
	ReplyTo   *OriginReplyTo `json:"reply_to,omitempty"`
	Reactions map[string]any `json:"reactions,omitempty"`
	Embeds    []any          `json:"embeds,omitempty"`
	Webhook   *OriginWebhook `json:"webhook,omitempty"`
}

// OriginReplyTo represents a reply target
type OriginReplyTo struct {
	ID      string `json:"id"`
	User    string `json:"user"`
	Content string `json:"content"`
}

// OriginWebhook represents webhook info on a message
type OriginWebhook struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

// OriginUser represents user data from the ready event
type OriginUser struct {
	Username string   `json:"username"`
	ID       string   `json:"id"`
	Roles    []string `json:"roles"`
}

// OriginChannel represents a channel
type OriginChannel struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Category    string `json:"category"`
}

// NewClient creates a new OriginChats client
func NewClient(serverURL, token string) *Client {
	return &Client{
		serverURL: serverURL,
		token:     token,
		inboundCh: make(chan map[string]any, 200),
		done:      make(chan struct{}),
		pending:   make(map[string]chan map[string]any),
		reconnect: true,
	}
}

// Connect establishes a WebSocket connection and authenticates
func (c *Client) Connect() error {
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(c.serverURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.serverURL, err)
	}

	c.conn = conn
	c.inboundCh = make(chan map[string]any, 200)
	c.done = make(chan struct{})

	// Start the single read loop — only goroutine that reads from the socket
	go c.readLoop()

	// Wait for handshake
	msg, err := c.waitForCmd("handshake", 10*time.Second)
	if err != nil {
		return fmt.Errorf("handshake timeout: %w", err)
	}
	c.handleHandshake(msg)

	// Generate validator and authenticate
	if err := c.authenticate(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Wait for auth success
	_, err = c.waitForCmd("auth_success", 10*time.Second)
	if err != nil {
		return fmt.Errorf("auth success timeout: %w", err)
	}

	// Wait for ready
	msg, err = c.waitForCmd("ready", 10*time.Second)
	if err != nil {
		return fmt.Errorf("ready timeout: %w", err)
	}

	c.authenticated = true
	c.ready = true

	// Parse user from ready
	if userRaw, ok := msg["user"].(map[string]any); ok {
		c.username, _ = userRaw["username"].(string)
		if id, ok := userRaw["id"].(string); ok {
			c.userID = id
		}
	}

	if c.onReady != nil {
		user := OriginUser{
			Username: c.username,
			ID:       c.userID,
		}
		if rolesRaw, ok := msg["user"].(map[string]any); ok {
			if r, ok := rolesRaw["roles"].([]any); ok {
				for _, role := range r {
					if rs, ok := role.(string); ok {
						user.Roles = append(user.Roles, rs)
					}
				}
			}
		}
		c.onReady(user)
	}

	// Request channels
	c.Send(map[string]any{"cmd": "channels_get"})

	// Start the dispatcher loop to route inbound messages to callbacks
	go c.dispatchLoop()

	return nil
}

// authenticate generates a validator and sends auth command
func (c *Client) authenticate() error {
	validator, err := c.generateValidator()
	if err != nil {
		return err
	}
	c.validator = validator
	return c.Send(map[string]any{
		"cmd":       "auth",
		"validator": validator,
	})
}

// generateValidator calls the Rotur API to generate a validator from the token + validator_key
func (c *Client) generateValidator() (string, error) {
	url := fmt.Sprintf("%s/generate_validator?key=%s&auth=%s",
		RoturAPIBase, c.validatorKey, c.token)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to call generate_validator: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read validator response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("validator API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Validator string `json:"validator"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse validator response: %w", err)
	}

	if result.Validator == "" {
		return "", fmt.Errorf("empty validator returned from API")
	}

	return result.Validator, nil
}

// Send sends a JSON message to the server
func (c *Client) Send(msg map[string]any) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// SendMessage sends a message to a channel and waits for the echo
func (c *Client) SendMessage(channel, content string) (*OriginMessage, error) {
	listener := randomString(20)
	resultCh := make(chan map[string]any, 1)

	c.pendingMu.Lock()
	c.pending[listener] = resultCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, listener)
		c.pendingMu.Unlock()
	}()

	err := c.Send(map[string]any{
		"cmd":      "message_new",
		"channel":  channel,
		"content":  content,
		"listener": listener,
	})
	if err != nil {
		return nil, err
	}

	// Wait for echo
	select {
	case msg := <-resultCh:
		originMsg := parseOriginMessage(msg)
		return &originMsg, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout waiting for message echo")
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// SendReply sends a reply to a specific message
func (c *Client) SendReply(channel, replyToID, content string) (*OriginMessage, error) {
	listener := randomString(20)
	resultCh := make(chan map[string]any, 1)

	c.pendingMu.Lock()
	c.pending[listener] = resultCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, listener)
		c.pendingMu.Unlock()
	}()

	err := c.Send(map[string]any{
		"cmd":      "message_new",
		"channel":  channel,
		"content":  content,
		"reply_to": replyToID,
		"listener": listener,
	})
	if err != nil {
		return nil, err
	}

	select {
	case msg := <-resultCh:
		originMsg := parseOriginMessage(msg)
		return &originMsg, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout waiting for reply echo")
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// EditMessage edits a message
func (c *Client) EditMessage(channel, messageID, content string) error {
	return c.Send(map[string]any{
		"cmd":     "message_edit",
		"channel": channel,
		"id":      messageID,
		"content": content,
	})
}

// DeleteMessage deletes a message
func (c *Client) DeleteMessage(channel, messageID string) error {
	return c.Send(map[string]any{
		"cmd":     "message_delete",
		"channel": channel,
		"id":      messageID,
	})
}

// AddReaction adds a reaction to a message
func (c *Client) AddReaction(channel, messageID, emoji string) error {
	return c.Send(map[string]any{
		"cmd":     "message_react_add",
		"channel": channel,
		"id":      messageID,
		"emoji":   emoji,
	})
}

// GetMessages fetches recent messages from a channel
func (c *Client) GetMessages(channel string, limit int) ([]OriginMessage, error) {
	listener := randomString(20)
	resultCh := make(chan map[string]any, 1)

	c.pendingMu.Lock()
	c.pending[listener] = resultCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, listener)
		c.pendingMu.Unlock()
	}()

	err := c.Send(map[string]any{
		"cmd":      "messages_get",
		"channel":  channel,
		"limit":    limit,
		"listener": listener,
	})
	if err != nil {
		return nil, err
	}

	select {
	case msg := <-resultCh:
		return parseMessagesResponse(msg), nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout waiting for messages")
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// SearchMessages searches messages in a channel
func (c *Client) SearchMessages(channel, query string) ([]OriginMessage, error) {
	listener := randomString(20)
	resultCh := make(chan map[string]any, 1)

	c.pendingMu.Lock()
	c.pending[listener] = resultCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, listener)
		c.pendingMu.Unlock()
	}()

	err := c.Send(map[string]any{
		"cmd":      "messages_search",
		"channel":  channel,
		"query":    query,
		"listener": listener,
	})
	if err != nil {
		return nil, err
	}

	select {
	case msg := <-resultCh:
		return parseMessagesResponse(msg), nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout waiting for search results")
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// SetTyping sends a typing indicator
func (c *Client) SetTyping(channel string) error {
	return c.Send(map[string]any{
		"cmd":     "typing",
		"channel": channel,
	})
}

// GetChannels returns the cached channel list
func (c *Client) GetChannels() []OriginChannel {
	c.channelsMu.RLock()
	defer c.channelsMu.RUnlock()
	return c.channels
}

// SetOnMessage sets the callback for incoming messages
func (c *Client) SetOnMessage(fn func(msg OriginMessage)) {
	c.onMessage = fn
}

// SetOnUserConnect sets the callback for user connect events
func (c *Client) SetOnUserConnect(fn func(username string)) {
	c.onUserConnect = fn
}

// SetOnUserDisconnect sets the callback for user disconnect events
func (c *Client) SetOnUserDisconnect(fn func(username string)) {
	c.onUserDisconnect = fn
}

// SetOnReady sets the callback for the ready event
func (c *Client) SetOnReady(fn func(user OriginUser)) {
	c.onReady = fn
}

// Username returns the authenticated username
func (c *Client) Username() string {
	return c.username
}

// UserID returns the authenticated user ID
func (c *Client) UserID() string {
	return c.userID
}

// ServerName returns the server name from handshake
func (c *Client) ServerName() string {
	return c.serverName
}

// ServerURL returns the URL of the connected server
func (c *Client) ServerURL() string {
	return c.serverURL
}

// IsReady returns whether the client is ready
func (c *Client) IsReady() bool {
	return c.ready
}

// Close closes the connection
func (c *Client) Close() {
	c.closed = true
	c.reconnect = false
	c.closeOnce.Do(func() {
		if c.conn != nil {
			c.conn.Close()
		}
		if c.done != nil {
			close(c.done)
		}
	})
}

// readLoop is the ONLY goroutine that reads from the WebSocket connection.
// All messages are pushed to inboundCh for routing.
func (c *Client) readLoop() {
	defer func() {
		c.closeOnce.Do(func() {
			if c.done != nil {
				close(c.done)
			}
		})
	}()

	for {
		select {
		case <-c.done:
			return
		default:
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if !c.closed {
				c.closeOnce.Do(func() {
					if c.done != nil {
						close(c.done)
					}
				})
			}
			return
		}

		var msg map[string]any
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		// Push everything to the inbound channel
		select {
		case c.inboundCh <- msg:
		case <-c.done:
			return
		default:
			// Channel full — drop oldest
			select {
			case <-c.inboundCh:
			default:
			}
			c.inboundCh <- msg
		}
	}
}

// dispatchLoop routes inbound messages to callbacks
func (c *Client) dispatchLoop() {
	for {
		select {
		case msg := <-c.inboundCh:
			c.routeMessage(msg)
		case <-c.done:
			return
		}
	}
}

// routeMessage dispatches a single message to the appropriate handler
func (c *Client) routeMessage(msg map[string]any) {
	// Check for listener-based responses first (for request-response patterns)
	if listener, ok := msg["listener"].(string); ok && listener != "" {
		c.pendingMu.Lock()
		if ch, exists := c.pending[listener]; exists {
			ch <- msg
			delete(c.pending, listener)
			c.pendingMu.Unlock()
			return
		}
		c.pendingMu.Unlock()
	}

	cmd, _ := msg["cmd"].(string)

	switch cmd {
	case "message_new":
		c.handleMessageNew(msg)
	case "user_connect":
		if user, ok := msg["user"].(map[string]any); ok {
			if username, ok := user["username"].(string); ok {
				if c.onUserConnect != nil {
					c.onUserConnect(username)
				}
			}
		}
	case "user_disconnect":
		if username, ok := msg["username"].(string); ok {
			if c.onUserDisconnect != nil {
				c.onUserDisconnect(username)
			}
		}
	case "channels_get":
		c.handleChannelsGet(msg)
	case "error":
		val, _ := msg["val"]
		fmt.Printf("[OriginChats] Server error: %v\n", val)
	}
}

// handleHandshake processes the handshake message
func (c *Client) handleHandshake(msg map[string]any) {
	val, _ := msg["val"].(map[string]any)
	if val == nil {
		return
	}

	if server, ok := val["server"].(map[string]any); ok {
		c.serverName, _ = server["name"].(string)
	}

	c.validatorKey, _ = val["validator_key"].(string)

	if caps, ok := val["capabilities"].([]any); ok {
		c.capabilities = make([]string, 0, len(caps))
		for _, cap := range caps {
			if s, ok := cap.(string); ok {
				c.capabilities = append(c.capabilities, s)
			}
		}
	}
}

// handleMessageNew processes a new message event
func (c *Client) handleMessageNew(msg map[string]any) {
	if c.onMessage == nil {
		return
	}

	originMsg := parseOriginMessage(msg)

	// Don't echo our own messages (handled by listener pattern)
	if originMsg.User == c.username {
		return
	}

	c.onMessage(originMsg)
}

// handleChannelsGet processes the channel list response
func (c *Client) handleChannelsGet(msg map[string]any) {
	channels, _ := msg["channels"].([]any)
	if channels == nil {
		return
	}

	c.channelsMu.Lock()
	defer c.channelsMu.Unlock()

	c.channels = make([]OriginChannel, 0, len(channels))
	for _, ch := range channels {
		if chMap, ok := ch.(map[string]any); ok {
			oc := OriginChannel{}
			oc.Name, _ = chMap["name"].(string)
			oc.Description, _ = chMap["description"].(string)
			oc.Type, _ = chMap["type"].(string)
			oc.Category, _ = chMap["category"].(string)
			c.channels = append(c.channels, oc)
		}
	}
}

// waitForCmd waits for a specific command from the inbound channel.
// This is used during the connection handshake before dispatchLoop is running.
func (c *Client) waitForCmd(targetCmd string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		select {
		case msg := <-c.inboundCh:
			cmd, _ := msg["cmd"].(string)

			if cmd == targetCmd {
				return msg, nil
			}

			// Handle handshake inline during connection setup
			if cmd == "handshake" {
				c.handleHandshake(msg)
			}

			// If we get auth_error while waiting for auth_success, fail fast
			if cmd == "auth_error" && targetCmd == "auth_success" {
				val, _ := msg["val"].(string)
				return nil, fmt.Errorf("authentication error: %s", val)
			}

			// Discard other messages during handshake

		case <-time.After(remaining):
			return nil, fmt.Errorf("timeout waiting for %s", targetCmd)
		case <-c.done:
			return nil, fmt.Errorf("connection closed while waiting for %s", targetCmd)
		}
	}
	return nil, fmt.Errorf("timeout waiting for %s", targetCmd)
}

// parseOriginMessage converts a raw message map to an OriginMessage
func parseOriginMessage(msg map[string]any) OriginMessage {
	m := OriginMessage{}
	m.ID, _ = msg["id"].(string)
	m.Channel, _ = msg["channel"].(string)

	// Handle user field — could be string or object
	switch u := msg["user"].(type) {
	case string:
		m.User = u
	case map[string]any:
		m.User, _ = u["username"].(string)
	}

	// Handle content field — could be in msg or msg.message
	switch content := msg["content"].(type) {
	case string:
		m.Content = content
	default:
		// Try message sub-object (server broadcasts wrap in "message")
		if msgObj, ok := msg["message"].(map[string]any); ok {
			m.Content, _ = msgObj["content"].(string)
			m.ID, _ = msgObj["id"].(string)
			switch u := msgObj["user"].(type) {
			case string:
				m.User = u
			case map[string]any:
				m.User, _ = u["username"].(string)
			}
		}
	}

	m.Timestamp, _ = msg["timestamp"].(float64)

	// Parse reply_to
	if replyTo, ok := msg["reply_to"].(map[string]any); ok {
		m.ReplyTo = &OriginReplyTo{
			ID:      toString(replyTo["id"]),
			User:    toString(replyTo["user"]),
			Content: toString(replyTo["content"]),
		}
	}

	// Parse reactions
	if reactions, ok := msg["reactions"].(map[string]any); ok {
		m.Reactions = reactions
	}

	// Parse webhook
	if webhook, ok := msg["webhook"].(map[string]any); ok {
		m.Webhook = &OriginWebhook{
			ID:     toString(webhook["id"]),
			Name:   toString(webhook["name"]),
			Avatar: toString(webhook["avatar"]),
		}
	}

	// Parse embeds
	if embeds, ok := msg["embeds"].([]any); ok {
		m.Embeds = embeds
	}

	return m
}

// parseMessagesResponse parses a messages_get response
func parseMessagesResponse(msg map[string]any) []OriginMessage {
	messages, _ := msg["messages"].([]any)
	if messages == nil {
		return nil
	}

	result := make([]OriginMessage, 0, len(messages))
	for _, m := range messages {
		if mMap, ok := m.(map[string]any); ok {
			result = append(result, parseOriginMessage(mMap))
		}
	}
	return result
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}

// randomString generates a random alphanumeric string
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	now := time.Now().UnixNano()
	for i := range b {
		now = time.Now().UnixNano()
		b[i] = charset[now%int64(len(charset))]
	}
	return string(b)
}

// ValidateToken checks if a Rotur token is valid by calling /me
func ValidateToken(token string) (string, error) {
	url := fmt.Sprintf("%s/me?auth=%s", RoturAPIBase, token)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to validate token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read validation response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("invalid token (status %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse validation response: %w", err)
	}

	username, _ := result["username"].(string)
	if username == "" {
		return "", fmt.Errorf("token valid but no username returned")
	}

	return username, nil
}

// generateValidatorLocal generates a validator locally using HMAC-SHA256
// This is a fallback if the API call fails
func generateValidatorLocal(validatorKey, token string) (string, error) {
	if validatorKey == "" || token == "" {
		return "", fmt.Errorf("validator_key or token is empty")
	}
	mac := hmac.New(sha256.New, []byte(validatorKey))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Ensure unused import satisfaction
var _ = strings.TrimSpace
var _ = generateValidatorLocal
