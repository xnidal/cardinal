package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cardinal/pkg/api"
	"cardinal/pkg/config"
	"cardinal/pkg/tools"
)

func convertToolDefs(defs []interface{}) []api.Tool {
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
	cfg := config.Load()

	if len(os.Args) > 1 {
		runCLI(cfg, strings.Join(os.Args[1:], " "))
		return
	}

	p := tea.NewProgram(NewModel(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(cfg *config.Config, prompt string) {
	working, _ := os.Getwd()
	systemPrompt := cfg.SystemPrompt + "\n\nWorking directory: " + working

	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	client := api.NewClient(cfg.APIURL, cfg.APIKey)
	toolDefs := convertToolDefs(tools.GetToolDefinitions())

	fmt.Printf("Cardinal [%s]\n\n", cfg.ActiveProfileName())

	maxRetries := 3
	retryCount := 0

	for {
		messages = compressMessagesCLI(messages)

		ch := client.ChatStreamChannel(cfg.Model, messages, toolDefs)

		var fullContent string
		var toolCalls []api.ToolCall
		var streamErr error

		for event := range ch {
			switch event.Type {
			case "content":
				fmt.Print(event.Content)
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
			if apiErr, ok := streamErr.(*api.APIError); ok && apiErr.StatusCode == 429 && retryCount < maxRetries {
				retryCount++
				waitTime := time.Duration(retryCount) * 2
				fmt.Fprintf(os.Stderr, "\nRate limited. Retrying in %ds (attempt %d/%d)...\n", waitTime, retryCount, maxRetries)
				time.Sleep(waitTime * time.Second)
				messages = []api.Message{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: prompt},
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", streamErr)
			os.Exit(1)
		}

		retryCount = 0

		if len(toolCalls) == 0 {
			fmt.Println()
			break
		}

		fmt.Println()
		approvals := promptForToolApprovals(toolCalls)
		results := executeToolPlan(working, toolCalls, approvals, nil)

		messages = append(messages, api.Message{
			Role:      "assistant",
			Content:   fullContent,
			ToolCalls: toolCalls,
		})

		for i, toolCall := range toolCalls {
			fmt.Printf("\n[Tool: %s]\n", toolCall.Function.Name)
			formatted := tools.FormatToolResult(results[i])
			fmt.Println(formatted)
			messages = append(messages, api.Message{
				Role:    "tool",
				Name:    toolCall.Function.Name,
				Content: formatted,
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
		if len(content) > 150 {
			content = content[:150] + "..."
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

func estimateTokensCLI(messages []api.Message) int {
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
