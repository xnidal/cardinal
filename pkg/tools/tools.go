package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"cardinal/pkg/storage"
)

type ToolResult struct {
	Name    string       `json:"name"`
	Success bool         `json:"success"`
	Output  string       `json:"output"`
	Error   string       `json:"error,omitempty"`
	Path    string       `json:"path,omitempty"`
	Lines   string       `json:"lines,omitempty"`
	Data    []ToolResult `json:"data,omitempty"`
}

type ToolCall struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

type ToolHandler struct {
	workingDir string
	tools      map[string]func(string) ToolResult
	onEditSoul func()
	todos      *storage.TodoStore
}

func NewToolHandler(workingDir string, onEditSoul func()) *ToolHandler {
	return NewToolHandlerWithTodos(workingDir, onEditSoul, storage.NewTodoStore())
}

// NewToolHandlerWithTodos lets callers share a todo store across the whole
// session. Each session should construct exactly one TodoStore; the store is
// in-memory only and intentionally drops on process exit.
func NewToolHandlerWithTodos(workingDir string, onEditSoul func(), todos *storage.TodoStore) *ToolHandler {
	th := &ToolHandler{
		workingDir: workingDir,
		tools:      make(map[string]func(string) ToolResult),
		onEditSoul: onEditSoul,
		todos:      todos,
	}

	th.tools["bash"] = th.executeBash
	th.tools["list_files"] = th.executeListFiles
	th.tools["read_files"] = th.executeReadFiles
	th.tools["write_file"] = th.executeWriteFile
	th.tools["edit_file"] = th.executeEditFile
	th.tools["grep"] = th.executeGrep
	th.tools["glob"] = th.executeGlob
	th.tools["file_info"] = th.executeFileInfo
	th.tools["edit_soul"] = th.executeEditSoul
	th.tools["calculate"] = th.executeCalculate
	th.tools["todo_write"] = th.executeTodoWrite
	th.tools["todo_read"] = th.executeTodoRead
	th.tools["subagent"] = th.executeSubAgent
	th.tools["subagent_status"] = th.executeSubAgentStatus
	th.tools["subagent_list"] = th.executeSubAgentList
	th.tools["subagent_clear"] = th.executeSubAgentClear

	return th
}

func KnownTool(name string) bool {
	switch name {
	case "bash", "list_files", "read_files", "write_file", "edit_file", "grep", "glob", "file_info", "edit_soul", "calculate",
		"todo_write", "todo_read",
		"subagent", "subagent_status", "subagent_list", "subagent_clear":
		return true
	default:
		return false
	}
}

func (th *ToolHandler) Execute(call ToolCall) ToolResult {
	// Validate reason if required by policy
	if result := ValidateReason(call.Name, call.Args, DefaultReasonPolicy()); result != nil {
		return *result
	}
	executor, exists := th.tools[call.Name]
	if !exists {
		return ToolResult{Name: call.Name, Success: false, Error: fmt.Sprintf("unknown tool: %s", call.Name)}
	}
	return executor(call.Args)
}

func RequiresApproval(name string) bool {
	switch name {
	case "list_files", "read_files", "grep", "glob", "file_info", "edit_soul", "calculate",
		"todo_write", "todo_read",
		"subagent", "subagent_status", "subagent_list", "subagent_clear":
		return false
	default:
		return true
	}
}

func PermissionDeniedResult(name string) ToolResult {
	return ToolResult{Name: name, Success: false, Error: "permission denied"}
}

func SummarizeCall(name, args string) string {
	if todo := summarizeTodoCall(name, args); todo != "" {
		return todo
	}
	switch name {
	case "bash":
		var params struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Command) != "" {
			return "$ " + truncate(params.Command, 60)
		}
	case "calculate":
		var params struct {
			Expression string `json:"expression"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Expression) != "" {
			return "= " + params.Expression
		}
	case "read_files":
		var params struct {
			Paths  []string `json:"paths"`
			Offset int      `json:"offset,omitempty"`
			Limit  int      `json:"limit,omitempty"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && len(params.Paths) > 0 {
			if len(params.Paths) == 1 {
				r := "read: " + params.Paths[0]
				if params.Offset > 0 || params.Limit > 0 {
					r += fmt.Sprintf(" (line %d", params.Offset+1)
					if params.Limit > 0 {
						r += fmt.Sprintf(", %d lines", params.Limit)
					}
					r += ")"
				}
				return r
			}
			return fmt.Sprintf("read_files (%d)", len(params.Paths))
		}
	case "write_file":
		var params struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Path) != "" {
			return "write: " + params.Path
		}
	case "edit_file":
		var params struct {
			Path string `json:"path"`
			Find string `json:"find"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Path) != "" {
			find := params.Find
			if len(find) > 30 {
				find = find[:30] + "..."
			}
			return "edit: " + params.Path + " (replace: " + find + ")"
		}
	case "list_files":
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil {
			path := "."
			if params.Path != "" {
				path = params.Path
			}
			return "ls: " + path
		}
	case "grep":
		var params struct {
			Pattern string `json:"pattern"`
			Include string `json:"include,omitempty"`
			Path    string `json:"path,omitempty"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Pattern) != "" {
			result := "grep: " + params.Pattern
			if params.Include != "" {
				result += " in " + params.Include
			}
			if params.Path != "" {
				result += " (" + params.Path + ")"
			}
			return truncate(result, 60)
		}
	case "glob":
		var params struct {
			Path    string `json:"path,omitempty"`
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Pattern) != "" {
			result := "glob: " + params.Pattern
			if params.Path != "" {
				result += " (" + params.Path + ")"
			}
			return truncate(result, 60)
		}
	case "file_info":
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Path) != "" {
			return "info: " + params.Path
		}
	case "edit_soul":
		var params struct {
			Find string `json:"find"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && params.Find != "" {
			find := params.Find
			if len(find) > 30 {
				find = find[:30] + "..."
			}
			return "edit_soul: replace '" + find + "'"
		}
	}
	baseSummary := truncate(name+" "+args, 60)
	if reason := ExtractReason(args); reason != "" {
		return truncate(baseSummary+" [reason: "+reason+"]", 100)
	}
	return baseSummary
}

func (th *ToolHandler) executeBash(args string) ToolResult {
	var params struct {
		Command string `json:"command"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "bash", Success: false, Error: err.Error()}
	}

	if strings.TrimSpace(params.Command) == "" {
		return ToolResult{Name: "bash", Success: false, Error: "no command provided"}
	}

	cmd := exec.Command("bash", "-c", params.Command)
	cmd.Dir = th.workingDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return ToolResult{Name: "bash", Success: false, Output: stdout.String(), Error: fmt.Sprintf("%s: %s", err.Error(), stderr.String()), Path: params.Command}
	}

	return ToolResult{Name: "bash", Success: true, Output: stdout.String(), Path: params.Command}
}

