package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"cardinal/pkg/api"

	"github.com/charmbracelet/lipgloss"
)

var (
	accentColor  = lipgloss.Color("63")
	userColor    = lipgloss.Color("42")
	aiColor      = lipgloss.Color("213")
	toolColor    = lipgloss.Color("214")
	successColor = lipgloss.Color("42")
	warningColor = lipgloss.Color("214")
	errorColor   = lipgloss.Color("196")
	dimColor     = lipgloss.Color("244")
	panelColor   = lipgloss.Color("238")

	dimStyle      = lipgloss.NewStyle().Foreground(dimColor)
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	subtitleStyle = lipgloss.NewStyle().Foreground(dimColor)
	errorStyle    = lipgloss.NewStyle().Foreground(errorColor).Bold(true)
	successStyle  = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	toolStyle     = lipgloss.NewStyle().Foreground(toolColor).Bold(true)

	// Diff rendering: proper unified-diff with single-character prefixes.
	// Foreground colors keep the +/- legible against any theme.
	addStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	delStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	ctxDiffStyle = dimStyle

	userMessageStyle      = lipgloss.NewStyle().Foreground(userColor)
	assistantMessageStyle = lipgloss.NewStyle().Foreground(aiColor)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(panelColor).
			Padding(0, 1)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).
			Padding(0, 1)

	headerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(panelColor).
			BorderBottom(true)
)

func (m Model) View() string {
	if m.mode != "" {
		return m.renderShell(m.renderMode())
	}
	return m.renderMainView()
}

func (m Model) renderShell(body string) string {
	header := m.renderHeader()
	if header == "" {
		return body
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

func (m Model) renderMainView() string {
	sections := []string{m.renderHeader()}

	// Inline tool-approval card (replaces the legacy full-screen perms page).
	if m.pendingApproval != nil {
		sections = append(sections, m.renderInlineApproval())
	}

	if m.useViewport && m.viewport.Height > 0 {
		sections = append(sections, m.viewport.View())
	} else {
		sections = append(sections, m.renderConversation())
	}

	if len(m.suggestions) > 0 {
		sections = append(sections, m.renderSuggestions())
	}
	if m.busy {
		sections = append(sections, m.renderThrobber())
	}

	sections = append(sections, "", m.renderModelRule(), m.renderInput())
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderModelRule is the divider row between the chat / approval card and
// the prompt. It shows context usage on the left and the current model name
// on the right.
func (m Model) renderModelRule() string {
	width := max(m.width, 40)
	var left string
	if m.contextUsed > 0 && m.contextLimit > 0 {
		pct := int(float64(m.contextUsed) / float64(m.contextLimit) * 100)
		left = lipgloss.NewStyle().Foreground(dimColor).Render("ctx " + strconv.Itoa(pct) + "%")
	} else {
		left = lipgloss.NewStyle().Foreground(dimColor).Render(strings.Repeat("─", 4))
	}
	model := m.cfg.Model
	if model == "" {
		model = "no model"
	}
	right := lipgloss.NewStyle().Foreground(dimColor).Render(model)
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	gap := max(width-leftWidth-rightWidth-2, 4)
	rule := lipgloss.NewStyle().Foreground(panelColor).Render(strings.Repeat("─", gap))
	return left + " " + rule + " " + right
}

func (m Model) renderInlineApproval() string {
	data := m.pendingApproval
	var rows []string
	rows = append(rows,
		titleStyle.Render("Approve tools")+"  "+dimStyle.Render("Space toggle · a allow · r reject · A all · R none · Enter run · Esc skip"))
	for i, tc := range data.toolCalls {
		prefix := "  "
		if i == data.selected {
			prefix = "› "
		}
		mark := lipgloss.NewStyle().Foreground(errorColor).Render("✗")
		if data.approvals[i] {
			mark = lipgloss.NewStyle().Foreground(successColor).Render("✓")
		}
		line := fmt.Sprintf("%s%s %s", prefix, mark, tc.Function.Name)
		args := oneLine(tc.Function.Arguments, max(m.width-14, 30))
		if args != "" {
			line += dimStyle.Render("  " + args)
		}
		if i == data.selected {
			line = lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(line)
		}
		rows = append(rows, line)
	}
	body := strings.Join(rows, "\n")
	return modePanel(m.width, body)
}

func (m Model) renderHeader() string {
	return ""
}

func (m Model) renderThrobber() string {
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "Working"
	}
	color := getStatusColor(status)
	return lipgloss.NewStyle().Foreground(color).Bold(true).Render(m.spinner.View() + " " + status)
}

func getStatusColor(status string) lipgloss.Color {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "error"), strings.Contains(lower, "fail"), strings.Contains(lower, "denied"):
		return errorColor
	case strings.Contains(lower, "permission"), strings.Contains(lower, "approve"):
		return aiColor
	case strings.Contains(lower, "running"), strings.Contains(lower, "tool"), strings.Contains(lower, "retry"):
		return warningColor
	case strings.Contains(lower, "ready"), strings.Contains(lower, "updated"), strings.Contains(lower, "saved"):
		return successColor
	default:
		return accentColor
	}
}

