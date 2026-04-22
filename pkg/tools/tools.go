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
	onEditSoul func()
}

func NewToolHandler(workingDir string, onEditSoul func()) *ToolHandler {
	th := &ToolHandler{
		workingDir: workingDir,
		tools:      make(map[string]func(string) ToolResult),
		onEditSoul: onEditSoul,
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
	th.tools["calculate"] = th.executeCalculate
	th.tools["todo_add"] = th.executeTodoAdd
	th.tools["todo_list"] = th.executeTodoList
	th.tools["todo_update"] = th.executeTodoUpdate
	th.tools["todo_remove"] = th.executeTodoRemove
	th.tools["subagent"] = th.executeSubAgent
	th.tools["subagent_status"] = th.executeSubAgentStatus
	th.tools["subagent_list"] = th.executeSubAgentList
	th.tools["subagent_clear"] = th.executeSubAgentClear

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
	case "list_files", "read_file", "grep", "glob", "file_info", "edit_soul", "calculate", "todo_list", "subagent", "subagent_status", "subagent_list", "subagent_clear":
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

	displayPath := params.Path
	if displayPath == "" {
		displayPath = "."
	}

	return ToolResult{Name: "list_files", Success: true, Output: strings.Join(files, "\n"), Path: displayPath}
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

func formatDiff(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	start := 0
	for start < len(oldLines) && start < len(newLines) && oldLines[start] == newLines[start] {
		start++
	}

	oldEnd := len(oldLines) - 1
	newEnd := len(newLines) - 1
	for oldEnd >= start && newEnd >= start && oldLines[oldEnd] == newLines[newEnd] {
		oldEnd--
		newEnd--
	}

	if start > oldEnd && start > newEnd {
		return "No changes"
	}

	contextLines := 2
	ctxStart := start - contextLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxOldEnd := oldEnd + contextLines
	if ctxOldEnd >= len(oldLines) {
		ctxOldEnd = len(oldLines) - 1
	}
	ctxNewEnd := newEnd + contextLines
	if ctxNewEnd >= len(newLines) {
		ctxNewEnd = len(newLines) - 1
	}

	var diff []string
	for i := ctxStart; i < start; i++ {
		diff = append(diff, fmt.Sprintf(" %d: %s", i+1, oldLines[i]))
	}
	for i := start; i <= oldEnd; i++ {
		diff = append(diff, fmt.Sprintf("-%d: %s", i+1, oldLines[i]))
	}
	for i := start; i <= newEnd; i++ {
		diff = append(diff, fmt.Sprintf("+%d: %s", i+1, newLines[i]))
	}
	for i := oldEnd + 1; i <= ctxOldEnd; i++ {
		diff = append(diff, fmt.Sprintf(" %d: %s", i+1, oldLines[i]))
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
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("Error: %s", result.Error)
		}
		return "Error: unknown error"
	}
	if result.Output != "" {
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
	case "read_file":
		var params struct {
			Path   string `json:"path"`
			Offset int    `json:"offset,omitempty"`
			Limit  int    `json:"limit,omitempty"`
		}
		if json.Unmarshal([]byte(args), &params) == nil {
			toolLabel = "read " + params.Path
			if params.Offset > 0 || params.Limit > 0 {
				toolLabel += fmt.Sprintf(" (line %d", params.Offset+1)
				if params.Limit > 0 {
					toolLabel += fmt.Sprintf(", %d lines", params.Limit)
				}
				toolLabel += ")"
			}
		} else {
			toolLabel = "read_file"
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
					},
					"required": []string{"command"},
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
				"name":        "read_file",
				"description": "Read the contents of a file under the working directory",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": "The file path"},
						"offset": map[string]any{"type": "integer", "description": "Line number to start reading from (0-based)"},
						"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read (default: all or 6000 chars)"},
					},
					"required": []string{"path"},
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
					"required": []string{"path", "content"},
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
					"required": []string{"path", "find", "replace"},
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
					"required": []string{"find", "replace"},
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