func (th *ToolHandler) executeListFiles(args string) ToolResult {
	var params struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "list_files", Success: false, Error: err.Error()}
	}

	fullPath, err := th.resolvePath(params.Path)
	if err != nil {
		return ToolResult{Name: "list_files", Success: false, Error: err.Error()}
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return ToolResult{Name: "list_files", Success: false, Error: err.Error()}
	}

	var files []string
	for _, entry := range entries {
		prefix := "[FILE] "
		if entry.IsDir() {
			prefix = "[DIR]  "
		}
		files = append(files, prefix+entry.Name())
	}

	displayPath := params.Path
	if displayPath == "" {
		displayPath = "."
	}

	return ToolResult{Name: "list_files", Success: true, Output: strings.Join(files, "\n"), Path: displayPath}
}

func (th *ToolHandler) executeReadFiles(args string) ToolResult {
	var params struct {
		Paths  []string `json:"paths"`
		Offset int      `json:"offset,omitempty"`
		Limit  int      `json:"limit,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "read_files", Success: false, Error: err.Error()}
	}
	if len(params.Paths) == 0 {
		return ToolResult{Name: "read_files", Success: false, Error: "paths is required"}
	}
	const maxOutput = 6000
	bodyLimit := params.Limit
	if bodyLimit <= 0 {
		bodyLimit = 6000
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	type fileResult struct {
		path      string
		relPath   string
		fromLine  int    // 1-based inclusive; 0 for "full file"
		toLine    int    // 1-based inclusive
		total     int
		truncated bool   // body was clipped at maxOutput
		body      string
		err       error
	}
	results := make([]fileResult, 0, len(params.Paths))
	for _, p := range params.Paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		fullPath, err := th.resolvePath(p)
		if err != nil {
			results = append(results, fileResult{path: p, err: err})
			continue
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			results = append(results, fileResult{path: p, err: err})
			continue
		}
		lines := strings.Split(string(data), "\n")
		total := len(lines)
		start := offset
		if start >= total {
			results = append(results, fileResult{path: p, total: total, fromLine: 1, toLine: 0})
			continue
		}
		end := offset + bodyLimit
		if end > total {
			end = total
		}
		body := strings.Join(lines[start:end], "\n")
		truncated := false
		if len(body) > maxOutput {
			body = body[:maxOutput]
			body += "\n... (truncated)"
			truncated = true
		}
		relPath := th.displayPath(fullPath)
		fr := fileResult{
			path:     p,
			relPath:  relPath,
			body:     body,
			total:    total,
			fromLine: start + 1,
			toLine:   end,
			truncated: truncated,
		}
		results = append(results, fr)
	}

	// Build per-file sub-results for Data field.
	var dataResults []ToolResult
	for _, r := range results {
		sub := ToolResult{Name: "read_files", Path: r.relPath}
		if r.err != nil {
			sub.Success = false
			sub.Error = r.err.Error()
			sub.Output = fmt.Sprintf("=== %s ===\nerror: %s", r.path, r.err.Error())
		} else {
			sub.Success = true
			if r.fromLine == 1 && r.toLine >= r.total {
				sub.Output = fmt.Sprintf("=== %s ===\n%s", r.relPath, r.body)
			} else {
				sub.Output = fmt.Sprintf("=== %s (%d-%d) ===\n%s", r.relPath, r.fromLine, r.toLine, r.body)
			}
		}
		dataResults = append(dataResults, sub)
	}

	if len(results) == 0 {
		return ToolResult{Name: "read_files", Success: false, Error: "no readable paths provided"}
	}

	// Build the model-facing body. Each file section: optional range suffix,
	// then the body. Failed files get an inline error message.
	var sb strings.Builder
	readCount := 0
	for _, r := range results {
		if r.err != nil {
			sb.WriteString(fmt.Sprintf("=== %s ===\nerror: %s\n\n", r.path, r.err.Error()))
			continue
		}
		readCount++
		isFull := r.fromLine == 1 && r.toLine >= r.total
		if isFull {
			sb.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", r.relPath, r.body))
		} else {
			sb.WriteString(fmt.Sprintf("=== %s (%d-%d) ===\n%s\n\n", r.relPath, r.fromLine, r.toLine, r.body))
		}
	}
	body := strings.TrimRight(sb.String(), "\n")

	// Attach per-file metadata so the UI can render a tree without
	// re-parsing the body. Lines is a JSON array, one entry per file.
	type entry struct {
		Path    string `json:"path"`
		Range   string `json:"range,omitempty"`
		Truncated bool  `json:"truncated,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	var metaEntries []entry
	for _, r := range results {
		e := entry{Path: r.relPath}
		if r.err != nil {
			e.Error = r.err.Error()
		} else if r.fromLine == 1 && r.toLine >= r.total {
			// full file, skip Range
		} else {
			e.Range = fmt.Sprintf("%d-%d", r.fromLine, r.toLine)
			if r.truncated {
				e.Truncated = true
			}
		}
		metaEntries = append(metaEntries, e)
	}
	metaJSON, _ := json.Marshal(metaEntries)
	summary := params.Paths[0]
	if len(params.Paths) > 1 {
		summary = ""
	}
	return ToolResult{
		Name:    "read_files",
		Success: readCount > 0,
		Output:  body,
		Path:    summary,
		Lines:   string(metaJSON),
		Data:    dataResults,
	}
}

func (th *ToolHandler) executeWriteFile(args string) ToolResult {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "write_file", Success: false, Error: "Invalid JSON: " + err.Error()}
	}

	if strings.TrimSpace(params.Path) == "" {
		return ToolResult{Name: "write_file", Success: false, Error: "Missing required parameter 'path'. Usage: {\"path\": \"filename.go\", \"content\": \"file content\"}"}
	}
	if strings.TrimSpace(params.Content) == "" {
		return ToolResult{Name: "write_file", Success: false, Error: "Missing required parameter 'content'. Usage: {\"path\": \"filename.go\", \"content\": \"file content\"}"}
	}

	fullPath, err := th.resolvePath(params.Path)
	if err != nil {
		return ToolResult{Name: "write_file", Success: false, Error: "Path error: " + err.Error() + ". Make sure the path is relative to the working directory and doesn't escape it."}
	}

	if err := os.WriteFile(fullPath, []byte(params.Content), 0644); err != nil {
		return ToolResult{Name: "write_file", Success: false, Error: "Cannot write file: " + err.Error() + ". Check if you have write permissions to this file."}
	}

	return ToolResult{Name: "write_file", Success: true, Output: fmt.Sprintf("Successfully wrote to %s", params.Path), Path: params.Path}
}