func (m Model) renderMode() string {
	switch m.mode {
	case "models":
		return m.renderModelsMode()
	case "profiles":
		return m.renderProfilesMode()
	case "profileForm":
		return m.renderProfileFormMode()
	case "endpoint", "apikey":
		return m.renderTextInputMode()
	case "permissions":
		return m.renderPermissionsMode()
	default:
		return m.renderConversation()
	}
}

func (m Model) renderConversation() string {
	hasStreaming := strings.TrimSpace(m.streaming) != ""
	hasThinking := strings.TrimSpace(m.thinking) != ""
	if len(m.messages) == 0 && !hasStreaming && !hasThinking && m.err == nil && len(m.systemNotes) == 0 {
		return m.renderWelcome()
	}
	var body string
	if len(m.messages) == 0 && !hasStreaming && !hasThinking && m.err == nil {
		body = m.renderSystemNotes()
	} else {
		body = m.renderChatHistory(m.messages, m.scrollOffset, hasStreaming, hasThinking, m.err)
		body = joinWithSystemNotes(body, m.renderSystemNotes())
	}
	return body
}

func (m Model) renderSystemNotes() string {
	if len(m.systemNotes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.systemNotes))
	for _, note := range m.systemNotes {
		glyph := lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("✦")
		text := lipgloss.NewStyle().Foreground(dimColor).Render(" " + note)
		parts = append(parts, glyph+text)
	}
	return strings.Join(parts, "\n")
}

func joinWithSystemNotes(chat, notes string) string {
	chat = strings.TrimRight(chat, "\n")
	notes = strings.TrimRight(notes, "\n")
	switch {
	case chat == "" && notes == "":
		return ""
	case chat == "":
		return notes
	case notes == "":
		return chat
	default:
		return notes + "\n\n" + chat
	}
}

func (m Model) renderChatHistory(messages []api.Message, scrollOffset int, hasStreaming, hasThinking bool, chatErr error) string {
	var blocks []string
	i := 0
	for i < len(messages) {
		msg := messages[i]
		if msg.Role == "tool" {
			run := m.collectToolRun(messages, i)
			if rendered := m.renderToolRun(run); rendered != "" {
				blocks = append(blocks, rendered)
			}
			i += run.count
			continue
		}
		if rendered := m.renderMessage(i, msg); rendered != "" {
			blocks = append(blocks, rendered)
		}
		i++
	}
	if hasThinking || hasStreaming {
		blocks = append(blocks, m.renderStreamingMessage())
	}
	if chatErr != nil {
		blocks = append(blocks, m.messageCardPlain("Error", errorColor, chatErr.Error()))
	}
	if len(blocks) == 0 {
		return m.renderWelcome()
	}
	return strings.Join(blocks, "\n\n")
}

