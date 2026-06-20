// Package prompt provides a modular, configurable system prompt builder
// for the Cardinal agent. It allows toggling sections and produces a
// final prompt string used at runtime.
package prompt

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
// The personality string is used as the identity section content.
func NewPromptBuilder(workingDir, soulContent string, personality ...string) *PromptBuilder {
	identityContent := "You are Cardinal, a helpful coding assistant. Be concise and direct."
	if len(personality) > 0 && strings.TrimSpace(personality[0]) != "" {
		identityContent = personality[0]
	}
	b := &PromptBuilder{
		workingDir:  workingDir,
		soulContent: soulContent,
	}
	env := buildEnvironmentSection(workingDir)
	b.sections = []Section{
		{
			Name:     "identity",
			Content:  identityContent,
			Enabled:  true,
			Required: true,
		},
		{
			Name:     "environment",
			Content:  env,
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
			Content: "TASK TRACKING: When a task has more than one step, sketch a todo " +
				"list using the todo_write tool. Keep it short — just the next few " +
				"moves, in plain language. Mark items in_progress when you start them, " +
				"completed when you're done. Do not add priorities, due dates, or " +
				"subtasks; none of that is exposed. The list is session-only.",
			Enabled:  true,
			Required: false,
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

func buildEnvironmentSection(workingDir string) string {
	var b strings.Builder
	b.WriteString("Environment:\n")
	b.WriteString("- Platform: " + describePlatform() + "\n")
	b.WriteString("- Architecture: " + runtime.GOARCH + "\n")
	b.WriteString("- Shell: " + describeShell() + "\n")
	b.WriteString("- User: " + safeGetEnv("USER", "USERNAME", "USERNAME") + "\n")
	b.WriteString("- Hostname: " + safeGetEnv("HOSTNAME", "COMPUTERNAME", "") + "\n")
	b.WriteString("- Terminal: " + safeGetEnv("TERM_PROGRAM", "TERM", "") + " (" + safeGetEnv("COLORTERM", "", "") + ")\n")
	b.WriteString("- Working directory: " + workingDir + "\n")
	b.WriteString("- Path separator: " + string(filepath.Separator) + "\n")
	b.WriteString("- Date: " + safeDate() + "\n")
	if lang := detectPrimaryLanguage(workingDir); lang != "" {
		b.WriteString("- Detected project language: " + lang + "\n")
	}
	if branch, dirty := describeGit(workingDir); branch != "" {
		b.WriteString("- Git branch: " + branch + dirty + "\n")
	}
	b.WriteString("- Files (top three levels):\n")
	for _, line := range compactFileTree(workingDir) {
		b.WriteString("  " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func safeGetEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "unknown"
}

func describeShell() string {
	shell := safeGetEnv("SHELL", "COMSPEC", "")
	if shell == "unknown" || shell == "" {
		return "unknown"
	}
	return filepath.Base(shell)
}

func safeDate() string {
	return time.Now().Format("2006-01-02 15:04 MST")
}

func detectPrimaryLanguage(root string) string {
	if hasFile(root, "go.mod") {
		return "Go"
	}
	if hasFile(root, "package.json") {
		return "JavaScript/TypeScript"
	}
	if hasFile(root, "Cargo.toml") {
		return "Rust"
	}
	if hasFile(root, "pyproject.toml") || hasFile(root, "setup.py") || hasFile(root, "requirements.txt") {
		return "Python"
	}
	if hasFile(root, "Gemfile") {
		return "Ruby"
	}
	if hasFile(root, "pom.xml") || hasFile(root, "build.gradle.kts") {
		return "JVM"
	}
	if hasFile(root, "composer.json") {
		return "PHP"
	}
	if hasFile(root, "Package.swift") {
		return "Swift"
	}
	return ""
}

func hasFile(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

func describeGit(root string) (string, string) {
	if !hasFile(root, ".git") {
		return "", ""
	}
	branch := strings.TrimSpace(runGit(root, "rev-parse", "--abbrev-ref", "HEAD"))
	if branch == "" || branch == "HEAD" {
		return "", ""
	}
	dirty := ""
	if out := strings.TrimSpace(runGit(root, "status", "--porcelain")); out != "" {
		dirty = " (dirty)"
	}
	return branch, dirty
}

func runGit(root string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func describePlatform() string {
	goos := runtime.GOOS
	switch goos {
	case "darwin":
		return "macOS " + shellVersion("sw_vers", "-productVersion")
	case "windows":
		return "Windows " + shellVersion("cmd", "/c", "ver")
	case "linux":
		return "Linux " + osReleasePrettyName()
	default:
		return strings.Title(goos)
	}
}

func shellVersion(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func osReleasePrettyName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			v := strings.TrimPrefix(line, "PRETTY_NAME=")
			v = strings.Trim(v, `"`)
			return v
		}
	}
	return ""
}

func compactFileTree(root string) []string {
	type entry struct {
		path  string
		isDir bool
	}
	var paths []entry
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := filepath.Base(path)
		if path == root {
			return nil
		}
		if strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			switch base {
			case "node_modules", "vendor", ".git", "dist", "build", "target", ".cache", ".next", ".idea", ".vscode":
				return fs.SkipDir
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if strings.Count(rel, string(filepath.Separator)) > 2 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(paths) < 80 {
			paths = append(paths, entry{path: rel, isDir: d.IsDir()})
		}
		return nil
	})
	lines := make([]string, 0, len(paths))
	for _, p := range paths {
		if p.isDir {
			lines = append(lines, p.path+string(filepath.Separator))
		} else {
			lines = append(lines, p.path)
		}
	}
	if len(paths) == 0 {
		return []string{"(empty directory)"}
	}
	return lines
}