func (th *ToolHandler) executeEditFile(args string) ToolResult {
	var params struct {
		Path    string `json:"path"`
		Find    string `json:"find"`
		Replace string `json:"replace"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "edit_file", Success: false, Error: "Invalid JSON: " + err.Error()}
	}

	if strings.TrimSpace(params.Path) == "" {
		return ToolResult{Name: "edit_file", Success: false, Error: "Missing required parameter 'path'. Usage: {\"path\": \"filename.go\", \"find\": \"text to find\", \"replace\": \"new text\"}"}
	}
	if strings.TrimSpace(params.Find) == "" {
		return ToolResult{Name: "edit_file", Success: false, Error: "Missing required parameter 'find'. Usage: {\"path\": \"filename.go\", \"find\": \"text to find\", \"replace\": \"new text\"}"}
	}

	fullPath, err := th.resolvePath(params.Path)
	if err != nil {
		return ToolResult{Name: "edit_file", Success: false, Error: "Path error: " + err.Error() + ". Make sure the path is relative to the working directory and doesn't escape it."}
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return ToolResult{Name: "edit_file", Success: false, Error: "Cannot read file: " + err.Error() + ". Check if the file exists and you have read permissions."}
	}

	oldContent := string(content)
	newContent := strings.Replace(oldContent, params.Find, params.Replace, 1)

	if oldContent == newContent {
		return ToolResult{Name: "edit_file", Success: false, Error: "Text not found: '" + params.Find + "' was not found in the file. Use read_files tool to see the exact content, paying attention to whitespace and special characters."}
	}

	if err := os.WriteFile(fullPath, []byte(newContent), 0644); err != nil {
		return ToolResult{Name: "edit_file", Success: false, Error: "Cannot write file: " + err.Error() + ". Check if you have write permissions to this file."}
	}

	diff := formatDiff(oldContent, newContent)
	return ToolResult{Name: "edit_file", Success: true, Output: diff, Path: params.Path}
}

func (th *ToolHandler) executeGrep(args string) ToolResult {
	var params struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include,omitempty"`
		Context int    `json:"context,omitempty"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "grep", Success: false, Error: err.Error()}
	}

	if strings.TrimSpace(params.Pattern) == "" {
		return ToolResult{Name: "grep", Success: false, Error: "pattern is required"}
	}

	searchPath := "."
	if params.Path != "" {
		searchPath = params.Path
	}

	include := "*"
	if params.Include != "" {
		include = params.Include
	}

	context := 2
	if params.Context > 0 {
		context = params.Context
	}

	matches, err := th.grepInDir(searchPath, params.Pattern, include, context)
	if err != nil {
		return ToolResult{Name: "grep", Success: false, Error: err.Error()}
	}

	summary := params.Pattern
	if params.Include != "" {
		summary += " in " + params.Include
	}
	if params.Path != "" {
		summary += " (" + params.Path + ")"
	}

	if len(matches) == 0 {
		return ToolResult{Name: "grep", Success: true, Output: "No matches found", Path: summary}
	}

	return ToolResult{Name: "grep", Success: true, Output: strings.Join(matches, "\n"), Path: summary}
}

func (th *ToolHandler) grepInDir(dir, pattern, include string, context int) ([]string, error) {
	resolvedDir, err := th.resolvePath(dir)
	if err != nil {
		return nil, err
	}

	var results []string

	entries, err := os.ReadDir(resolvedDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if !matchPattern(entry.Name(), include) {
			continue
		}

		filePath := filepath.Join(resolvedDir, entry.Name())
		matches := th.grepInFile(filePath, pattern, context)
		if len(matches) > 0 {
			relPath, _ := filepath.Rel(th.workingDir, filePath)
			results = append(results, fmt.Sprintf("=== %s ===", relPath))
			results = append(results, matches...)
			results = append(results, "")
		}
	}

	return results, nil
}

func (th *ToolHandler) grepInFile(filePath, pattern string, context int) []string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	var matches []string
	lines := strings.Split(string(content), "\n")
	re := regexp.MustCompile("(?i)" + pattern)

	for i, line := range lines {
		if re.MatchString(line) {
			start := max(0, i-context)
			end := min(len(lines)-1, i+context)

			if start < i {
				matches = append(matches, fmt.Sprintf("--- line %d ---", i+1))
			}
			for j := start; j <= end; j++ {
				prefix := "  "
				if j == i {
					prefix = "> "
				}
				matches = append(matches, fmt.Sprintf("%s%d: %s", prefix, j+1, lines[j]))
			}
		}
	}

	return matches
}

func matchPattern(name, pattern string) bool {
	patterns := strings.SplitSeq(pattern, ",")
	for p := range patterns {
		p = strings.TrimSpace(p)
		if p == "*" {
			return true
		}
		if strings.HasSuffix(p, "*") {
			prefix := strings.TrimSuffix(p, "*")
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		if strings.HasPrefix(p, "*") {
			suffix := strings.TrimPrefix(p, "*")
			if strings.HasSuffix(name, suffix) {
				return true
			}
		}
		if name == p {
			return true
		}
	}
	return false
}

func (th *ToolHandler) executeGlob(args string) ToolResult {
	var params struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "glob", Success: false, Error: err.Error()}
	}

	searchPath := "."
	if params.Path != "" {
		searchPath = params.Path
	}

	pattern := "*"
	if params.Pattern != "" {
		pattern = params.Pattern
	}

	fullPath, err := th.resolvePath(searchPath)
	if err != nil {
		return ToolResult{Name: "glob", Success: false, Error: err.Error()}
	}

	var matches []string

	// Handle recursive glob patterns with **
	if strings.Contains(pattern, "**") {
		matches, err = globRecursive(fullPath, pattern)
	} else {
		matches, err = filepath.Glob(filepath.Join(fullPath, pattern))
	}
	if err != nil {
		return ToolResult{Name: "glob", Success: false, Error: err.Error()}
	}

	if len(matches) == 0 {
		return ToolResult{Name: "glob", Success: true, Output: "No matches found", Path: searchPath}
	}

	var results []string
	for _, match := range matches {
		relPath, _ := filepath.Rel(th.workingDir, match)
		info, _ := os.Stat(match)
		prefix := "[FILE] "
		if info.IsDir() {
			prefix = "[DIR] "
		}
		results = append(results, prefix+relPath)
	}

	return ToolResult{Name: "glob", Success: true, Output: strings.Join(results, "\n"), Path: searchPath}
}

func globRecursive(basePath, pattern string) ([]string, error) {
	var matches []string

	parts := strings.Split(pattern, "**")
	if len(parts) < 2 {
		return filepath.Glob(filepath.Join(basePath, pattern))
	}

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info == nil {
			return nil
		}

		suffix := parts[len(parts)-1]
		suffix = strings.TrimPrefix(suffix, string(filepath.Separator))

		matched, err := filepath.Match(suffix, filepath.Base(path))
		if err != nil {
			return nil
		}
		if matched {
			matches = append(matches, path)
		}

		return nil
	})

	return matches, err
}

func (th *ToolHandler) executeFileInfo(args string) ToolResult {
	var params struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "file_info", Success: false, Error: err.Error()}
	}

	if strings.TrimSpace(params.Path) == "" {
		return ToolResult{Name: "file_info", Success: false, Error: "path is required"}
	}

	fullPath, err := th.resolvePath(params.Path)
	if err != nil {
		return ToolResult{Name: "file_info", Success: false, Error: err.Error()}
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return ToolResult{Name: "file_info", Success: false, Error: err.Error()}
	}

	modTime := info.ModTime().Format("2006-01-02 15:04:05")
	size := info.Size()

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Path: %s\n", params.Path))
	output.WriteString(fmt.Sprintf("Type: %s\n", map[bool]string{true: "directory", false: "file"}[info.IsDir()]))
	output.WriteString(fmt.Sprintf("Size: %d bytes\n", size))
	output.WriteString(fmt.Sprintf("Modified: %s\n", modTime))

	return ToolResult{Name: "file_info", Success: true, Output: output.String()}
}

func (th *ToolHandler) executeCalculate(args string) ToolResult {
	var params struct {
		Expression string `json:"expression"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "calculate", Success: false, Error: err.Error()}
	}

	if params.Expression == "" {
		return ToolResult{Name: "calculate", Success: false, Error: "expression is required"}
	}

	result := evaluateExpression(params.Expression)
	return ToolResult{Name: "calculate", Success: true, Output: result}
}