func (m Model) renderWelcome() string {
	width := min(max(m.width-4, 62), 96)
	logo := []string{
		"  _________                  .___.__              .__                               ",
		"  \\_   ___ \\_____ _______  __| _/|__| ____ _____  |  |                              ",
		"  /    \\  \\/\\__  \\_  __ \\ / __ | |  |/    \\__   \\ |  |                             ",
		"  \\     \\____/ __ \\|  | \\/ /_/ | |  |   |  \\/ __ \\|  |__                           ",
		"   \\______  (____  /__|  \\____ | |__|___|  (____  /____/                            ",
		"          \\/     \\/           \\/         \\/     \\/                                  ",
	}
	for i, line := range logo {
		logo[i] = lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(line)
	}
	commands := []string{
		lipgloss.NewStyle().Foreground(accentColor).Render("/models") + dimStyle.Render("       choose model"),
		lipgloss.NewStyle().Foreground(accentColor).Render("/profiles") + dimStyle.Render("     switch endpoint/profile"),
		lipgloss.NewStyle().Foreground(accentColor).Render("/autoapprove") + dimStyle.Render("  run tools without prompts"),
		lipgloss.NewStyle().Foreground(accentColor).Render("/help") + dimStyle.Render("         show commands"),
	}
	body := strings.Join(logo, "\n") + "\n\n" +
		dimStyle.Render("Ready to code") + "\n" +
		dimStyle.Render("Ask for edits, debugging, refactors, command output, or code search.") + "\n\n" +
		strings.Join(commands, "\n") + "\n\n" +
		dimStyle.Render("Enter sends · Esc cancels · ↑/↓ prompt history")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(panelColor).
		Padding(1, 2).
		Width(width).
		Render(body)
}

func (m Model) renderMessage(msgIndex int, msg api.Message) string {
	if msg.Role == "system" {
		return ""
	}
	if msg.Role == "tool" {
		return ""
	}

	label, color := roleMeta(msg)
	content := strings.TrimSpace(msg.Content)
	thinking := strings.TrimSpace(msg.Thinking)
	var parts []string
	if thinking != "" && m.showThinking {
		preview := oneLine(thinking, max(m.width-12, 24))
		parts = append(parts, m.messageCardPlain(label+" thinking", dimColor, preview))
	}
	if content != "" {
		if msg.Role == "user" && !looksLikeMarkdown(content) {
			parts = append(parts, m.messageCardPlain(label, color, content))
		} else {
			parts = append(parts, m.messageCard(label, color, content))
		}
	}
	return strings.Join(parts, "\n")
}

func (m Model) messageCard(title string, color lipgloss.Color, content string) string {
	return m.messageCardRendered(title, color, content, true)
}

// messageCardPlain is the legacy raw-text variant. Tool results already go
// through formatToolSummary (which produces colorized diffs/bullets/etc.); we
// don't want to feed that back through a markdown renderer.
func (m Model) messageCardPlain(title string, color lipgloss.Color, content string) string {
	return m.messageCardRendered(title, color, content, false)
}

func (m Model) messageCardRendered(title string, color lipgloss.Color, content string, useMarkdown bool) string {
	width := max(m.width-4, 30)
	header := lipgloss.NewStyle().Foreground(color).Bold(true).Render("● " + title)
	content = strings.TrimSpace(content)
	if content == "" {
		return header
	}
	body := content
	if useMarkdown && m.markdown != nil {
		// Glamour's renderer respects WithWordWrap, but it also emits
		// hard-wrapped blocks (headings, tables) at the configured width.
		body = m.markdown.Render(content)
	}
	body = lipgloss.NewStyle().PaddingLeft(2).Width(width).Render(body)
	return header + "\n" + body
}

func (m Model) renderToolResult(msg api.Message) string {
	return ""
}

type toolRun struct {
	start int
	count int
	name  string
	args  []string
	seq   []api.Message
}

func (m Model) collectToolRun(messages []api.Message, start int) toolRun {
	run := toolRun{start: start, name: messages[start].Name}
	for i := start; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role != "tool" || msg.Name != run.name {
			break
		}
		var args map[string]any
		_ = json.Unmarshal([]byte(msg.ToolArgs), &args)
		run.seq = append(run.seq, msg)
		run.count++
		if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
			// read_files: collect each normalised path
			for _, p := range paths {
				if s, ok := p.(string); ok && s != "" {
					run.args = append(run.args, normalisePath(s))
				}
			}
		} else if path := stringArg(args, "path"); path != "" {
			run.args = append(run.args, normalisePath(path))
		} else if pat := stringArg(args, "pattern"); pat != "" {
			run.args = append(run.args, pat)
		} else if cmd := stringArg(args, "command"); cmd != "" {
			run.args = append(run.args, oneLine(cmd, 40))
		} else if expr := stringArg(args, "expression"); expr != "" {
			run.args = append(run.args, expr)
		} else {
			run.args = append(run.args, "-")
		}
	}
	return run
}

