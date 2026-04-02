package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"cardinal/pkg/storage"
)

type ToolResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
	Path    string `json:"path,omitempty"`
	Lines   string `json:"lines,omitempty"`
}

type ToolCall struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

type ToolHandler struct {
	workingDir string
	tools      map[string]func(string) ToolResult
}

func NewToolHandler(workingDir string) *ToolHandler {
	th := &ToolHandler{
		workingDir: workingDir,
		tools:      make(map[string]func(string) ToolResult),
	}

	th.tools["bash"] = th.executeBash
	th.tools["list_files"] = th.executeListFiles
	th.tools["read_file"] = th.executeReadFile
	th.tools["write_file"] = th.executeWriteFile
	th.tools["edit_file"] = th.executeEditFile
	th.tools["grep"] = th.executeGrep
	th.tools["glob"] = th.executeGlob
	th.tools["file_info"] = th.executeFileInfo
	th.tools["edit_soul"] = th.executeEditSoul
	th.tools["todo_add"] = th.executeTodoAdd
	th.tools["todo_list"] = th.executeTodoList
	th.tools["todo_update"] = th.executeTodoUpdate
	th.tools["todo_remove"] = th.executeTodoRemove

	return th
}

func (th *ToolHandler) Execute(call ToolCall) ToolResult {
	executor, exists := th.tools[call.Name]
	if !exists {
		return ToolResult{Name: call.Name, Success: false, Error: fmt.Sprintf("unknown tool: %s", call.Name)}
	}
	return executor(call.Args)
}

func RequiresApproval(name string) bool {
	switch name {
	case "list_files", "read_file", "grep", "glob", "file_info", "edit_soul", "todo_list":
		return false
	default:
		return true
	}
}

func PermissionDeniedResult(name string) ToolResult {
	return ToolResult{Name: name, Success: false, Error: "permission denied"}
}