func evaluateExpression(expr string) string {
	expr = strings.ReplaceAll(expr, " ", "")

	if expr == "" {
		return "Error: empty expression"
	}

	// Handle factorial - find the number immediately before !
	if strings.Contains(expr, "!") {
		idx := strings.Index(expr, "!")
		if idx > 0 {
			// Find the number/expression immediately before !
			// Walk backwards to find the start of the factorial operand
			start := idx - 1
			for start >= 0 {
				c := expr[start]
				if (c >= '0' && c <= '9') || c == '.' || c == ')' {
					start--
				} else {
					break
				}
			}
			start++ // Move forward to the first character of the number

			// Handle parentheses - find matching opening paren
			if start < idx && expr[idx-1] == ')' {
				// Find matching opening paren
				parenCount := 1
				parenStart := idx - 2
				for parenStart >= 0 && parenCount > 0 {
					if expr[parenStart] == ')' {
						parenCount++
					} else if expr[parenStart] == '(' {
						parenCount--
					}
					parenStart--
				}
				start = parenStart + 1
			}

			beforeFactorial := expr[start:idx]
			afterFactorial := expr[idx+1:]
			prefix := expr[:start]

			// Evaluate the part before !
			beforeResult := eval(beforeFactorial)
			if strings.HasPrefix(beforeResult, "Error:") {
				return beforeResult
			}

			// Parse the result as an integer for factorial
			var n int
			_, err := fmt.Sscanf(beforeResult, "%d", &n)
			if err != nil {
				return fmt.Sprintf("Error: could not parse '%s' (result: %s) as integer for factorial", beforeFactorial, beforeResult)
			}

			if n < 0 {
				return "Error: factorial of negative number"
			}
			if n > 10000 {
				return "Error: factorial too large"
			}

			factResult := factorial(n).String()

			// Rebuild the expression with the factorial result
			newExpr := prefix + factResult + afterFactorial
			return eval(newExpr)
		}
	}

	return eval(expr)
}