func (m Model) renderToolRun(run toolRun) string {
	if run.count == 0 {
		return ""
	}

	// edit_file: show the colourised diff directly (no bullet prefix)
	if run.name == "edit_file" {
		var parts []string
		for _, msg := range run.seq {
			preview := m.formatToolSummary(msg, max(m.width-8, 32))
			if preview != "" {
				parts = append(parts, preview)
			}
		}
		return m.messageCardPlain(groupTitle(run.name, dedupArgs(run.name, run.args)), toolColor, strings.Join(parts, "\n"))
	}

	// Single read: just a bold title, no tree behind it.
	if run.name == "read_files" {
		if len(run.args) == 1 {
			title := "● " + groupTitle(run.name, run.args)
			return lipgloss.NewStyle().Foreground(toolColor).Bold(true).Render(title)
		}
		tree := m.renderReadFilesTree(run)
		if tree == "" {
			return ""
		}
		return m.messageCardPlain(groupTitle(run.name, run.args), toolColor, tree)
	}

	// bash: collapse trivial `cat <file>…` invocations into a Read.
	if run.name == "bash" {
		if files := bashCatFiles(run); files != nil {
			title := groupTitle("read_files", files)
			out := formatCatOutput(run)
			return m.messageCardPlain(title, toolColor, out)
		}
		args := dedupArgs(run.name, run.args)
		title := groupTitle(run.name, args)
		body := formatBashOutput(run)
		if body == "" {
			return ""
		}
		return m.messageCardPlain(title, toolColor, body)
	}

	// todo tools: render each run as a small tool card. A bare checklist
	// would float unattributed and read as part of the assistant's reply; we
	// want it to look like a tool result, even if the "title" is constant.
	if isTodoTool(run.name) {
		// todo_write returns the *full current list* on every call, so the
		// last snapshot supersedes earlier ones in the same run. Showing all
		// of them would repeat identical content.
		last := ""
		for _, msg := range run.seq {
			if preview := m.formatToolSummary(msg, max(m.width-8, 32)); preview != "" {
				last = preview
			}
		}
		if last == "" {
			return ""
		}
		return m.messageCardPlain("Todos", toolColor, last)
	}

	args := dedupArgs(run.name, run.args)
	title := groupTitle(run.name, args)
	bodyLines := []string{}
	for i, msg := range run.seq {
		preview := m.formatToolSummary(msg, max(m.width-8, 32))
		if preview != "" {
			bodyLines = append(bodyLines, "  • "+preview)
			if i >= 3 {
				break
			}
		}
	}
	return m.messageCardPlain(title, toolColor, strings.Join(bodyLines, "\n"))
}

// bashCatFiles returns the list of paths being `cat`'d if the entire run is
// a sequence of `cat <file>…` invocations (and nothing else). Returns nil
// if any call uses a different command or flags, so unrelated bash work
// keeps the regular "Ran <cmd>" rendering.
func bashCatFiles(run toolRun) []string {
	if run.name != "bash" || len(run.seq) == 0 {
		return nil
	}
	var files []string
	for _, msg := range run.seq {
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(msg.ToolArgs), &args); err != nil {
			return nil
		}
		cmd := strings.TrimSpace(args.Command)
		// strip any leading `command ` builtin prefix used to bypass aliases
		cmd = strings.TrimPrefix(cmd, "command ")
		if cmd == "" {
			return nil
		}
		fields := strings.Fields(cmd)
		if len(fields) < 2 || fields[0] != "cat" {
			return nil
		}
		// reject any flag; `cat -n foo` etc. shouldn't be silently collapsed
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") {
				return nil
			}
			files = append(files, normalisePath(f))
		}
	}
	return files
}

