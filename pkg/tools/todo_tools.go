package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"cardinal/pkg/storage"
)

func (th *ToolHandler) executeTodoWrite(args string) ToolResult {
	var params struct {
		// Top-level fields for the operation as a whole.
		Action string `json:"action"` // "add", "update", "remove"
		// Fields used by add.
		Title string `json:"title,omitempty"`
		// Fields used by update / remove.
		ID     string `json:"id,omitempty"`
		Status string `json:"status,omitempty"` // "pending", "in_progress", "completed"
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "todo_write", Success: false, Error: err.Error()}
	}
	switch strings.ToLower(strings.TrimSpace(params.Action)) {
	case "add":
		if strings.TrimSpace(params.Title) == "" {
			return ToolResult{Name: "todo_write", Success: false, Error: "title is required"}
		}
		_, err := th.todos.Add(params.Title)
		if err != nil {
			return ToolResult{Name: "todo_write", Success: false, Error: err.Error()}
		}
		return ToolResult{Name: "todo_write", Success: true, Output: FormatTodoList(th.todos.List(""))}
	case "update":
		if strings.TrimSpace(params.ID) == "" {
			return ToolResult{Name: "todo_write", Success: false, Error: "id is required"}
		}
		var status *storage.TodoStatus
		if params.Status != "" {
			s := storage.TodoStatus(strings.ToLower(strings.TrimSpace(params.Status)))
			switch s {
			case storage.TodoPending, storage.TodoInProgress, storage.TodoCompleted:
				status = &s
			default:
				return ToolResult{Name: "todo_write", Success: false, Error: "status must be pending, in_progress, or completed"}
			}
		}
		// Also allow updating the title (handy, no extra tool needed).
		var titlePtr *string
		if params.Title != "" {
			t := params.Title
			titlePtr = &t
		}
		if _, err := th.todos.Update(params.ID, status, titlePtr); err != nil {
			return ToolResult{Name: "todo_write", Success: false, Error: err.Error()}
		}
		return ToolResult{Name: "todo_write", Success: true, Output: FormatTodoList(th.todos.List(""))}
	case "remove":
		if strings.TrimSpace(params.ID) == "" {
			return ToolResult{Name: "todo_write", Success: false, Error: "id is required"}
		}
		if err := th.todos.Remove(params.ID); err != nil {
			return ToolResult{Name: "todo_write", Success: false, Error: err.Error()}
		}
		return ToolResult{Name: "todo_write", Success: true, Output: FormatTodoList(th.todos.List(""))}
	default:
		return ToolResult{Name: "todo_write", Success: false, Error: "action must be add, update, or remove"}
	}
}

func (th *ToolHandler) executeTodoRead(args string) ToolResult {
	var params struct {
		Status string `json:"status,omitempty"`
	}
	// Allow empty args.
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &params); err != nil {
			return ToolResult{Name: "todo_read", Success: false, Error: err.Error()}
		}
	}
	statusFilter := strings.ToLower(strings.TrimSpace(params.Status))
	if statusFilter != "" {
		switch statusFilter {
		case string(storage.TodoPending), string(storage.TodoInProgress), string(storage.TodoCompleted):
		default:
			return ToolResult{Name: "todo_read", Success: false, Error: "status filter must be pending, in_progress, or completed"}
		}
	}
	items := th.todos.List(statusFilter)
	if len(items) == 0 {
		if statusFilter == "" {
			return ToolResult{Name: "todo_read", Success: true, Output: "No todos yet."}
		}
		return ToolResult{Name: "todo_read", Success: true, Output: "No " + statusFilter + " todos."}
	}
	return ToolResult{Name: "todo_read", Success: true, Output: FormatTodoList(items)}
}

// FormatTodoList renders a todo list in a plain, human-friendly checklist.
// Each item occupies one line. Completed items are wrapped in strikethrough
// markers so the terminal/renderer can style them if it wishes; otherwise
// they read as natural prose. Exposed so other packages (e.g. the todo
// verifier) can render the same snapshot the model sees.
func FormatTodoList(items []storage.TodoItem) string {
	if len(items) == 0 {
		return "No todos yet."
	}
	var sb strings.Builder
	for _, item := range items {
		mark := "☐"
		switch item.Status {
		case storage.TodoInProgress:
			mark = "◐"
		case storage.TodoCompleted:
			mark = "☑"
		}
		title := item.Title
		idSuffix := "  #" + shortID(item.ID)
		switch item.Status {
		case storage.TodoCompleted:
			sb.WriteString(fmt.Sprintf("%s ~~%s~~%s\n", mark, title, idSuffix))
		case storage.TodoInProgress:
			sb.WriteString(fmt.Sprintf("%s %s (in progress)%s\n", mark, title, idSuffix))
		default:
			sb.WriteString(fmt.Sprintf("%s %s%s\n", mark, title, idSuffix))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// shortID returns the trailing portion of a todo id so the rendered checklist
// stays compact. Real ids look like "20260120123456-abcd" — keeping just the
// random tail is plenty for a model to disambiguate.
func shortID(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 && i < len(id)-1 {
		return id[i+1:]
	}
	return id
}

func getTodoToolDefinitions() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "todo_write",
				"description": "Manage the session todo list. Use action=add with a title to create a todo, action=update with id and status (or a new title) to change one, or action=remove with id to delete it. Always returns the current todo list.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"add", "update", "remove"},
							"description": "What to do: add creates a new todo, update changes status or title of an existing one, remove deletes it.",
						},
						"title": map[string]any{
							"type":        "string",
							"description": "For action=add: the todo text. For action=update: an optional new title.",
						},
						"id": map[string]any{
							"type":        "string",
							"description": "The short id shown in the list (e.g. 'ab12'). Required for update and remove.",
						},
						"status": map[string]any{
							"type":        "string",
							"enum":        []string{"pending", "in_progress", "completed"},
							"description": "New status. Only used with action=update.",
						},
					},
					"required": []string{"action"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "todo_read",
				"description": "Show the current todo list. Optionally filter by status (pending, in_progress, completed).",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": map[string]any{
							"type":        "string",
							"enum":        []string{"pending", "in_progress", "completed"},
							"description": "Optional filter: only show todos with this status.",
						},
					},
				},
			},
		},
	}
}

func summarizeTodoCall(name, args string) string {
	switch name {
	case "todo_write":
		var params struct {
			Action string `json:"action"`
			Title  string `json:"title,omitempty"`
			ID     string `json:"id,omitempty"`
			Status string `json:"status,omitempty"`
		}
		if err := json.Unmarshal([]byte(args), &params); err != nil {
			return "todo"
		}
		switch params.Action {
		case "add":
			if params.Title != "" {
				return "todo: add '" + truncate(params.Title, 40) + "'"
			}
			return "todo: add"
		case "update":
			bits := "todo: update " + params.ID
			if params.Status != "" {
				bits += " → " + params.Status
			}
			if params.Title != "" {
				bits += " (rename)"
			}
			return bits
		case "remove":
			return "todo: remove " + params.ID
		}
		return "todo"
	case "todo_read":
		return "todo: list"
	}
	// Backward-compat summary lines for any leftover callers.
	switch name {
	case "todo_add", "todo_list", "todo_update", "todo_remove", "todo_set_progress", "todo_toggle_step":
		return "todo"
	}
	return ""
}


