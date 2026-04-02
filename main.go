package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

	for {
		ch := client.ChatStreamChannel(cfg.Model, messages, toolDefs)

		var fullContent string
		var toolCalls []api.ToolCall

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
				fmt.Fprintf(os.Stderr, "Error: %v\n", event.Error)
				os.Exit(1)
			}
		}

		if len(toolCalls) == 0 {
			fmt.Println()
			break
		}

		fmt.Println()
		approvals := promptForToolApprovals(toolCalls)
		results := executeToolPlan(working, toolCalls, approvals)

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