// formatBashOutput strips the duplicate "$ cmd" replay from a tool body
// because the title already shows the command. Falls back to plain content.
func formatBashOutput(run toolRun) string {
	var parts []string
	for _, msg := range run.seq {
		body := strings.TrimSpace(msg.Content)
		if body == "" {
			continue
		}
		parts = append(parts, compactLines(body, 12, 200))
	}
	return strings.Join(parts, "\n")
}

// formatCatOutput preserves the cat'd body verbatim — it's the only
// interesting signal of a Read-from-bash.
func formatCatOutput(run toolRun) string {
	var parts []string
	for _, msg := range run.seq {
		body := strings.TrimSpace(msg.Content)
		if body == "" {
			continue
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n")
}

func isTodoTool(name string) bool {
	switch name {
	case "todo_write", "todo_read":
		return true
	}
	return false
}

func (m Model) renderReadFilesTree(run toolRun) string {
	if len(run.seq) == 0 {
		return ""
	}
	// Use the last call's metadata: read_files returns the full set on every
	// consecutive call so showing the latest snapshot is the right behaviour.
	msg := run.seq[len(run.seq)-1]
	if strings.TrimSpace(msg.MetaLines) == "" || strings.TrimSpace(msg.MetaLines) == "[]" {
		return ""
	}
	type entry struct {
		Path      string `json:"path"`
		Range     string `json:"range,omitempty"`
		Truncated bool   `json:"truncated,omitempty"`
		Error     string `json:"error,omitempty"`
	}
	var entries []entry
	if err := json.Unmarshal([]byte(msg.MetaLines), &entries); err != nil || len(entries) == 0 {
		return ""
	}

	last := len(entries) - 1
	lines := []string{}
	for i, e := range entries {
		glyph := "├─"
		if i == last {
			glyph = "└─"
		}
		switch {
		case e.Error != "":
			lines = append(lines, fmt.Sprintf("%s %s — %s", glyph, normalisePath(e.Path), e.Error))
		case e.Range != "":
			display := e.Path + " (" + e.Range + ")"
			if e.Truncated {
				display += " …"
			}
			lines = append(lines, glyph+" "+display)
		default:
			// Full file: just the path, no parenthetical.
			lines = append(lines, glyph+" "+e.Path)
		}
	}
	return strings.Join(lines, "\n")
}

func dedupArgs(name string, args []string) []string {
	if len(args) == 0 {
		return args
	}
	switch name {
	case "read_files", "write_file", "edit_file", "file_info", "glob", "list_files":
	default:
		return args
	}
	counts := map[string]int{}
	order := []string{}
	for _, a := range args {
		if a == "-" {
			counts["_other"]++
			order = append(order, "_other")
			continue
		}
		if counts[a] == 0 {
			order = append(order, a)
		}
		counts[a]++
	}
	out := make([]string, 0, len(order))
	for _, a := range order {
		if n := counts[a]; n > 1 && a != "_other" {
			out = append(out, fmt.Sprintf("%s \u00d7%d", a, n))
		} else if a == "_other" {
			out = append(out, fmt.Sprintf("(other) \u00d7%d", counts["_other"]))
		} else {
			out = append(out, a)
		}
	}
	return out
}

func groupTitle(name string, args []string) string {
	switch name {
	case "list_files":
		return toolGroupName("Listed files in", args)
	case "read_files":
		if len(args) == 1 {
			return "Read " + args[0]
		}
		return fmt.Sprintf("Read %d files", len(args))
	case "write_file":
		return toolGroupName("Wrote", args)
	case "edit_file":
		return toolGroupName("Edited", args)
	case "bash":
		return toolGroupName("Ran", args)
	case "grep":
		return toolGroupName("Searched", args)
	case "glob":
		return toolGroupName("Matched", args)
	case "file_info":
		return toolGroupName("Inspected", args)
	case "calculate":
		return toolGroupName("Calculated", args)
	default:
		return toolGroupName(name, args)
	}
}

func toolGroupName(prefix string, args []string) string {
	if len(args) == 0 {
		return prefix
	}
	if len(args) == 1 {
		return prefix + " " + args[0]
	}
	return prefix + " " + strings.Join(args, ", ")
}

func (m Model) renderToolResultAt(msgIndex int, msg api.Message) string {
	name := msg.Name
	if name == "" {
		name = "tool"
	}
	body := m.formatToolSummary(msg, max(m.width-8, 32))
	return m.messageCardPlain(m.toolDisplayTitle(msg), toolColor, body)
}

func (m Model) toolDisplayTitle(msg api.Message) string {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(msg.ToolArgs), &args)
	path := stringArg(args, "path")
	if path != "" {
		path = m.formatPath(path)
	}
	switch msg.Name {
	case "list_files":
		if path == "" {
			path = "."
		}
		return "Listed files in " + path
	case "read_files":
		path := stringArg(args, "path")
		if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
			strs := make([]string, 0, len(paths))
			for _, p := range paths {
				if s, ok := p.(string); ok {
					strs = append(strs, normalisePath(s))
				}
			}
			if len(strs) == 1 {
				return "Read " + strs[0]
			}
			return fmt.Sprintf("Read %d files", len(strs))
		}
		if path != "" {
			return "Read " + normalisePath(path)
		}
		return "Read"
	case "write_file":
		return "Wrote " + path
	case "edit_file":
		return "Edited " + path
	case "bash":
		cmd := stringArg(args, "command")
		if cmd != "" {
			return "Ran " + oneLine(cmd, 60)
		}
		return "Ran command"
	case "grep":
		return "Searched " + stringArg(args, "pattern")
	case "glob":
		return "Found files matching " + stringArg(args, "pattern")
	case "file_info":
		return "Inspected " + path
	case "calculate":
		return "Calculated " + stringArg(args, "expression")
	case "set_goal":
		goal := stringArg(args, "goal")
		if goal != "" {
			return "Goal: " + oneLine(goal, 80)
		}
		return "Set goal"
	case "yes":
		summary := stringArg(args, "summary")
		if summary != "" {
			return "Finished: " + oneLine(summary, 80)
		}
		return "Finished task"
	case "format_error":
		return "Corrected tool-call format"
	default:
		return msg.Name
	}
}

