package tools

import (
	"encoding/json"
	"fmt"
	"sync"
)

// SubAgentModel profiles for different use cases
var SubAgentModels = map[string]ModelProfile{
	"fast": {
		Name:        "fast",
		Description: "Fast, lightweight model for quick tasks like codebase exploration, simple queries, and formatting",
		Model:       "", // Will use default from config if empty
		MaxTokens:   2048,
		Temperature: 0.3,
	},
	"smart": {
		Name:        "smart",
		Description: "Capable model for complex reasoning, analysis, and multi-step tasks",
		Model:       "", // Will use default from config if empty
		MaxTokens:   4096,
		Temperature: 0.7,
	},
	"tiny": {
		Name:        "tiny",
		Description: "Smallest, fastest model for trivial tasks",
		Model:       "",
		MaxTokens:   1024,
		Temperature: 0.2,
	},
}

type ModelProfile struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Model       string  `json:"model,omitempty"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
}

// SubAgentTask represents a task to be executed by a sub-agent
type SubAgentTask struct {
	ID          string `json:"id"`
	Profile     string `json:"profile"`
	Prompt      string `json:"prompt"`
	SystemAddOn string `json:"system_add_on,omitempty"`
	Status      string `json:"status"` // pending, running, completed, failed
	Result      string `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
}

// SubAgentManager manages concurrent sub-agent tasks
type SubAgentManager struct {
	mu        sync.RWMutex
	tasks     map[string]*SubAgentTask
	taskOrder []string
}

var subAgentManager = &SubAgentManager{
	tasks: make(map[string]*SubAgentTask),
}

func GetSubAgentManager() *SubAgentManager {
	return subAgentManager
}

func (m *SubAgentManager) CreateTask(profile, prompt, systemAddOn string) *SubAgentTask {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := fmt.Sprintf("task_%d", len(m.tasks)+1)
	task := &SubAgentTask{
		ID:          id,
		Profile:     profile,
		Prompt:      prompt,
		SystemAddOn: systemAddOn,
		Status:      "pending",
	}
	m.tasks[id] = task
	m.taskOrder = append(m.taskOrder, id)
	return task
}

func (m *SubAgentManager) GetTask(id string) *SubAgentTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tasks[id]
}

func (m *SubAgentManager) UpdateTask(id string, status, result, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task, ok := m.tasks[id]; ok {
		task.Status = status
		if result != "" {
			task.Result = result
		}
		if errMsg != "" {
			task.Error = errMsg
		}
	}
}

func (m *SubAgentManager) ListTasks() []*SubAgentTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tasks := make([]*SubAgentTask, 0, len(m.taskOrder))
	for _, id := range m.taskOrder {
		if task, ok := m.tasks[id]; ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func (m *SubAgentManager) ClearCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	newOrder := []string{}
	for _, id := range m.taskOrder {
		if task, ok := m.tasks[id]; ok {
			if task.Status != "completed" && task.Status != "failed" {
				newOrder = append(newOrder, id)
			} else {
				delete(m.tasks, id)
			}
		}
	}
	m.taskOrder = newOrder
}

// GetAvailableProfiles returns list of available model profiles
func GetAvailableProfiles() []ModelProfile {
	profiles := make([]ModelProfile, 0, len(SubAgentModels))
	for _, p := range SubAgentModels {
		profiles = append(profiles, p)
	}
	return profiles
}

// Tool result formatting for subagent tools
func formatSubAgentTaskResult(task *SubAgentTask) string {
	result := fmt.Sprintf("<subagent_task id=\"%s\" profile=\"%s\" status=\"%s\">\n",
		task.ID, task.Profile, task.Status)
	if task.Prompt != "" {
		truncated := task.Prompt
		if len(truncated) > 100 {
			truncated = truncated[:100] + "..."
		}
		result += fmt.Sprintf("  <prompt>%s</prompt>\n", truncated)
	}
	if task.Result != "" {
		result += fmt.Sprintf("  <result>%s</result>\n", task.Result)
	}
	if task.Error != "" {
		result += fmt.Sprintf("  <error>%s</error>\n", task.Error)
	}
	result += "</subagent_task>"
	return result
}

func (th *ToolHandler) executeSubAgent(args string) ToolResult {
	var params struct {
		Profile     string `json:"profile"`
		Prompt      string `json:"prompt"`
		SystemAddOn string `json:"system_add_on,omitempty"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "subagent", Success: false, Error: err.Error()}
	}

	if params.Prompt == "" {
		return ToolResult{Name: "subagent", Success: false, Error: "prompt is required"}
	}

	profile := params.Profile
	if profile == "" {
		profile = "fast" // default to fast
	}

	if _, ok := SubAgentModels[profile]; !ok {
		return ToolResult{
			Name:    "subagent",
			Success: false,
			Error:   fmt.Sprintf("unknown profile: %s (available: fast, smart, tiny)", profile),
		}
	}

	// Create the task - actual execution happens in the TUI layer
	task := subAgentManager.CreateTask(profile, params.Prompt, params.SystemAddOn)

	return ToolResult{
		Name:    "subagent",
		Success: true,
		Output:  fmt.Sprintf("Task created: %s (status: pending)", task.ID),
		Path:    task.ID,
	}
}

func (th *ToolHandler) executeSubAgentStatus(args string) ToolResult {
	var params struct {
		TaskID string `json:"task_id"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "subagent_status", Success: false, Error: err.Error()}
	}

	task := subAgentManager.GetTask(params.TaskID)
	if task == nil {
		return ToolResult{
			Name:    "subagent_status",
			Success: false,
			Error:   fmt.Sprintf("task not found: %s", params.TaskID),
		}
	}

	return ToolResult{
		Name:    "subagent_status",
		Success: true,
		Output:  formatSubAgentTaskResult(task),
		Path:    task.ID,
	}
}

func (th *ToolHandler) executeSubAgentList(args string) ToolResult {
	tasks := subAgentManager.ListTasks()
	if len(tasks) == 0 {
		return ToolResult{
			Name:    "subagent_list",
			Success: true,
			Output:  "No active sub-agent tasks",
		}
	}

	output := fmt.Sprintf("Active sub-agent tasks (%d):\n", len(tasks))
	for _, task := range tasks {
		output += fmt.Sprintf("- %s [%s]: %s\n", task.ID, task.Profile, task.Status)
	}

	return ToolResult{
		Name:    "subagent_list",
		Success: true,
		Output:  output,
	}
}

func (th *ToolHandler) executeSubAgentClear(args string) ToolResult {
	subAgentManager.ClearCompleted()
	return ToolResult{Name: "subagent_clear", Success: true, Output: "Cleared completed sub-agents"}
}