func factorial(n int) *big.Int {
	if n <= 1 {
		return big.NewInt(1)
	}
	result := big.NewInt(1)
	for i := 2; i <= n; i++ {
		result.Mul(result, big.NewInt(int64(i)))
	}
	return result
}

func eval(s string) string {
	s = strings.TrimSpace(s)

	if s == "" {
		return "Error: empty expression"
	}

	if strings.ContainsAny(s, "+-*/^()") {
		return evalComplex(s)
	}

	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Sprintf("Error: could not parse '%s' as number", s)
	}

	return formatNumber(n)
}

func evalComplex(s string) string {
	result, err := parseAndEvaluate(s)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return formatNumber(result)
}

func parseAndEvaluate(s string) (float64, error) {
	tokens := tokenize(s)
	if len(tokens) == 0 {
		return 0, fmt.Errorf("empty expression")
	}

	pos := 0
	return parseExpression(tokens, &pos)
}

func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]

		if c == '(' || c == ')' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(c))
			continue
		}

		if c == '+' || c == '-' || c == '*' || c == '/' || c == '^' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(c))
			continue
		}

		current.WriteByte(c)
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

func parseExpression(tokens []string, pos *int) (float64, error) {
	if *pos >= len(tokens) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	left, err := parseTerm(tokens, pos)
	if err != nil {
		return 0, err
	}

	for *pos < len(tokens) {
		op := tokens[*pos]
		if op != "+" && op != "-" {
			break
		}
		*pos++
		right, err := parseTerm(tokens, pos)
		if err != nil {
			return 0, err
		}
		if op == "+" {
			left += right
		} else {
			left -= right
		}
	}

	return left, nil
}