func (m Model) formatToolExpanded(msg api.Message, width int) string {
	args := strings.TrimSpace(msg.ToolArgs)
	content := strings.TrimSpace(msg.Content)
	if args != "" {
		return dimStyle.Render("args: "+args) + "\n" + compactLines(content, 0, width)
	}
	return compactLines(content, 0, width)
}

func (m Model) formatToolSummary(msg api.Message, width int) string {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(msg.ToolArgs), &args)
	path := stringArg(args, "path")
	if path != "" {
		path = m.formatPath(path)
	}

	summary := ""
	showOutput := true
	switch msg.Name {
	case "bash":
		summary = "$ " + stringArg(args, "command")
		showOutput = strings.TrimSpace(msg.Content) != ""
	case "read_files":
		summary = ""
		showOutput = false
	case "write_file":
		summary = ""
		showOutput = false
	case "edit_file":
		diff := strings.TrimSpace(msg.Content)
		if diff != "" {
			return renderDiff(diff, width)
		}
		showOutput = false
	case "file_info":
		summary = ""
		showOutput = false
	case "list_files":
		if path == "" {
			path = "."
		}
		summary = ""
		showOutput = false
	case "grep":
		summary = ""
		showOutput = false
	case "glob":
		summary = ""
		showOutput = false
	case "calculate":
		summary = ""
		showOutput = false
	case "set_goal", "yes":
		summary = ""
		showOutput = false
	case "edit_soul":
		summary = ""
		showOutput = false
	case "todo_write", "todo_read":
		// Todo tools return a natural checklist; render it directly. We
		// strip the per-line "#xxxx" short id because it's noise for a human
		// reader (looks like a git hash or hex color); the model still has
		// the id available in the raw tool output it received, and the todo
		// verifier feedback uses it explicitly.
		content := strings.TrimRight(msg.Content, "\n")
		if strings.TrimSpace(content) == "" {
			return ""
		}
		return stripTodoIDs(content)
	default:
		summary = compactLines(msg.Content, 4, width)
	}

	if !showOutput {
		return summary
	}
	out := compactLines(msg.Content, 12, width)
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == strings.TrimSpace(summary) {
		return summary
	}
	if summary == "" {
		return out
	}
	return summary + "\n" + dimStyle.Render(out)
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func compactLines(content string, maxLines, maxWidth int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	for i := range lines {
		lines[i] = oneLine(lines[i], maxWidth)
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string, maxWidth int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if maxWidth <= 3 || len(s) <= maxWidth {
		return s
	}
	return s[:maxWidth-3] + "..."
}

// normalisePath strips a leading "./" from a path so that "./foo.go"
// and "foo.go" always display as the same form.
func normalisePath(p string) string {
	for strings.HasPrefix(p, "./") || strings.HasPrefix(p, ".\\") {
		p = strings.TrimPrefix(p, "./")
		p = strings.TrimPrefix(p, ".\\")
	}
	return p
}

// todoIDSuffixRE matches the trailing "  #xxxx" short-id stamp that the
// model-visible checklist uses to address items. The UI drops it; the raw
// tool output keeps it.
var todoIDSuffixRE = regexp.MustCompile(`[ \t]+#[A-Za-z0-9]{2,}\s*$`)

// stripTodoIDs removes the trailing #xxxx short id from each line of a
// checklist. Trailing whitespace gets collapsed, otherwise the lines are
// otherwise left untouched so they still line up in the UI.
func stripTodoIDs(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = todoIDSuffixRE.ReplaceAllString(line, "")
	}
	return strings.Join(lines, "\n")
}

