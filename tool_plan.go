package main

import (
	"cardinal/pkg/api"
	"cardinal/pkg/tools"
)

func defaultToolApprovals(toolCalls []api.ToolCall) []bool {
	approvals := make([]bool, len(toolCalls))
	for i, toolCall := range toolCalls {
		approvals[i] = !tools.RequiresApproval(toolCall.Function.Name)
	}
	return approvals
}

func hasPendingApprovals(toolCalls []api.ToolCall) bool {
	for _, toolCall := range toolCalls {
		if tools.RequiresApproval(toolCall.Function.Name) {
			return true
		}
	}
	return false
}

func executeToolPlan(working string, toolCalls []api.ToolCall, approvals []bool) []tools.ToolResult {
	handler := tools.NewToolHandler(working)
	results := make([]tools.ToolResult, 0, len(toolCalls))

	for i, toolCall := range toolCalls {
		approved := i < len(approvals) && approvals[i]
		if !approved {
			results = append(results, tools.PermissionDeniedResult(toolCall.Function.Name))
			continue
		}
		results = append(results, handler.Execute(tools.ToolCall{
			Name: toolCall.Function.Name,
			Args: toolCall.Function.Arguments,
		}))
	}

	return results
}

func approvedToolCount(approvals []bool) int {
	count := 0
	for _, approved := range approvals {
		if approved {
			count++
		}
	}
	return count
}

func pluralize(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
