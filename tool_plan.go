package main

import (
	"cardinal/pkg/api"
	"cardinal/pkg/storage"
	"cardinal/pkg/tools"
)

func defaultToolApprovals(toolCalls []api.ToolCall) []bool {
	approvals := make([]bool, len(toolCalls))
	for i, toolCall := range toolCalls {
		approvals[i] = !tools.RequiresApproval(toolCall.Function.Name)
	}
	return approvals
}

func executeToolPlan(working string, todos *storage.TodoStore, toolCalls []api.ToolCall, approvals []bool, onEditSoul func()) []tools.ToolResult {
	handler := tools.NewToolHandlerWithTodos(working, onEditSoul, todos)

	// Filter to only approved calls, but maintain indices for results
	approvedCalls := make([]tools.ToolCall, 0, len(toolCalls))
	approvedIndices := make([]int, 0, len(toolCalls))
	for i, toolCall := range toolCalls {
		approved := i < len(approvals) && approvals[i]
		if approved {
			approvedCalls = append(approvedCalls, tools.ToolCall{
				Name: toolCall.Function.Name,
				Args: toolCall.Function.Arguments,
			})
			approvedIndices = append(approvedIndices, i)
		}
	}

	// Execute approved calls in parallel
	approvedResults := handler.ExecuteParallel(approvedCalls)

	// Build full results array, inserting permission denied for unapproved calls
	results := make([]tools.ToolResult, len(toolCalls))
	for i := range toolCalls {
		approved := i < len(approvals) && approvals[i]
		if !approved {
			results[i] = tools.PermissionDeniedResult(toolCalls[i].Function.Name)
		}
	}

	// Fill in results for approved calls
	for j, origIdx := range approvedIndices {
		results[origIdx] = approvedResults[j]
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