// renderDiff colourises a unified-diff produced by formatDiff. Each input
// line starts with a single marker character (' ', '+' or '-') followed by
// the line-number columns and the line text. The marker drives the colour
// choice; the remaining body is preserved verbatim so the rendered lines
// stay aligned in the TUI.
func renderDiff(content string, width int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	const maxLines = 40
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, dimStyle.Render(fmt.Sprintf("   ... %d more lines ...", len(lines)-maxLines)))
	}
	var styled []string
	for _, line := range lines {
		if width > 0 && len(line) > width {
			line = line[:width-3] + "..."
		}
		if line == "" {
			styled = append(styled, dimStyle.Render(""))
			continue
		}
		marker := line[0]
		switch marker {
		case '+':
			styled = append(styled, addStyle.Render(line))
		case '-':
			styled = append(styled, delStyle.Render(line))
		default:
			styled = append(styled, dimStyle.Render(line))
		}
	}
	return strings.Join(styled, "\n")
}

func (m Model) formatPath(path string) string {
	if strings.HasPrefix(path, m.working) {
		if rel, err := filepath.Rel(m.working, path); err == nil && !strings.HasPrefix(rel, "..") {
			return normalisePath(rel)
		}
	}
	return normalisePath(path)
}

func (m Model) renderStreamingMessage() string {
	var parts []string
	if streaming := strings.TrimSpace(m.streaming); streaming != "" {
		parts = append(parts, m.messageCard("Cardinal", aiColor, streaming))
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderSuggestions() string {
	if len(m.suggestions) == 0 {
		return ""
	}
	items := make([]string, 0, len(m.suggestions))
	for i, s := range m.suggestions {
		if i == m.suggSelected {
			items = append(items, lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("› "+s))
		} else {
			items = append(items, dimStyle.Render("  "+s))
		}
	}
	return lipgloss.NewStyle().MarginLeft(1).Render(strings.Join(items, "\n"))
}

func (m Model) renderModelsMode() string {
	data := m.modeData.(*modelsMode)
	data.filter = strings.TrimSpace(data.filterInput.Value())
	clampModelsMode(data)
	filtered := filterModels(data.models, data.filter)
	var rows []string
	rows = append(rows, titleStyle.Render("Choose model"), dimStyle.Render("Type to filter · Enter select · Esc cancel"), "")
	if len(filtered) == 0 {
		rows = append(rows, dimStyle.Render("No models found"))
	}
	visible := data.visibleLines
	if visible <= 0 {
		visible = 12
	}
	for i := data.scroll; i < len(filtered) && i < data.scroll+visible; i++ {
		name := filtered[i].ID
		prefix := "  "
		style := dimStyle
		if i == data.selected {
			prefix = "› "
			style = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
		}
		rows = append(rows, prefix+style.Render(name))
	}
	rows = append(rows, "", "Filter: "+data.filterInput.View())
	return modePanel(m.width, strings.Join(rows, "\n"))
}

func (m Model) renderProfilesMode() string {
	data := m.modeData.(*profilesMode)
	var rows []string
	rows = append(rows, titleStyle.Render("Profiles"), dimStyle.Render("Enter select · n new · e edit · Esc cancel"), "")
	if len(data.profiles) == 0 {
		rows = append(rows, dimStyle.Render("No profiles"))
	}
	for i, p := range data.profiles {
		prefix := "  "
		style := dimStyle
		if i == data.selected {
			prefix = "› "
			style = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
		}
		rows = append(rows, prefix+style.Render(p.Name)+dimStyle.Render("  "+p.Model+" @ "+compactEndpoint(p.APIURL)))
	}
	return modePanel(m.width, strings.Join(rows, "\n"))
}

func (m Model) renderProfileFormMode() string {
	data := m.modeData.(*profileFormMode)
	var rows []string
	rows = append(rows, titleStyle.Render(data.title), dimStyle.Render(data.help), "")
	for i, label := range data.labels {
		prefix := "  "
		style := dimStyle
		if i == data.selected {
			prefix = "› "
			style = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
		}
		rows = append(rows, prefix+style.Render(label)+": "+data.inputs[i].View())
	}
	rows = append(rows, "", dimStyle.Render("Enter save · Tab next · Esc cancel"))
	return modePanel(m.width, strings.Join(rows, "\n"))
}

func (m Model) renderTextInputMode() string {
	data := m.modeData.(*textInputMode)
	body := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(data.title),
		dimStyle.Render(data.help),
		"",
		data.input.View(),
		"",
		dimStyle.Render("Enter save · Esc cancel"),
	)
	return modePanel(m.width, body)
}

func (m Model) renderPermissionsMode() string {
	data := m.modeData.(*permissionMode)
	var rows []string
	rows = append(rows, titleStyle.Render("Approve tools"), dimStyle.Render("Space toggle · a allow · r reject · Enter continue"), "")
	for i, tc := range data.toolCalls {
		prefix := "  "
		if i == data.selected {
			prefix = "› "
		}
		mark := lipgloss.NewStyle().Foreground(errorColor).Render("✗")
		if data.approvals[i] {
			mark = lipgloss.NewStyle().Foreground(successColor).Render("✓")
		}
		line := fmt.Sprintf("%s %s %s", prefix, mark, tc.Function.Name)
		args := oneLine(tc.Function.Arguments, 70)
		if args != "" {
			line += dimStyle.Render("  " + args)
		}
		if i == data.selected {
			line = lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(line)
		}
		rows = append(rows, line)
	}
	return modePanel(m.width, strings.Join(rows, "\n"))
}

func modePanel(width int, body string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(panelColor).
		Padding(1, 2).
		Width(min(max(width-4, 44), 100)).
		Render(body)
}

func (m Model) renderInput() string {
	width := max(m.width-2, 30)
	return lipgloss.NewStyle().Width(width).Render(m.input.View())
}

func roleMeta(msg api.Message) (string, lipgloss.Color) {
	switch msg.Role {
	case "user":
		return "You", userColor
	case "assistant":
		return "Cardinal", aiColor
	case "tool":
		return "Tool", toolColor
	default:
		return msg.Role, dimColor
	}
}

func compactEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return endpoint
	}
	return u.Host
}

// looksLikeMarkdown is a cheap heuristic for whether a string is worth
// running through glamour. We avoid rendering markdown for ordinary prompts;
// only treat the message as markdown when it contains at least one of the
// classic markers. Glamour is forgiving enough that the cost of a miss is
// small (a few extra ANSI bytes), so we err slightly on the side of true.
func looksLikeMarkdown(s string) bool {
	if strings.Contains(s, "\n```") || strings.Contains(s, "```") {
		return true
	}
	for _, marker := range []string{
		"\n# ", "\n## ", "\n### ",
		"\n- ", "\n* ", "\n1. ",
		"\n> ",
		"**", "__", "`",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	return oneLine(s, maxLen)
}
