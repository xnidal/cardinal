package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

type TodoPriority string

const (
	PriorityLow    TodoPriority = "low"
	PriorityMedium TodoPriority = "medium"
	PriorityHigh   TodoPriority = "high"
)

type TodoItem struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Status      TodoStatus   `json:"status"`
	Priority    TodoPriority `json:"priority"`
	DueDate     *time.Time   `json:"due_date,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type TodoList struct {
	Items []TodoItem `json:"items"`
}

func getTodoPath() string {
	return filepath.Join(getConfigPath(), "todos.json")
}

func LoadTodos() (*TodoList, error) {
	path := getTodoPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TodoList{Items: []TodoItem{}}, nil
		}
		return nil, err
	}
	var list TodoList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

func SaveTodos(list *TodoList) error {
	path := getTodoPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func AddTodo(title, description string, priority TodoPriority, dueDate *time.Time) (*TodoItem, error) {
	list, err := LoadTodos()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	item := TodoItem{
		ID:          generateTodoID(),
		Title:       title,
		Description: description,
		Status:      TodoPending,
		Priority:    priority,
		DueDate:     dueDate,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	list.Items = append(list.Items, item)
	if err := SaveTodos(list); err != nil {
		return nil, err
	}
	return &item, nil
}

func UpdateTodo(id string, status *TodoStatus, priority *TodoPriority, dueDate *time.Time) (*TodoItem, error) {
	list, err := LoadTodos()
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].ID == id {
			if status != nil {
				list.Items[i].Status = *status
			}
			if priority != nil {
				list.Items[i].Priority = *priority
			}
			if dueDate != nil {
				list.Items[i].DueDate = dueDate
			}
			list.Items[i].UpdatedAt = time.Now()
			if err := SaveTodos(list); err != nil {
				return nil, err
			}
			return &list.Items[i], nil
		}
	}
	return nil, os.ErrNotExist
}

func RemoveTodo(id string) error {
	list, err := LoadTodos()
	if err != nil {
		return err
	}
	for i, item := range list.Items {
		if item.ID == id {
			list.Items = append(list.Items[:i], list.Items[i+1:]...)
			return SaveTodos(list)
		}
	}
	return os.ErrNotExist
}

func generateTodoID() string {
	return time.Now().Format("20060102150405") + "-" + randomString(4)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := range b {
		seed = seed*1103515245 + 12345
		b[i] = letters[int(seed)%len(letters)]
	}
	return string(b)
}