func parseTerm(tokens []string, pos *int) (float64, error) {
	if *pos >= len(tokens) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	left, err := parsePower(tokens, pos)
	if err != nil {
		return 0, err
	}

	for *pos < len(tokens) {
		op := tokens[*pos]
		if op != "*" && op != "/" {
			break
		}
		*pos++
		right, err := parsePower(tokens, pos)
		if err != nil {
			return 0, err
		}
		if op == "*" {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
	}

	return left, nil
}

func parsePower(tokens []string, pos *int) (float64, error) {
	left, err := parseFactor(tokens, pos)
	if err != nil {
		return 0, err
	}

	for *pos < len(tokens) && tokens[*pos] == "^" {
		*pos++
		right, err := parseFactor(tokens, pos)
		if err != nil {
			return 0, err
		}
		left = math.Pow(left, right)
	}

	return left, nil
}

func parseFactor(tokens []string, pos *int) (float64, error) {
	if *pos >= len(tokens) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	token := tokens[*pos]

	if token == "(" {
		*pos++
		result, err := parseExpression(tokens, pos)
		if err != nil {
			return 0, err
		}
		if *pos >= len(tokens) || tokens[*pos] != ")" {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		*pos++
		return result, nil
	}

	*pos++
	n, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse '%s' as number", token)
	}
	return n, nil
}

func formatNumber(n float64) string {
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	if math.IsNaN(n) {
		return "NaN"
	}

	if n == math.Floor(n) && math.Abs(n) < 1e15 {
		return fmt.Sprintf("%.0f", n)
	}

	trimmed := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.10f", n), "0"), ".")
	if strings.Contains(trimmed, ".") {
		return trimmed
	}
	return fmt.Sprintf("%.10g", n)
}

func (th *ToolHandler) executeEditSoul(args string) ToolResult {
	var params struct {
		Find    string `json:"find"`
		Replace string `json:"replace"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "edit_soul", Success: false, Error: err.Error()}
	}

	if params.Find == "" {
		return ToolResult{Name: "edit_soul", Success: false, Error: "find text is required"}
	}

	configDir := storage.GetConfigDir()
	soulPath := filepath.Join(configDir, "SOUL.md")

	var oldContent string
	if data, err := os.ReadFile(soulPath); err == nil {
		oldContent = string(data)
	}

	newContent := strings.Replace(oldContent, params.Find, params.Replace, 1)

	if oldContent == "" {
		newContent = params.Replace
	}

	if oldContent == newContent && oldContent != "" {
		return ToolResult{Name: "edit_soul", Success: false, Error: "find text not found in SOUL.md"}
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return ToolResult{Name: "edit_soul", Success: false, Error: err.Error()}
	}

	if err := os.WriteFile(soulPath, []byte(newContent), 0644); err != nil {
		return ToolResult{Name: "edit_soul", Success: false, Error: err.Error()}
	}

	if th.onEditSoul != nil {
		th.onEditSoul()
	}

	return ToolResult{Name: "edit_soul", Success: true, Output: fmt.Sprintf("Updated SOUL.md in %s", configDir)}
}

// formatDiff produces a unified-diff style preview of the change between two
// file versions. Each output line begins with a single marker character
// (' ', '+', '-') followed by the line text — we deliberately omit line
// numbers to keep the diff compact and easy to read. Identical context at
// the start and end of the file is collapsed into one "... N unchanged
// lines ..." row so we don't print redundant context twice.
func formatDiff(oldContent, newContent string) string {
	oldLines := splitTrailingNL(oldContent)
	newLines := splitTrailingNL(newContent)

	if len(oldLines) == 0 && len(newLines) == 0 {
		return "No changes"
	}

	m, n := len(oldLines), len(newLines)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	type op int
	const (
		opEq op = iota
		opDel
		opIns
	)
	type hunk struct {
		kind op
		old  []string
		new  []string
		skip int
	}
	var hunks []hunk
	var (
		bufEqOld, bufEqNew []string
		bufDel             []string
		bufIns             []string
	)
	flushEq := func() {
		if len(bufEqOld) == 0 {
			return
		}
		hunks = append(hunks, hunk{kind: opEq, old: append([]string(nil), bufEqOld...), new: append([]string(nil), bufEqNew...)})
		bufEqOld, bufEqNew = nil, nil
	}
	flushDel := func() {
		if len(bufDel) == 0 {
			return
		}
		hunks = append(hunks, hunk{kind: opDel, old: append([]string(nil), bufDel...)})
		bufDel = nil
	}
	flushIns := func() {
		if len(bufIns) == 0 {
			return
		}
		hunks = append(hunks, hunk{kind: opIns, new: append([]string(nil), bufIns...)})
		bufIns = nil
	}

	i, j := 0, 0
	canEqStep := func() bool { return i < m && j < n && oldLines[i] == newLines[j] }
	for i < m || j < n {
		if canEqStep() {
			flushDel()
			flushIns()
			for canEqStep() {
				bufEqOld = append(bufEqOld, oldLines[i])
				bufEqNew = append(bufEqNew, newLines[j])
				i++
				j++
			}
			flushEq()
			continue
		}
		if j >= n || (i < m && dp[i+1][j] >= dp[i][j+1]) {
			bufDel = append(bufDel, oldLines[i])
			i++
		} else {
			bufIns = append(bufIns, newLines[j])
			j++
		}
	}
	flushDel()
	flushIns()
	flushEq()

	// Collapse redundant outer opEq blocks: keep at most contextLines of
	// lines on each end, drop the middle into a single skip marker.
	const contextLines = 2
	collapse := func(h hunk) []hunk {
		if h.kind != opEq || len(h.old) <= contextLines*2 {
			return []hunk{h}
		}
		return []hunk{
			{kind: opEq, old: h.old[:contextLines], new: h.new[:contextLines]},
			{kind: opEq, skip: len(h.old) - contextLines*2},
			{kind: opEq, old: h.old[len(h.old)-contextLines:], new: h.new[len(h.new)-contextLines:]},
		}
	}
	if len(hunks) > 0 && hunks[0].kind == opEq {
		hunks = append(collapse(hunks[0]), hunks[1:]...)
	}
	if len(hunks) > 0 && hunks[len(hunks)-1].kind == opEq {
		last := hunks[len(hunks)-1]
		hunks = hunks[:len(hunks)-1]
		hunks = append(hunks, collapse(last)...)
	}

	var rows []string
	for _, h := range hunks {
		if h.kind == opEq && h.skip > 0 {
			rows = append(rows, fmt.Sprintf("   ... %d unchanged line%s ...", h.skip, plural(h.skip)))
			continue
		}
		switch h.kind {
		case opEq:
			for _, line := range h.old {
				rows = append(rows, " "+line)
			}
		case opDel:
			for _, line := range h.old {
				rows = append(rows, "-"+line)
			}
		case opIns:
			for _, line := range h.new {
				rows = append(rows, "+"+line)
			}
		}
	}
	if len(rows) == 0 {
		return "No changes"
	}
	// If nothing actually changed (all hunks are opEq) collapse to a single
	// notice so we don't render a context-only diff that hides the message.
	hadChange := false
	for _, h := range hunks {
		if h.kind != opEq {
			hadChange = true
			break
		}
	}
	if !hadChange {
		return "No changes"
	}
	return strings.Join(rows, "\n")
}

func splitTrailingNL(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (th *ToolHandler) resolvePath(path string) (string, error) {
	base, err := filepath.Abs(th.workingDir)
	if err != nil {
		return "", err
	}
	if resolvedBase, err := filepath.EvalSymlinks(base); err == nil {
		base = resolvedBase
	}

	target := strings.TrimSpace(path)
	if target == "" {
		target = "."
	}
	// Normalise leading "./" so model-side paths and tool-side paths agree.
	for strings.HasPrefix(target, "./") || strings.HasPrefix(target, ".\\") {
		target = strings.TrimPrefix(target, "./")
		target = strings.TrimPrefix(target, ".\\")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}

	target, err = filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", err
	}

	checkPath := target
	if resolvedTarget, err := filepath.EvalSymlinks(target); err == nil {
		checkPath = resolvedTarget
	} else {
		parent := filepath.Dir(target)
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr == nil {
			checkPath = filepath.Join(resolvedParent, filepath.Base(target))
		}
	}

	rel, err := filepath.Rel(base, checkPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path is outside working directory: %s", path)
	}

	return target, nil
}

// displayPath converts an absolute filesystem path to its canonical
// relative form under the working directory. The result always carries a
// leading "./" so that "./foo.go" and "foo.go" stay visually distinct as
// paths, but never a stray leading "././" or doubled slashes. Returns the
// absolute path unchanged if it lives outside the working dir.
func (th *ToolHandler) displayPath(absPath string) string {
	return displayPath(absPath, th.workingDir)
}

func displayPath(absPath, workingDir string) string {
	base, err := filepath.Abs(workingDir)
	if err != nil {
		return absPath
	}
	if resolvedBase, err := filepath.EvalSymlinks(base); err == nil {
		base = resolvedBase
	}
	target := absPath
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return absPath
	}
	if rel == "." || rel == "" {
		return "."
	}
	return "./" + filepath.ToSlash(rel)
}

const maxToolOutputSize = 50 * 1024 // 50KB

func FormatToolResult(result ToolResult) string {
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("Error: %s", result.Error)
		}
		return "Error: unknown error"
	}
	if result.Output != "" {
		if len(result.Output) > maxToolOutputSize {
			truncated := result.Output[:maxToolOutputSize]
			lastNewline := strings.LastIndex(truncated, "\n")
			if lastNewline > 0 {
				truncated = truncated[:lastNewline]
			}
			return truncated + fmt.Sprintf("\n... output truncated (%d bytes omitted)", len(result.Output)-len(truncated))
		}
		return result.Output
	}
	return "Success"
}

func FormatToolResultCLI(result ToolResult, toolName, args string) string {
	var output strings.Builder

	var toolLabel string
	switch toolName {
	case "list_files":
		var params struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			path := params.Path
			if path == "" {
				path = "."
			}
			toolLabel = "list " + path
		} else {
			toolLabel = "list_files"
		}
	case "glob":
		var params struct {
			Path    string `json:"path"`
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			toolLabel = "glob " + params.Pattern
			if params.Path != "" {
				toolLabel += " (" + params.Path + ")"
			}
		} else {
			toolLabel = "glob"
		}
	case "bash":
		var params struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			cmd := params.Command
			if len(cmd) > 40 {
				cmd = cmd[:37] + "..."
			}
			toolLabel = "bash: " + cmd
		} else {
			toolLabel = "bash"
		}
	case "read_files":
		var params struct {
			Paths  []string `json:"paths"`
			Offset int      `json:"offset,omitempty"`
			Limit  int      `json:"limit,omitempty"`
		}
		if json.Unmarshal([]byte(args), &params) == nil && len(params.Paths) > 0 {
			if len(params.Paths) == 1 {
				toolLabel = "read " + params.Paths[0]
				if params.Offset > 0 || params.Limit > 0 {
					toolLabel += fmt.Sprintf(" (line %d", params.Offset+1)
					if params.Limit > 0 {
						toolLabel += fmt.Sprintf(", %d lines", params.Limit)
					}
					toolLabel += ")"
				}
			} else {
				toolLabel = fmt.Sprintf("read_files (%d)", len(params.Paths))
			}
		} else {
			toolLabel = "read_files"
		}
	case "grep":
		var params struct {
			Pattern string `json:"pattern"`
			Include string `json:"include,omitempty"`
			Path    string `json:"path,omitempty"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			toolLabel = "grep " + params.Pattern
			if params.Include != "" {
				toolLabel += " in " + params.Include
			}
		} else {
			toolLabel = "grep"
		}
	case "write_file":
		var params struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			toolLabel = "write " + params.Path
		} else {
			toolLabel = "write_file"
		}
	case "edit_file":
		var params struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			toolLabel = "edit " + params.Path
		} else {
			toolLabel = "edit_file"
		}
	case "calculate":
		var params struct {
			Expression string `json:"expression"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			if result.Success && result.Output != "" {
				toolLabel = "calculate " + params.Expression + " = " + result.Output
			} else {
				toolLabel = "calculate: " + params.Expression
			}
		} else {
			toolLabel = "calculate"
		}
	default:
		toolLabel = toolName
	}

	output.WriteString(fmt.Sprintf("> %s\n", toolLabel))

	if !result.Success && result.Error != "" {
		output.WriteString(fmt.Sprintf("Error: %s\n", result.Error))
		return output.String()
	}

	if result.Output != "" {
		lines := strings.Split(result.Output, "\n")
		maxWidth := 80
		if len(lines) > 10 {
			lines = append(lines[:10], fmt.Sprintf("... %d more lines", len(lines)-10))
		}
		for _, line := range lines {
			if len(line) > maxWidth {
				line = line[:maxWidth-3] + "..."
			}
			output.WriteString(line + "\n")
		}
	}

	return output.String()
}

func GetToolDefinitions() []any {
	defs := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "bash",
				"description": "Execute a bash command in the working directory",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{"type": "string", "description": "The bash command to execute"},
						"reason":  map[string]any{"type": "string", "description": "Required: Explain why you are using this tool"},
					},
					"required": []string{"command", "reason"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "list_files",
				"description": "List files in a directory under the working directory",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "The directory path"},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_files",
				"description": "Read one or more files under the working directory. Prefer batching paths together in a single call instead of issuing one read per file, even when you only need the headlines of each.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"paths": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
							"description": "Files to read. Always batch multiple files into a single call when you need them together — this is much cheaper than many separate calls.",
						},
						"offset": map[string]any{"type": "integer", "description": "Line number to start reading from (0-based); applies to every file in the batch."},
						"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read per file (default: 6000 chars)."},
					},
					"required": []string{"paths"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "write_file",
				"description": "Write content to a file under the working directory",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": "The file path"},
						"content": map[string]any{"type": "string", "description": "The content to write"},
					},
					"required": []string{"path", "content", "reason"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "edit_file",
				"description": "Find and replace text in a file (replaces only the first occurrence)",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": "The file path"},
						"find":    map[string]any{"type": "string", "description": "Text to find"},
						"replace": map[string]any{"type": "string", "description": "Text to replace it with"},
					},
					"required": []string{"path", "find", "replace", "reason"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "grep",
				"description": "Search for a pattern in files",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern": map[string]any{"type": "string", "description": "The regex pattern to search for"},
						"path":    map[string]any{"type": "string", "description": "Directory to search in (default: current directory)"},
						"include": map[string]any{"type": "string", "description": "File pattern to include (e.g., *.go, *.js)"},
						"context": map[string]any{"type": "integer", "description": "Lines of context around matches (default: 2)"},
					},
					"required": []string{"pattern"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "glob",
				"description": "Find files matching a pattern",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": "Directory to search in (default: current directory)"},
						"pattern": map[string]any{"type": "string", "description": "Glob pattern (e.g., *.go, **/*.txt)"},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "file_info",
				"description": "Get information about a file or directory",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "The file or directory path"},
					},
					"required": []string{"path"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "calculate",
				"description": "Evaluate a mathematical expression with 100% certainty. Supports +, -, *, /, ^ (power), ! (factorial), and parentheses.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"expression": map[string]any{"type": "string", "description": "Mathematical expression to evaluate (e.g., '2 + 2', '(5 * 3) / 2', '2^10', '5!')"},
					},
					"required": []string{"expression"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "edit_soul",
				"description": "Edit the agent's SOUL.md file in the cardinal config directory",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"find":    map[string]any{"type": "string", "description": "Text to find in SOUL.md"},
						"replace": map[string]any{"type": "string", "description": "Text to replace it with"},
					},
					"required": []string{"find", "replace", "reason"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "subagent",
				"description": "Launch a sub-agent to perform a task. The sub-agent uses the same tools as the main agent but runs in a separate context.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"profile":       map[string]any{"type": "string", "description": "Model profile to use: fast, smart, or tiny (default: fast)"},
						"prompt":        map[string]any{"type": "string", "description": "Task description for the sub-agent"},
						"system_add_on": map[string]any{"type": "string", "description": "Additional system instructions for the sub-agent"},
					},
					"required": []string{"prompt"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "subagent_status",
				"description": "Get the status and result of a sub-agent task",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task_id": map[string]any{"type": "string", "description": "The task ID returned from subagent"},
					},
					"required": []string{"task_id"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "subagent_list",
				"description": "List all active sub-agent tasks",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "subagent_clear",
				"description": "Clear completed sub-agent tasks",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
	}
	return append(defs, getTodoToolDefinitions()...)
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

// ExecuteParallel runs multiple tool calls concurrently when they are independent.
// It detects dependencies between calls (e.g., one call depends on the output of
// a previous call) and runs dependent calls sequentially while independent ones
// run in parallel. Independent calls are those that don't write to the same file
// or depend on a previous call's output path.
func (th *ToolHandler) ExecuteParallel(calls []ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))

	// Group calls into batches of independent operations
	batches := th.groupIndependentCalls(calls)

	for _, batch := range batches {
		if len(batch) == 1 {
			// Single call - just execute it
			i := batch[0]
			results[i] = th.Execute(calls[i])
		} else {
			// Multiple independent calls - execute in parallel
			var wg sync.WaitGroup
			for _, idx := range batch {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					results[i] = th.Execute(calls[i])
				}(idx)
			}
			wg.Wait()
		}
	}

	return results
}

// groupIndependentCalls analyzes tool calls and groups independent ones together
// so they can be executed in parallel. Calls are dependent if:
// - A write_file or edit_file targets a path that a previous read_files references
// - An edit_file targets a path that a previous edit_file or write_file also targets
// - A bash command might depend on a previous write_file/edit_file
func (th *ToolHandler) groupIndependentCalls(calls []ToolCall) [][]int {
	if len(calls) <= 1 {
		if len(calls) == 1 {
			return [][]int{{0}}
		}
		return nil
	}

	// Track which paths are written to and which are read from
	writtenPaths := make(map[string]bool)
	readPaths := make(map[string]bool)

	// Each batch is a list of indices that can run in parallel
	var batches [][]int
	currentBatch := []int{0}

	// Parse the first call to seed written/read paths
	th.trackPaths(calls[0], writtenPaths, readPaths)

	for i := 1; i < len(calls); i++ {
		if th.isDependent(calls[i], writtenPaths, readPaths) {
			// This call depends on a previous one - flush current batch and start new
			if len(currentBatch) > 0 {
				batches = append(batches, currentBatch)
			}
			currentBatch = []int{i}
			// Reset tracking for the new batch context
			writtenPaths = make(map[string]bool)
			readPaths = make(map[string]bool)
			th.trackPaths(calls[i], writtenPaths, readPaths)
		} else {
			// Independent - can run in same batch
			currentBatch = append(currentBatch, i)
			th.trackPaths(calls[i], writtenPaths, readPaths)
		}
	}

	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}

	return batches
}

// isDependent checks if a tool call depends on any previously tracked write/read paths
func (th *ToolHandler) isDependent(call ToolCall, writtenPaths, readPaths map[string]bool) bool {
	// read_files: check each path against writtenPaths
	if call.Name == "read_files" {
		var params struct {
			Paths []string `json:"paths"`
		}
		if err := json.Unmarshal([]byte(call.Args), &params); err == nil {
			for _, p := range params.Paths {
				if writtenPaths[p] {
					return true
				}
			}
		}
		return false
	}

	path, isWrite := th.getCallPathAndType(call)
	if path == "" {
		// bash commands are treated as potentially dependent on writes
		if call.Name == "bash" {
			return len(writtenPaths) > 0
		}
		return false
	}

	// If this call writes to a path that was already written, it's dependent
	if isWrite {
		if writtenPaths[path] {
			return true
		}
		// If this call writes to a path that was previously read by a write-dependent call, it's dependent
		if readPaths[path] {
			return true
		}
	}

	// If this call reads from a path that was written to, it's dependent
	if !isWrite && writtenPaths[path] {
		return true
	}

	return false
}

// trackPaths records the paths read/written by a tool call
func (th *ToolHandler) trackPaths(call ToolCall, writtenPaths, readPaths map[string]bool) {
	switch call.Name {
	case "read_files":
		var params struct {
			Paths []string `json:"paths"`
		}
		if err := json.Unmarshal([]byte(call.Args), &params); err == nil {
			for _, p := range params.Paths {
				if p != "" {
					readPaths[p] = true
				}
			}
		}
	default:
		path, isWrite := th.getCallPathAndType(call)
		if path != "" {
			if isWrite {
				writtenPaths[path] = true
			} else {
					readPaths[path] = true
			}
		}
	}
}

// getCallPathAndType extracts the target path and whether it's a write operation
func (th *ToolHandler) getCallPathAndType(call ToolCall) (string, bool) {
	switch call.Name {
	case "write_file", "edit_file":
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(call.Args), &params); err == nil && params.Path != "" {
			return params.Path, true
		}
	case "read_files":
		var params struct {
			Paths []string `json:"paths"`
		}
		if err := json.Unmarshal([]byte(call.Args), &params); err == nil {
			// Return first path for dependency tracking; all paths are reads.
			if len(params.Paths) > 0 {
				return params.Paths[0], false
			}
		}
	case "list_files":
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(call.Args), &params); err == nil && params.Path != "" {
			return params.Path, false
		}
	case "file_info":
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(call.Args), &params); err == nil && params.Path != "" {
			return params.Path, false
		}
	case "edit_soul":
		return "SOUL.md", true
	}
	return "", false
}
