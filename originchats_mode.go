package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"

	"cardinal/pkg/api"
	"cardinal/pkg/config"
	"cardinal/pkg/originchats"
	"cardinal/pkg/tools"
)

// runOriginChats starts the fully autonomous OriginChats AI agent
func runOriginChats(cfg *config.Config, serverURL string) {
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║   Cardinal × OriginChats                 ║")
	fmt.Println("║   Autonomous AI Agent                    ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	// Load or obtain Rotur token
	tokenStore := originchats.NewTokenStore()
	token, err := tokenStore.Load()
	if err != nil {
		fmt.Print("Enter your Rotur auth token: ")
		var input string
		fmt.Scanln(&input)
		token = strings.TrimSpace(input)
		if token == "" {
			fmt.Println("No token provided. Exiting.")
			os.Exit(1)
		}
	} else {
		fmt.Println("Using saved Rotur token")
	}

	// Validate token
	fmt.Print("Validating token... ")
	username, err := originchats.ValidateToken(token)
	if err != nil {
		fmt.Printf("Invalid token: %v\n", err)
		fmt.Print("Enter your Rotur auth token: ")
		var input string
		fmt.Scanln(&input)
		token = strings.TrimSpace(input)
		if token == "" {
			fmt.Println("No token provided. Exiting.")
			os.Exit(1)
		}
		username, err = originchats.ValidateToken(token)
		if err != nil {
			fmt.Printf("Token still invalid: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("authenticated as %s\n", username)

	// Save token for next time
	if err := tokenStore.Save(token, username); err != nil {
		fmt.Printf("Warning: could not save token: %v\n", err)
	} else {
		fmt.Println("Token saved for future use")
	}

	// Determine server URL
	if serverURL == "" {
		serverURL = originchats.DMsServer
	}
	fmt.Printf("Connecting to %s...\n", serverURL)

	// Create and connect OriginChats client
	client := originchats.NewClient(serverURL, token)
	history := originchats.NewMessageHistory(50)
	toolMgr := originchats.NewToolManager(client, history)

	// Set up message handler — this is the core loop
	client.SetOnMessage(func(msg originchats.OriginMessage) {
		history.Add(msg.Channel, msg)
		compressed := originchats.Compress(msg.Content)
		fmt.Printf("📨 [%s in #%s]: %s\n", msg.User, msg.Channel, compressed)
	})

	// Connect
	if err := client.Connect(); err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Printf("Connected to %s as %s!\n", client.ServerName(), client.Username())
	fmt.Println("Agent is running autonomously. Press Ctrl+C to stop.")
	fmt.Println()

	// Fetch initial channels
	channels := client.GetChannels()
	if len(channels) > 0 {
		fmt.Printf("Available channels (%d):\n", len(channels))
		for _, ch := range channels {
			fmt.Printf("  #%s\n", ch.Name)
		}
		fmt.Println()
	}

	// Create the autonomous session
	oc := &originChatsSession{
		cfg:      cfg,
		client:   client,
		history:  history,
		toolMgr:  toolMgr,
		token:    token,
		username: username,
		messages: []api.Message{},
	}

	// Set the message callback to trigger AI processing
	client.SetOnMessage(func(msg originchats.OriginMessage) {
		history.Add(msg.Channel, msg)
		compressed := originchats.Compress(msg.Content)
		fmt.Printf("📨 [%s in #%s]: %s\n", msg.User, msg.Channel, compressed)
		oc.handleIncomingMessage(msg)
	})

	// Block forever — the agent runs autonomously via callbacks
	select {}
}

// originChatsSession holds state for an OriginChats session
type originChatsSession struct {
	cfg        *config.Config
	client     *originchats.Client
	history    *originchats.MessageHistory
	toolMgr    *originchats.ToolManager
	token      string
	username   string
	messages   []api.Message
	lastActive string // last channel with activity
	processing bool   // whether AI is currently processing
}

// handleIncomingMessage decides whether the AI should respond
func (oc *originChatsSession) handleIncomingMessage(msg originchats.OriginMessage) {
	// Don't respond to our own messages
	if msg.User == oc.username {
		return
	}

	// Skip if already processing a message
	if oc.processing {
		fmt.Println("⏳ Skipping — already processing a response")
		return
	}

	// Decide whether to respond
	shouldRespond := false

	// Always respond if directly mentioned
	lower := strings.ToLower(msg.Content)
	if strings.Contains(lower, strings.ToLower(oc.username)) {
		shouldRespond = true
	}
	if strings.HasPrefix(lower, "@cardinal") || strings.HasPrefix(lower, "cardinal,") || strings.HasPrefix(lower, "cardinal ") {
		shouldRespond = true
	}

	// Respond to DMs (every message in a DM channel)
	if oc.client.ServerName() == "" || strings.Contains(oc.client.ServerURL(), "dms.mistium.com") {
		shouldRespond = true
	}

	// Always respond — this is an autonomous agent
	shouldRespond = true

	if !shouldRespond {
		return
	}

	oc.processing = true
	go func() {
		defer func() { oc.processing = false }()
		oc.generateAndSendResponse(msg)
	}()
}

// generateAndSendResponse uses the AI to generate a response and send it
func (oc *originChatsSession) generateAndSendResponse(msg originchats.OriginMessage) {
	// Send typing indicator
	oc.client.SetTyping(msg.Channel)

	// Build context for the AI
	channel := msg.Channel
	oc.lastActive = channel

	formatted := originchats.FormatMessageForAI(msg)
	channelHistory := oc.history.FormatHistory(channel, 20)

	userMsg := fmt.Sprintf("Incoming message:\n%s\n", formatted)
	if channelHistory != "" {
		userMsg += fmt.Sprintf("\nRecent channel context:\n%s\n", channelHistory)
	}
	userMsg += fmt.Sprintf("\nYou are %s on OriginChats. Respond naturally using your originchats_send_message or originchats_send_reply tool. Only use tools to respond — do not just output text.", oc.username)

	oc.messages = append(oc.messages, api.Message{Role: "user", Content: userMsg})
	oc.processAIResponse(channel)
}

// processAIResponse sends messages to the AI and executes tool calls
func (oc *originChatsSession) processAIResponse(targetChannel string) {
	client := api.NewClient(oc.cfg.APIURL, oc.cfg.APIKey)

	// Build tool definitions: standard + originchats
	toolDefs := convertToolDefs(tools.GetToolDefinitions())
	toolDefs = append(toolDefs, convertToolDefs(originchats.GetToolDefinitions())...)

	// Build system prompt
	systemPrompt := oc.buildSystemPrompt()

	messages := append([]api.Message{{Role: "system", Content: systemPrompt}}, oc.messages...)
	messages = compressOriginChatsMessages(messages)

	maxRetries := 5
	retryCount := 0
	baseDelay := 1 * time.Second
	lastToolCalls := ""
	repeatedCallCount := 0

	for {
		ch := client.ChatStreamChannel(oc.cfg.Model, messages, toolDefs, api.CalculateMaxTokens(messages, toolDefs, 128000))
		var fullContent string
		var toolCalls []api.ToolCall
		var streamErr error

		for event := range ch {
			switch event.Type {
			case "content":
				fullContent += event.Content
			case "tool_call":
				if event.Tool != nil && event.Tool.Function.Name != "" {
					toolCalls = append(toolCalls, *event.Tool)
				}
			case "error":
				streamErr = event.Error
			}
		}

		if streamErr != nil {
			if retryCount < maxRetries {
				retryCount++
				delay := min(time.Duration(float64(baseDelay)*math.Pow(2, float64(retryCount-1))), 30*time.Second)
				fmt.Printf("⚠️  Error: %v. Retrying in %v...\n", streamErr, delay)
				time.Sleep(delay)
				continue
			}
			fmt.Printf("❌ Error: %v\n", streamErr)
			break
		}

		retryCount = 0

		// Check for XML-formatted tool calls
		xmlToolCallPattern := regexp.MustCompile(`(<[a-z_]+\s|<tool_call[^>]*name\s*=\s*["'][^"']+["'][^>]*>)`)
		if xmlToolCallPattern.MatchString(fullContent) {
			messages = append(messages, api.Message{
				Role:    "assistant",
				Content: fullContent,
			})
			messages = append(messages, api.Message{
				Role:       "tool",
				ToolCallID: "format_error",
				Content:    "Error: Do not use XML tool calls. Use the proper function calling API.",
			})
			continue
		}

		if len(toolCalls) == 0 {
			// No tool calls — the AI just output text directly.
			// Since we want autonomous behavior, send it as a message.
			content := strings.TrimSpace(fullContent)
			if content != "" && targetChannel != "" {
				content = sanitizeContent(content)
				if len(content) > 2000 {
					content = content[:1997] + "..."
				}
				_, err := oc.client.SendMessage(targetChannel, content)
				if err != nil {
					fmt.Printf("❌ Failed to send: %v\n", err)
				} else {
					fmt.Printf("🤖 → #%s: %s\n", targetChannel, truncateStr(content, 120))
				}
			}
			// Record the exchange
			oc.messages = append(oc.messages, api.Message{Role: "assistant", Content: fullContent})
			break
		}

		// Check for repeated tool calls (loop detection)
		toolCallSummary := ""
		for _, tc := range toolCalls {
			toolCallSummary += tc.Function.Name + ":" + tc.Function.Arguments + "|"
		}
		if toolCallSummary == lastToolCalls {
			repeatedCallCount++
			if repeatedCallCount >= 2 {
				fmt.Println("⚠️  Breaking loop: same tool calls repeated")
				break
			}
		} else {
			repeatedCallCount = 0
		}
		lastToolCalls = toolCallSummary

		// Execute tool calls
		messages = append(messages, api.Message{
			Role:      "assistant",
			Content:   fullContent,
			ToolCalls: toolCalls,
		})

		for _, toolCall := range toolCalls {
			name := toolCall.Function.Name
			args := toolCall.Function.Arguments

			var result string
			if isOriginChatsTool(name) {
				ocResult := oc.toolMgr.Execute(name, args)
				result = originchats.FormatToolResult(ocResult)

				if ocResult.Success {
					fmt.Printf("🔧 %s\n", ocResult.Output)
				} else {
					fmt.Printf("❌ %s: %s\n", name, ocResult.Error)
				}
			} else {
				handler := tools.NewToolHandler(".", nil)
				tResult := handler.Execute(tools.ToolCall{Name: name, Args: args})
				result = tools.FormatToolResult(tResult)
			}

			messages = append(messages, api.Message{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Content:    result,
			})
		}
	}

	// Trim message history to prevent unbounded growth
	if len(oc.messages) > 60 {
		oc.messages = oc.messages[len(oc.messages)-40:]
	}
}

// buildSystemPrompt creates the system prompt for the OriginChats AI
func (oc *originChatsSession) buildSystemPrompt() string {
	var sb strings.Builder

	sb.WriteString("You are Cardinal, an autonomous AI agent on OriginChats (a real-time chat platform).\n")
	sb.WriteString(fmt.Sprintf("Your username is: %s\n\n", oc.username))

	sb.WriteString("You receive messages from users and respond using your tools.\n")
	sb.WriteString("CRITICAL: Always use originchats_send_message or originchats_send_reply to respond. Never just output text — always call a tool.\\n\n")

	sb.WriteString("Available OriginChats tools:\n")
	sb.WriteString("- originchats_send_message: Send a message to a channel\n")
	sb.WriteString("- originchats_send_reply: Reply to a specific message (preferred when responding to someone)\n")
	sb.WriteString("- originchats_edit_message: Edit one of your own messages\n")
	sb.WriteString("- originchats_get_messages: Fetch recent messages from a channel\n")
	sb.WriteString("- originchats_search_messages: Search messages in a channel\n")
	sb.WriteString("- originchats_add_reaction: Add an emoji reaction to a message\n")
	sb.WriteString("- originchats_set_typing: Show typing indicator\n")
	sb.WriteString("- originchats_list_channels: List available channels\n")
	sb.WriteString("- originchats_get_history: View cached message history\n\n")

	sb.WriteString("Guidelines:\n")
	sb.WriteString("- Be helpful, friendly, and conversational\n")
	sb.WriteString("- Use originchats_send_reply when responding to a specific message (it shows the reply thread)\n")
	sb.WriteString("- Keep responses concise — this is a chat, not an essay\n")
	sb.WriteString("- You can use bash, read_file, and other standard tools if needed for tasks\n")
	sb.WriteString("- Avoid @ mentions that ping people (use \"@ username\" with a space instead)\n")
	sb.WriteString("- If someone asks something you can't do, just say so honestly\n\n")

	// Add available channels
	channels := oc.client.GetChannels()
	if len(channels) > 0 {
		sb.WriteString("Available channels:\n")
		for _, ch := range channels {
			sb.WriteString(fmt.Sprintf("- #%s", ch.Name))
			if ch.Description != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", ch.Description))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if oc.lastActive != "" {
		sb.WriteString(fmt.Sprintf("Current channel: #%s\n", oc.lastActive))
	}

	return sb.String()
}

// sanitizeContent prevents @ mention pings
func sanitizeContent(content string) string {
	return strings.ReplaceAll(content, "@", "@ ")
}

// isOriginChatsTool checks if a tool name belongs to OriginChats
func isOriginChatsTool(name string) bool {
	return strings.HasPrefix(name, "originchats_")
}

// compressOriginChatsMessages compresses messages to fit within context limits
func compressOriginChatsMessages(messages []api.Message) []api.Message {
	contextLimit := 128000
	threshold := int(float64(contextLimit) * 0.8)

	if estimateOCMessages(messages) < threshold {
		return messages
	}

	keepRecent := 10
	summaryEnd := len(messages) - keepRecent
	if summaryEnd <= 1 {
		return messages
	}

	var summary strings.Builder
	summary.WriteString("Previous conversation summary:\n")
	for i := 1; i < summaryEnd; i++ {
		msg := messages[i]
		content := msg.Content
		if len(content) > 150 {
			content = content[:150] + "..."
		}
		content = strings.ReplaceAll(content, "\n", " ")
		summary.WriteString(fmt.Sprintf("> %s: %s\n", msg.Role, content))
	}

	compressed := []api.Message{
		messages[0],
		{Role: "system", Content: summary.String()},
	}
	compressed = append(compressed, messages[summaryEnd:]...)

	return compressed
}

func estimateOCMessages(messages []api.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)/4 + 10
	}
	return total
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// convertToolDefsOC converts OriginChats tool defs to api.Tool
func convertToolDefsOC(defs []any) []api.Tool {
	var result []api.Tool
	for _, def := range defs {
		data, _ := json.Marshal(def)
		var tool api.Tool
		_ = json.Unmarshal(data, &tool)
		result = append(result, tool)
	}
	return result
}
