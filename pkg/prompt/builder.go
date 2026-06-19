// Package prompt provides a modular, configurable system prompt builder
// for the Cardinal agent. It allows toggling sections and produces a
// final prompt string used at runtime.
package prompt

import (
	"fmt"
	"strings"
)

// Section represents a configurable section of the system prompt.
type Section struct {
	Name     string
	Content  string
	Enabled  bool
	Required bool // if true, cannot be disabled
}

// PromptBuilder constructs the agent system prompt from modular sections.
type PromptBuilder struct {
	sections    []Section
	workingDir  string
	soulContent string
}

// NewPromptBuilder creates a PromptBuilder with default sections.
func NewPromptBuilder(workingDir, soulContent string) *PromptBuilder {
	b := &PromptBuilder{
		workingDir:  workingDir,
		soulContent: soulContent,
	}
	b.sections = []Section{
		{
			Name:     "identity",
			Content:  "You are Cardinal, a helpful coding assistant. Be concise and direct.",
			Enabled:  true,
			Required: true,
		},
		{
			Name:     "working_dir",
			Content:  "Working directory: " + workingDir,
			Enabled:  true,
			Required: true,
		},
		{
			Name: "tool_format",
			Content: "When using tools, you MUST use the standard function calling " +
				"format with JSON arguments. Do NOT use XML tags. Use the provided " +
				"tool definitions through the proper API function calling mechanism.",
			Enabled:  true,
			Required: true,
		},
		{
			Name: "parallel_tools",
			Content: "IMPORTANT: You can call multiple independent tools in a single " +
				"response. When you need to perform several independent operations " +
				"(e.g., reading multiple files, running multiple searches, listing " +
				"multiple directories), make all those tool calls at once rather than " +
				"sequentially. This saves time and reduces unnecessary round-trips. " +
				"Only sequence tool calls when one depends on the output of another.",
			Enabled:  true,
			Required: false,
		},
		{
			Name: "action_directive",
			Content: "Unless the user explicitly asks for a plan or some other intent " +
				"that makes it clear that code should not be written, assume the user " +
				"wants you to make code changes or run tools to solve the user's " +
				"problem. In these cases, it is bad to output your proposed solution " +
				"in a message, you should go ahead and actually implement the change. " +
				"If you encounter challenges or blockers, you should attempt to resolve " +
				"them yourself.",
			Enabled:  true,
			Required: true,
		},
		{
			Name: "todo_tracking",
			Content: "TASK TRACKING MANDATE: When the user asks you to perform a task " +
				"that involves multiple steps, you MUST create and maintain a todo list " +
				"using the todo_add tool. Break the task down into discrete steps, add " +
				"each step as a todo item, and update each item's status (pending -> " +
				"in_progress -> completed) as you work through them using the " +
				"todo_update tool. This applies to ANY task with more than one step. " +
				"You must track your progress - do not skip this. After completing a " +
				"step, immediately mark it completed before moving to the next one. " +
				"Before starting a step, mark it in_progress.",
			Enabled:  true,
			Required: true,
		},
		{
			Name: "behavior",
			Content: "You should act, not just suggest. Prefer tools over " +
				"explanations. Iterate until the task is complete. When something " +
				"can be implemented, implement it. When something is broken, fix it " +
				"properly. When something is unclear, infer intelligently and proceed.",
			Enabled:  true,
			Required: false,
		},
	}

	// Add soul section only if there is content
	if strings.TrimSpace(soulContent) != "" {
		b.sections = append(b.sections, Section{
			Name:     "soul",
			Content:  soulContent,
			Enabled:  true,
			Required: false,
		})
	}
	return b
}

// AddSection appends a custom section to the builder.
func (b *PromptBuilder) AddSection(name, content string, enabled, required bool) {
	b.sections = append(b.sections, Section{
		Name:     name,
		Content:  content,
		Enabled:  enabled,
		Required: required,
	})
}

// EnableSection enables a section by name. Returns an error if not found.
func (b *PromptBuilder) EnableSection(name string) error {
	for i := range b.sections {
		if b.sections[i].Name == name {
			b.sections[i].Enabled = true
			return nil
		}
	}
	return fmt.Errorf("section not found: %s", name)
}

// DisableSection disables a section by name.
// Returns an error if the section is required or not found.
func (b *PromptBuilder) DisableSection(name string) error {
	for i := range b.sections {
		if b.sections[i].Name == name {
			if b.sections[i].Required {
				return fmt.Errorf("cannot disable required section: %s", name)
			}
			b.sections[i].Enabled = false
			return nil
		}
	}
	return fmt.Errorf("section not found: %s", name)
}

// SetSectionContent updates the content of a section by name.
func (b *PromptBuilder) SetSectionContent(name, content string) error {
	for i := range b.sections {
		if b.sections[i].Name == name {
			b.sections[i].Content = content
			return nil
		}
	}
	return fmt.Errorf("section not found: %s", name)
}

// Build assembles all enabled sections into a single prompt string,
// separated by double newlines.
func (b *PromptBuilder) Build() string {
	var parts []string
	for _, s := range b.sections {
		if s.Enabled && strings.TrimSpace(s.Content) != "" {
			parts = append(parts, s.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// SectionNames returns the names of all sections in order.
func (b *PromptBuilder) SectionNames() []string {
	names := make([]string, len(b.sections))
	for i, s := range b.sections {
		names[i] = s.Name
	}
	return names
}

// IsEnabled returns whether a named section is currently enabled.
func (b *PromptBuilder) IsEnabled(name string) bool {
	for _, s := range b.sections {
		if s.Name == name {
			return s.Enabled
		}
	}
	return false
}
