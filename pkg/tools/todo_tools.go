package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cardinal/pkg/storage"
)

func (th *ToolHandler) executeTodoAdd(args string) ToolResult {
	var params struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Priority    string `json:"priority,omitempty"`
		DueDate     string `json:"due_date,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "todo_add", Success: false, Error: err.Error()}
	}
	if strings.TrimSpace(params.Title) == "" {
		return ToolResult{Name: "todo_add", Success: false, Error: "title is required"}
	}
	priority := storage.PriorityMedium
	switch strings.ToLower(params.Priority) {
	case "low":
		priority = storage.PriorityLow
	case "high":
		priority = storage.PriorityHigh
	case "medium":
		priority = storage.PriorityMedium
	}
	var dueDate *time.Time
	if params.DueDate != "" {
		parsed, err := time.Parse("2006-01-02", params.DueDate)
		if err != nil {
			return ToolResult{Name: "todo_add", Success: false, Error: "invalid due_date format, use YYYY-MM-DD"}
		}
		dueDate = &parsed
	}
	item, err := storage.AddTodo(params.Title, params.Description, priority, dueDate)
	if err != nil {
		return ToolResult{Name: "todo_add", Success: false, Error: err.Error()}
	}
	return ToolResult{Name: "todo_add", Success: true, Output: formatTodoItem(item)}
}

func (th *ToolHandler) executeTodoList(args string) ToolResult {
	var params struct {
		Status string `json:"status,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "todo_list", Success: false, Error: err.Error()}
	}
	list, err := storage.LoadTodos()
	if err != nil {
		return ToolResult{Name: "todo_list", Success: false, Error: err.Error()}
	}
	var filtered []storage.TodoItem
	for _, item := range list.Items {
		if params.Status == "" {
			filtered = append(filtered, item)
		} else {
			status := strings.ToLower(params.Status)
			if strings.ToLower(string(item.Status)) == status {
				filtered = append(filtered, item)
			}
		}
	}
	if len(filtered) == 0 {
		return ToolResult{Name: "todo_list", Success: true, Output: "No todo items found"}
	}
	var output strings.Builder
	for _, item := range filtered {
		output.WriteString(formatTodoItem(&item) + "\n")
	}
	return ToolResult{Name: "todo_list", Success: true, Output: strings.TrimSpace(output.String())}
}

func (th *ToolHandler) executeTodoUpdate(args string) ToolResult {
	var params struct {
		ID       string `json:"id"`
		Status   string `json:"status,omitempty"`
		Priority string `json:"priority,omitempty"`
		DueDate  string `json:"due_date,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "todo_update", Success: false, Error: err.Error()}
	}
	if strings.TrimSpace(params.ID) == "" {
		return ToolResult{Name: "todo_update", Success: false, Error: "id is required"}
	}
	var status *storage.TodoStatus
	if params.Status != "" {
		s := storage.TodoStatus(strings.ToLower(params.Status))
		switch s {
		case storage.TodoPending, storage.TodoInProgress, storage.TodoCompleted:
			status = &s
		default:
			return ToolResult{Name: "todo_update", Success: false, Error: "invalid status, use: pending, in_progress, or completed"}
		}
	}
	var priority *storage.TodoPriority
	if params.Priority != "" {
		p := storage.TodoPriority(strings.ToLower(params.Priority))
		switch p {
		case storage.PriorityLow, storage.PriorityMedium, storage.PriorityHigh:
			priority = &p
		default:
			return ToolResult{Name: "todo_update", Success: false, Error: "invalid priority, use: low, medium, or high"}
		}
	}
	var dueDate *time.Time
	if params.DueDate != "" {
		parsed, err := time.Parse("2006-01-02", params.DueDate)
		if err != nil {
			return ToolResult{Name: "todo_update", Success: false, Error: "invalid due_date format, use YYYY-MM-DD"}
		}
		dueDate = &parsed
	}
	item, err := storage.UpdateTodo(params.ID, status, priority, dueDate)
	if err != nil {
		return ToolResult{Name: "todo_update", Success: false, Error: err.Error()}
	}
	return ToolResult{Name: "todo_update", Success: true, Output: formatTodoItem(item)}
}

func (th *ToolHandler) executeTodoRemove(args string) ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "todo_remove", Success: false, Error: err.Error()}
	}
	if strings.TrimSpace(params.ID) == "" {
		return ToolResult{Name: "todo_remove", Success: false, Error: "id is required"}
	}
	if err := storage.RemoveTodo(params.ID); err != nil {
		return ToolResult{Name: "todo_remove", Success: false, Error: err.Error()}
	}
	return ToolResult{Name: "todo_remove", Success: true, Output: "Removed todo item: " + params.ID}
}

func formatTodoItem(item *storage.TodoItem) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] %s", item.ID, item.Title))
	if item.Description != "" {
		sb.WriteString(fmt.Sprintf(" - %s", item.Description))
	}
	sb.WriteString(fmt.Sprintf(" (%s, %s)", item.Priority, item.Status))
	if item.DueDate != nil {
		sb.WriteString(fmt.Sprintf(" due:%s", item.DueDate.Format("2006-01-02")))
	}
	return sb.String()
}

func getTodoToolDefinitions() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "todo_add",
				"description": "Add a new todo item to the todo list",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title":       map[string]interface{}{"type": "string", "description": "The title of the todo item"},
						"description": map[string]interface{}{"type": "string", "description": "Optional description of the todo item"},
						"priority":    map[string]interface{}{"type": "string", "description": "Priority level: low, medium, or high (default: medium)"},
						"due_date":    map[string]interface{}{"type": "string", "description": "Due date in YYYY-MM-DD format"},
					},
					"required": []string{"title"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "todo_list",
				"description": "List all todo items, optionally filtered by status",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"status": map[string]interface{}{"type": "string", "description": "Filter by status: pending, in_progress, or completed"},
					},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "todo_update",
				"description": "Update a todo item's status, priority, or due date",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":        map[string]interface{}{"type": "string", "description": "The ID of the todo item"},
						"status":    map[string]interface{}{"type": "string", "description": "New status: pending, in_progress, or completed"},
						"priority":  map[string]interface{}{"type": "string", "description": "New priority: low, medium, or high"},
						"due_date":  map[string]interface{}{"type": "string", "description": "New due date in YYYY-MM-DD format"},
					},
					"required": []string{"id"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "todo_remove",
				"description": "Remove a todo item from the list",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{"type": "string", "description": "The ID of the todo item to remove"},
					},
					"required": []string{"id"},
				},
			},
		},
	}
}

func summarizeTodoCall(name, args string) string {
	switch name {
	case "todo_add":
		var params struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && params.Title != "" {
			return "todo: add '" + truncate(params.Title, 40) + "'"
		}
	case "todo_list":
		return "todo: list items"
	case "todo_update":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && params.ID != "" {
			return "todo: update " + params.ID
		}
	case "todo_remove":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && params.ID != "" {
			return "todo: remove " + params.ID
		}
	}
	return ""
}
