package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cardinal/pkg/api"
	"cardinal/pkg/config"
	"cardinal/pkg/tools"
)

func convertToolDefs(defs []any) []api.Tool {
	var result []api.Tool
	for _, def := range defs {
		data, _ := json.Marshal(def)
		var tool api.Tool
		_ = json.Unmarshal(data, &tool)
		result = append(result, tool)
	}
	return result
}

func main() {
	modelFlag := flag.String("model", "", "model to use for this session")
	flag.Parse()

	cfg := config.Load()

	if *modelFlag != "" {
		cfg.Model = *modelFlag
	}

	args := flag.Args()
	if len(args) > 0 {
		switch args[0] {
		case "install":
			runInstall()
		case "run":
			runCLI(cfg, strings.Join(args[1:], " "))
		case "originchats":
			serverURL := ""
			if len(args) > 1 {
				serverURL = args[1]
			}
			runOriginChats(cfg, serverURL)
		}
		return
	}

	p := tea.NewProgram(NewModel(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runInstall() {
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting executable path: %v\n", err)
		os.Exit(1)
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving symlinks: %v\n", err)
		os.Exit(1)
	}

	targetPath := "/usr/local/bin/cardinal"

	execData, err := os.ReadFile(execPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading executable: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(targetPath, execData, 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to %s: %v\n", targetPath, err)
		fmt.Println("\nYou may need to run this command with sudo:")
		fmt.Println("  sudo cardinal /install")
		os.Exit(1)
	}

	fmt.Println("✓ Cardinal installed successfully!")
	fmt.Printf("  Location: %s\n", targetPath)
	fmt.Println("\nYou can now run 'cardinal' from anywhere.")
}

func runCLI(cfg *config.Config, prompt string) {
	working, _ := os.Getwd()
	systemPrompt := cfg.SystemPrompt + "\n\nWorking directory: " + working
	systemPrompt += "\n\nWhen using tools, you MUST use the standard function calling format with JSON arguments. Do NOT use XML tags like <tool_call>. Use the provided tool definitions through the proper API function calling mechanism."

	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	client := api.NewClient(cfg.APIURL, cfg.APIKey)
	toolDefs := convertToolDefs(tools.GetToolDefinitions())

	fmt.Printf("Cardinal [%s]\n\n", cfg.ActiveProfileName())

	maxRetries := 5
	retryCount := 0
	baseDelay := 1 * time.Second
	lastToolCalls := ""
	repeatedCallCount := 0

	for {
		messages = compressMessagesCLI(messages)

		maxTokens := api.CalculateMaxTokens(messages, toolDefs, 128000)
		if maxTokens > 16384 {
			maxTokens = 16384
		}
		ch := client.ChatStreamChannel(cfg.Model, messages, toolDefs, maxTokens)

		var fullContent string
		var toolCalls []api.ToolCall
		var streamErr error

		for event := range ch {
			switch event.Type {
			case "content":
				fmt.Print(event.Content)
				fullContent += event.Content
			case "thinking":
				// Thinking content is captured but not displayed in CLI mode
			case "tool_call":
				if event.Tool != nil && event.Tool.Function.Name != "" {
					toolCalls = append(toolCalls, *event.Tool)
				}
			case "error":
				streamErr = event.Error
			}
		}

		if streamErr != nil {
			logBadRequestError(streamErr, cfg.Model, messages, toolDefs)

			if shouldRetryCLI(streamErr, retryCount, maxRetries) {
				retryCount++
				delay := min(time.Duration(float64(baseDelay)*math.Pow(2, float64(retryCount-1))), 30*time.Second)
				fmt.Fprintf(os.Stderr, "\nError: %v. Retrying in %v (attempt %d/%d)...\n", formatCLIError(streamErr), delay, retryCount, maxRetries)
				time.Sleep(delay)
				continue
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", streamErr)
			os.Exit(1)
		}

		retryCount = 0

		// Check for XML-formatted tool calls in content
		xmlToolCallPattern := regexp.MustCompile(`(<tool_call>[a-z_]+\s|<tool_call[^>]*name\s*=\s*["'][^"']+["'][^>]*>)`)
		if xmlToolCallPattern.MatchString(fullContent) {
			messages = append(messages, api.Message{
				Role:    "assistant",
				Content: fullContent,
			})
			messages = append(messages, api.Message{
				Role:       "tool",
				ToolCallID: "format_error",
				Content:    "Error: Your message was ignored because it contained XML-formatted tool calls. You MUST use the proper function calling API with JSON format. Do NOT write tool calls as text in your message. The system will handle tool execution for you. Just respond normally and the tools will be called automatically through the API.",
			})
			fmt.Println("\n[Warning: XML tool call format detected - message ignored]")
			continue
		}

		if len(toolCalls) == 0 {
			fmt.Println()
			break
		}

		// Check for repeated identical tool calls to break loops
		toolCallSummary := ""
		for _, tc := range toolCalls {
			toolCallSummary += tc.Function.Name + ":" + tc.Function.Arguments + "|"
		}
		if toolCallSummary == lastToolCalls {
			repeatedCallCount++
			if repeatedCallCount >= 2 {
				fmt.Println("\n[Breaking loop: same tool calls repeated]")
				break
			}
		} else {
			repeatedCallCount = 0
		}
		lastToolCalls = toolCallSummary

		fmt.Println()
		approvals := promptForToolApprovals(toolCalls)
		results := executeToolPlan(working, toolCalls, approvals, nil)

		messages = append(messages, api.Message{
			Role:      "assistant",
			Content:   fullContent,
			ToolCalls: toolCalls,
		})

		for i, toolCall := range toolCalls {
			formatted := tools.FormatToolResultCLI(results[i], toolCall.Function.Name, toolCall.Function.Arguments)
			fmt.Println(formatted)
			messages = append(messages, api.Message{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Content:    tools.FormatToolResult(results[i]),
			})
		}

		fmt.Println()
	}
}

func promptForToolApprovals(toolCalls []api.ToolCall) []bool {
	approvals := defaultToolApprovals(toolCalls)
	reader := bufio.NewReader(os.Stdin)

	for i, toolCall := range toolCalls {
		if approvals[i] {
			continue
		}

		fmt.Printf("Approve %s?\n%s\nAllow? [y/N]: ", toolCall.Function.Name, tools.SummarizeCall(toolCall.Function.Name, toolCall.Function.Arguments))
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))
		approvals[i] = response == "y" || response == "yes"
	}

	return approvals
}

func compressMessagesCLI(messages []api.Message) []api.Message {
	contextLimit := 128000

	threshold := int(float64(contextLimit) * 0.8)
	if estimateTokensCLI(messages) < threshold {
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
		// Include thinking in the summary if present
		if msg.Thinking != "" {
			content = "<thinking>" + msg.Thinking + "</thinking> " + content
		}
		if len(content) > 150 {
			content = content[:150] + "..."
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

func estimateTokensCLI(messages []api.Message) int {
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

func shouldRetryCLI(err error, retryCount, maxRetries int) bool {
	if err == nil || retryCount >= maxRetries {
		return false
	}

	if apiErr, ok := err.(*api.APIError); ok {
		switch apiErr.StatusCode {
		case 408, 429, 500, 502, 503, 504:
			return true
		}
	}

	return true
}

func formatCLIError(err error) string {
	if err == nil {
		return ""
	}

	if apiErr, ok := err.(*api.APIError); ok {
		switch apiErr.StatusCode {
		case 429:
			return "Rate limited"
		case 500:
			return "Server error"
		case 502:
			return "Bad gateway"
		case 503:
			return "Service unavailable"
		case 504:
			return "Gateway timeout"
		default:
			return fmt.Sprintf("API error (%d): %s", apiErr.StatusCode, apiErr.Message)
		}
	}

	return err.Error()
}