func SummarizeCall(name, args string) string {
	switch name {
	case "bash":
		var params struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Command) != "" {
			return "$ " + truncate(params.Command, 60)
		}
	case "read_file":
		var params struct {
			Path   string `json:"path"`
			Offset int    `json:"offset,omitempty"`
			Limit  int    `json:"limit,omitempty"`
		}
		if err := json.Unmarshal([]byte(args), &params); err == nil && strings.TrimSpace(params.Path) != "" {
			result := "read: " + params.Path
			if params.Offset > 0 || params.Limit > 0 {
				result += fmt.Sprintf(" (line %d", params.Offset+1)
				if params.Limit > 0 {
					result += fmt.Sprintf(", %d lines", params.Limit)
				}
				result += ")"
			}
			return result
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
	return truncate(name+" "+args, 60)
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

	return ToolResult{Name: "list_files", Success: true, Output: strings.Join(files, "\n")}
}

func (th *ToolHandler) executeReadFile(args string) ToolResult {
	var params struct {
		Path   string `json:"path"`
		Offset int    `json:"offset,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolResult{Name: "read_file", Success: false, Error: err.Error()}
	}

	if strings.TrimSpace(params.Path) == "" {
		return ToolResult{Name: "read_file", Success: false, Error: "no file path provided"}
	}

	fullPath, err := th.resolvePath(params.Path)
	if err != nil {
		return ToolResult{Name: "read_file", Success: false, Error: err.Error()}
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return ToolResult{Name: "read_file", Success: false, Error: err.Error()}
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= totalLines {
		return ToolResult{Name: "read_file", Success: false, Error: "offset beyond file length"}
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 6000
	}

	end := offset + limit
	if end > totalLines {
		end = totalLines
	}

	selectedLines := lines[offset:end]
	output := strings.Join(selectedLines, "\n")

	if len(output) > 6000 {
		output = output[:6000]
		output += "\n... (truncated)"
	}

	return ToolResult{Name: "read_file", Success: true, Output: output, Path: params.Path, Lines: fmt.Sprintf("%d-%d/%d", offset+1, end, totalLines)}
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
	if params.Content == "" {
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
	if params.Find == "" {
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
		return ToolResult{Name: "edit_file", Success: false, Error: "Text not found: '" + params.Find + "' was not found in the file. Use read_file tool to see the exact content, paying attention to whitespace and special characters."}
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

	matches, err := filepath.Glob(filepath.Join(fullPath, pattern))
	if err != nil {
		return ToolResult{Name: "glob", Success: false, Error: err.Error()}
	}

	if len(matches) == 0 {
		return ToolResult{Name: "glob", Success: true, Output: "No matches found"}
	}

	var results []string
	for _, match := range matches {
		relPath, _ := filepath.Rel(th.workingDir, match)
		info, _ := os.Stat(match)
		prefix := "[FILE] "
		if info.IsDir() {
			prefix = "[DIR]  "
		}
		results = append(results, prefix+relPath)
	}

	return ToolResult{Name: "glob", Success: true, Output: strings.Join(results, "\n")}
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

	return ToolResult{Name: "edit_soul", Success: true, Output: fmt.Sprintf("Updated SOUL.md in %s", configDir)}
}

func formatDiff(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var diff []string
	diff = append(diff, fmt.Sprintf("success=\"true\" path=\"edit\""))

	for i := 0; i < len(oldLines) || i < len(newLines); i++ {
		oldLine := ""
		newLine := ""
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}

		if oldLine != newLine {
			if oldLine != "" {
				diff = append(diff, fmt.Sprintf("-%d: %s", i+1, oldLine))
			}
			if newLine != "" {
				diff = append(diff, fmt.Sprintf("+%d: %s", i+1, newLine))
			}
		}
	}

	return strings.Join(diff, "\n")
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

func FormatToolResult(result ToolResult) string {
	var output strings.Builder
	output.WriteString(fmt.Sprintf("<tool_result name=\"%s\" success=\"%v\"", result.Name, result.Success))
	if result.Path != "" {
		output.WriteString(fmt.Sprintf(" path=\"%s\"", result.Path))
	}
	if result.Lines != "" {
		output.WriteString(fmt.Sprintf(" lines=\"%s\"", result.Lines))
	}
	output.WriteString(">\n")
	if result.Output != "" {
		output.WriteString(result.Output + "\n")
	}
	if result.Error != "" {
		output.WriteString(fmt.Sprintf("Error: %s\n", result.Error))
	}
	output.WriteString("</tool_result>")
	return output.String()
}

func GetToolDefinitions() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "bash",
				"description": "Execute a bash command in the working directory",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{"type": "string", "description": "The bash command to execute"},
					},
					"required": []string{"command"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "list_files",
				"description": "List files in a directory under the working directory",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string", "description": "The directory path"},
					},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "read_file",
				"description": "Read the contents of a file under the working directory",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":   map[string]interface{}{"type": "string", "description": "The file path"},
						"offset": map[string]interface{}{"type": "integer", "description": "Line number to start reading from (0-based)"},
						"limit":  map[string]interface{}{"type": "integer", "description": "Maximum number of lines to read (default: all or 6000 chars)"},
					},
					"required": []string{"path"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "write_file",
				"description": "Write content to a file under the working directory",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "The file path"},
						"content": map[string]interface{}{"type": "string", "description": "The content to write"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "edit_file",
				"description": "Find and replace text in a file (replaces only the first occurrence)",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "The file path"},
						"find":    map[string]interface{}{"type": "string", "description": "Text to find"},
						"replace": map[string]interface{}{"type": "string", "description": "Text to replace it with"},
					},
					"required": []string{"path", "find", "replace"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "grep",
				"description": "Search for a pattern in files",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{"type": "string", "description": "The regex pattern to search for"},
						"path":    map[string]interface{}{"type": "string", "description": "Directory to search in (default: current directory)"},
						"include": map[string]interface{}{"type": "string", "description": "File pattern to include (e.g., *.go, *.js)"},
						"context": map[string]interface{}{"type": "integer", "description": "Lines of context around matches (default: 2)"},
					},
					"required": []string{"pattern"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "glob",
				"description": "Find files matching a pattern",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "Directory to search in (default: current directory)"},
						"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern (e.g., *.go, **/*.txt)"},
					},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "file_info",
				"description": "Get information about a file or directory",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string", "description": "The file or directory path"},
					},
					"required": []string{"path"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "edit_soul",
				"description": "Edit the agent's SOUL.md file in the cardinal config directory",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"find":    map[string]interface{}{"type": "string", "description": "Text to find in SOUL.md"},
						"replace": map[string]interface{}{"type": "string", "description": "Text to replace it with"},
					},
					"required": []string{"find", "replace"},
				},
			},
		},
	}
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
