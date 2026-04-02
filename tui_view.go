package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"cardinal/pkg/api"
	"cardinal/pkg/tools"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	toolStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
)

func (m Model) View() string {
	if m.mode != "" {
		return m.renderMode()
	}
	return m.renderMainView()
}

func (m Model) renderMainView() string {
	sections := []string{m.renderHeader()}

	// Use viewport for conversation when enabled
	if m.useViewport && m.viewport.Height > 0 {
		sections = append(sections, m.viewport.View())
	} else {
		sections = append(sections, m.renderConversation())
	}

	if len(m.suggestions) > 0 {
		sections = append(sections, m.renderSuggestions())
	}

	// Add padding before input/throbber
	sections = append(sections, "", "")

	// Add throbber above input when busy
	if m.busy {
		sections = append(sections, m.renderThrobber())
	}

	sections = append(sections, m.renderInput(), m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) renderThrobber() string {
	status := m.status
	if status == "" {
		status = "Working..."
	}

	color := getStatusColor(status)
	throbberStyle := lipgloss.NewStyle().Foreground(color)
	statusStyle := lipgloss.NewStyle().Foreground(color)

	return "  " + throbberStyle.Render(m.spinner.View()) + " " + statusStyle.Render(status)
}

func getStatusColor(status string) lipgloss.Color {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "thinking") || strings.Contains(lower, "hmmm") ||
		strings.Contains(lower, "ponder") || strings.Contains(lower, "comput") ||
		strings.Contains(lower, "process") || strings.Contains(lower, "load"):
		return lipgloss.Color("6")
	case strings.Contains(lower, "receiving") || strings.Contains(lower, "writing"):
		return lipgloss.Color("5")
	case strings.Contains(lower, "running") || strings.Contains(lower, "tools") ||
		strings.Contains(lower, "continu"):
		return lipgloss.Color("3")
	case strings.Contains(lower, "error") || strings.Contains(lower, "fail") ||
		strings.Contains(lower, "denied"):
		return lipgloss.Color("1")
	case strings.Contains(lower, "permission") || strings.Contains(lower, "approve"):
		return lipgloss.Color("5")
	default:
		return lipgloss.Color("8")
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
		return m.renderMainView()
	}
}

func (m Model) renderHeader() string {
	status := m.status
	if status == "" {
		status = "Ready"
	}
	if m.autoApprove {
		status += " • auto-approve"
	}

	// Don't show spinner in header anymore - it's now above input
	info := dimStyle.Render(
		m.cfg.ActiveProfileName() + " • " + m.cfg.Model + " • " + compactEndpoint(m.cfg.APIURL),
	)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		titleStyle.Render("Cardinal")+" "+dimStyle.Render(status),
		info,
		"",
	)
}

func (m Model) renderConversation() string {
	hasStreaming := strings.TrimSpace(m.streaming) != ""

	if len(m.messages) == 0 && !hasStreaming && m.err == nil {
		return m.renderWelcome()
	}

	var blocks []string

	if m.scrollOffset > 0 {
		blocks = append(blocks, dimStyle.Render(fmt.Sprintf(" ↑ %d older message%s", m.scrollOffset, pluralize(m.scrollOffset))))
	}

	for _, message := range m.getVisibleMessages() {
		if rendered := m.renderMessage(message); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}

	if hasStreaming {
		blocks = append(blocks, m.renderStreamingMessage())
	}

	if m.err != nil {
		blocks = append(blocks, errorStyle.Render(" Error: "+m.err.Error()))
	}

	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

func (m Model) renderWelcome() string {
	lines := []string{
		"  Start typing to begin a conversation.",
		"",
		dimStyle.Render("  /profiles switch provider"),
		dimStyle.Render("  /models pick a model"),
		dimStyle.Render("  /autoapprove toggle auto-approve tools"),
		dimStyle.Render("  /help all commands"),
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderMessage(msg api.Message) string {
	if msg.Role == "system" {
		return ""
	}

	if msg.Role == "tool" {
		return m.renderToolResult(msg)
	}

	label, color := roleMeta(msg)
	content := strings.TrimSpace(msg.Content)

	if content == "" {
		return ""
	}

	labelLine := lipgloss.NewStyle().Bold(true).Foreground(color).Render("  " + label)
	contentLine := lipgloss.NewStyle().PaddingLeft(4).Width(max(m.width-4, 20)).Render(content)

	return lipgloss.JoinVertical(lipgloss.Left, labelLine, contentLine, "")
}

func (m Model) renderToolResult(msg api.Message) string {
	toolName := msg.Name
	if toolName == "" {
		toolName = "tool"
	}

	maxHeight := 10
	maxWidth := max(m.width-4, 20)

	var content string

	switch toolName {
	case "read_file":
		path := extractPathFromToolResult(msg.Content)
		displayPath := m.formatPath(path)
		linesInfo := extractLinesFromToolResult(msg.Content)
		if linesInfo != "" {
			content = dimStyle.Render("  → read " + displayPath + " (" + linesInfo + ")")
		} else {
			content = dimStyle.Render("  → read " + displayPath)
		}

	case "list_files":
		path := extractPathFromToolResult(msg.Content)
		displayPath := m.formatPath(path)
		content = dimStyle.Render("  → list " + displayPath)

	case "bash":
		content = m.formatBashOutput(msg.Content, maxHeight, maxWidth)

	case "write_file":
		content = m.formatWriteFileOutput(msg.Content, maxHeight, maxWidth)

	case "edit_file":
		content = m.formatEditFileOutput(msg.Content, maxHeight, maxWidth)

	case "grep", "glob":
		content = m.formatGrepOutput(msg.Content, maxHeight, maxWidth)

	case "file_info":
		content = m.formatFileInfoOutput(msg.Content, maxHeight, maxWidth)

	default:
		content = m.formatGenericToolOutput(msg.Content, maxHeight, maxWidth)
	}

	if content == "" {
		return ""
	}

	return lipgloss.NewStyle().PaddingLeft(4).Render(content)
}

func extractPathFromToolResult(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}
	firstLine := lines[0]
	pathStart := strings.Index(firstLine, " path=\"")
	if pathStart == -1 {
		return ""
	}
	pathStart += 7
	pathEnd := strings.Index(firstLine[pathStart:], "\"")
	if pathEnd == -1 {
		return ""
	}
	return firstLine[pathStart : pathStart+pathEnd]
}

func extractLinesFromToolResult(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}
	firstLine := lines[0]
	linesStart := strings.Index(firstLine, " lines=\"")
	if linesStart == -1 {
		return ""
	}
	linesStart += 7
	linesEnd := strings.Index(firstLine[linesStart:], "\"")
	if linesEnd == -1 {
		return ""
	}
	return firstLine[linesStart : linesStart+linesEnd]
}

func extractErrorFromToolResult(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Error:") {
			return strings.TrimPrefix(line, "Error:")
		}
	}
	return ""
}

func (m Model) formatPath(path string) string {
	if path == "" {
		return "file"
	}
	if !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(m.working, path)
	if err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func (m Model) formatBashOutput(output string, maxHeight, maxWidth int) string {
	success, outputText := m.extractToolResult(output)
	command := extractPathFromToolResult(output)

	displayCmd := "bash"
	if command != "" {
		if len(command) > 50 {
			displayCmd = command[:50] + "..."
		} else {
			displayCmd = command
		}
	}

	if outputText == "" {
		return dimStyle.Render("  → " + displayCmd + " (no output)")
	}

	outputLines := m.truncateLines(outputText, maxHeight, maxWidth)
	icon := "→"
	if success {
		return successStyle.Render("  "+icon+" "+displayCmd+":") + "\n" + dimStyle.Render(strings.Join(outputLines, "\n"))
	}
	return toolStyle.Render("  "+icon+" "+displayCmd+":") + "\n" + dimStyle.Render(strings.Join(outputLines, "\n"))
}

func (m Model) extractToolResult(output string) (bool, string) {
	success := strings.Contains(output, "success=\"true\"")
	lines := strings.Split(output, "\n")
	var outputLines []string
	inOutput := false
	for _, line := range lines {
		if strings.Contains(line, "<tool_result") {
			inOutput = true
			continue
		}
		if strings.Contains(line, "</tool_result>") {
			break
		}
		if inOutput && !strings.HasPrefix(line, "Error:") {
			outputLines = append(outputLines, line)
		}
	}
	return success, strings.TrimSpace(strings.Join(outputLines, "\n"))
}

func (m Model) truncateLines(text string, maxHeight, maxWidth int) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
		lines = append(lines, dimStyle.Render(fmt.Sprintf("... (%d more lines)", len(strings.Split(text, "\n"))-maxHeight)))
	}
	truncated := make([]string, len(lines))
	for i, line := range lines {
		if len(line) > maxWidth {
			truncated[i] = line[:maxWidth-3] + "..."
		} else {
			truncated[i] = line
		}
	}
	return truncated
}

func (m Model) formatWriteFileOutput(output string, maxHeight, maxWidth int) string {
	_ = maxHeight
	_ = maxWidth

	success := strings.Contains(output, "success=\"true\"")
	path := extractPathFromToolResult(output)
	displayPath := m.formatPath(path)

	icon := "✓"
	style := successStyle
	if !success {
		icon = "✗"
		style = toolStyle
	}

	if success {
		return style.Render("  " + icon + " wrote " + displayPath)
	}
	return style.Render("  " + icon + " write failed: " + displayPath)
}

func (m Model) formatEditFileOutput(output string, maxHeight, maxWidth int) string {
	success := strings.Contains(output, "success=\"true\"")
	path := extractPathFromToolResult(output)
	displayPath := m.formatPath(path)

	var diffLines []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			diffLines = append(diffLines, line)
		}
	}

	if len(diffLines) > maxHeight {
		diffLines = diffLines[:maxHeight]
	}

	icon := "✓"
	style := successStyle
	if !success {
		icon = "✗"
		style = toolStyle
	}

	if success {
		var body []string
		body = append(body, style.Render("  "+icon+" edited "+displayPath))
		for _, line := range diffLines {
			if strings.HasPrefix(line, "-") {
				body = append(body, dimStyle.Render("  "+line))
			} else if strings.HasPrefix(line, "+") {
				body = append(body, successStyle.Render("  "+line))
			}
		}
		return strings.Join(body, "\n")
	}

	errorMsg := extractErrorFromToolResult(output)
	if errorMsg != "" {
		return style.Render("  " + icon + " edit failed: " + errorMsg)
	}
	return style.Render("  " + icon + " edit failed")
}

func (m Model) formatGrepOutput(output string, maxHeight, maxWidth int) string {
	success, outputText := m.extractToolResult(output)
	searchInfo := extractPathFromToolResult(output)

	display := "grep"
	if searchInfo != "" {
		if len(searchInfo) > 40 {
			display = searchInfo[:40] + "..."
		} else {
			display = searchInfo
		}
	}

	if outputText == "" || strings.Contains(outputText, "No matches found") {
		return dimStyle.Render("  → " + display + ": no matches")
	}

	outputLines := m.truncateLines(outputText, maxHeight, maxWidth)

	if success {
		return successStyle.Render("  → "+display+":") + "\n" + dimStyle.Render(strings.Join(outputLines, "\n"))
	}
	return toolStyle.Render("  → "+display+":") + "\n" + dimStyle.Render(strings.Join(outputLines, "\n"))
}

func (m Model) formatFileInfoOutput(output string, maxHeight, maxWidth int) string {
	success, outputText := m.extractToolResult(output)
	if outputText == "" {
		return dimStyle.Render("  → file_info: no output")
	}

	lines := strings.Split(outputText, "\n")
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}

	if success {
		var body []string
		body = append(body, successStyle.Render("  ✓ file info:"))
		for _, line := range lines {
			body = append(body, dimStyle.Render("  "+line))
		}
		return strings.Join(body, "\n")
	}
	return toolStyle.Render("  ✗ file_info failed")
}

func (m Model) formatGenericToolOutput(output string, maxHeight, maxWidth int) string {
	success, outputText := m.extractToolResult(output)
	if outputText == "" {
		return dimStyle.Render("  → (no output)")
	}

	outputLines := m.truncateLines(outputText, maxHeight, maxWidth)
	icon := "→"
	if success {
		return dimStyle.Render("  "+icon+" ") + strings.Join(outputLines, "\n")
	}
	return toolStyle.Render("  "+icon+" ") + dimStyle.Render(strings.Join(outputLines, "\n"))
}

func (m Model) renderStreamingMessage() string {
	var blocks []string

	thinking := strings.TrimSpace(m.thinking)
	content := strings.TrimSpace(m.streaming)

	labelLine := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")).Render("  Cardinal")
	blocks = append(blocks, labelLine)

	if thinking != "" {
		thinkingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).PaddingLeft(4).Width(max(m.width-4, 20))
		blocks = append(blocks, thinkingStyle.Render("Thinking: "+thinking))
	}

	if content != "" {
		contentLine := lipgloss.NewStyle().PaddingLeft(4).Width(max(m.width-4, 20)).Render(content)
		blocks = append(blocks, contentLine)
	}

	if len(blocks) == 1 {
		return ""
	}

	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

func (m Model) renderSuggestions() string {
	var lines []string
	for i, suggestion := range m.suggestions {
		style := dimStyle
		if i == m.suggSelected {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
		}
		lines = append(lines, style.Render("  "+suggestion))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderInput() string {
	return "  > " + m.input.View()
}

func (m Model) renderFooter() string {
	scrollHint := ""
	if m.useViewport {
		scrollHint = " • ↑/↓ scroll"
	}
	hint := fmt.Sprintf("  Ctrl+C quit%s • Tab complete • /help", scrollHint)
	return "\n" + dimStyle.Render(hint)
}

func (m Model) renderPanel(title, body, hint string) string {
	var sections []string

	sections = append(sections, m.renderHeader())
	sections = append(sections, titleStyle.Render("  "+title), "")

	if body != "" {
		sections = append(sections, body)
	}

	if m.err != nil {
		sections = append(sections, "", errorStyle.Render("  Error: "+m.err.Error()))
	}

	if hint != "" {
		sections = append(sections, "", dimStyle.Render("  "+hint))
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) renderModelsMode() string {
	data := m.modeData.(*modelsMode)
	filtered := filterModelsView(data.models, data.filter)

	visibleLines := data.visibleLines
	if visibleLines <= 0 {
		visibleLines = 15
	}

	if data.scroll > len(filtered)-visibleLines {
		data.scroll = max(0, len(filtered)-visibleLines)
	}
	if data.selected >= len(filtered) {
		data.selected = max(0, len(filtered)-1)
	}

	var lines []string
	lines = append(lines, dimStyle.Render("  Filter: "+data.filterInput.View()))

	start := data.scroll
	end := start + visibleLines
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		model := filtered[i]
		idx := i - start
		if idx+data.scroll == data.selected {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render("  > "+model.ID))
		} else {
			lines = append(lines, dimStyle.Render("    "+model.ID))
		}
	}

	if len(filtered) == 0 {
		lines = append(lines, dimStyle.Render("  No matching models."))
	}

	showing := len(filtered)
	if data.filter != "" {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  Showing %d-%d of %d models", start+1, end, showing)))
	} else if showing > visibleLines {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d-%d of %d models", start+1, end, showing)))
	}

	return m.renderPanel("Choose Model", strings.Join(lines, "\n"), "Tab filter • ↑/↓/Ctrl+U/D scroll • Enter select • Esc cancel")
}

func filterModelsView(models []api.Model, filter string) []api.Model {
	if filter == "" {
		return models
	}
	filter = strings.ToLower(filter)
	var filtered []api.Model
	for _, model := range models {
		if strings.Contains(strings.ToLower(model.ID), filter) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func (m Model) renderProfilesMode() string {
	data := m.modeData.(*profilesMode)

	var blocks []string
	for i, profile := range data.profiles {
		active := ""
		if profile.Name == m.cfg.ActiveProfileName() {
			active = " (active)"
		}
		if i == data.selected {
			blocks = append(blocks, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true).Render("  > "+profile.Name+active))
		} else {
			blocks = append(blocks, dimStyle.Render("    "+profile.Name+active))
		}
		blocks = append(blocks, dimStyle.Render("    "+profile.Model+" • "+compactEndpoint(profile.APIURL)))
	}

	if len(blocks) == 0 {
		blocks = append(blocks, dimStyle.Render("  No profiles saved. Press n to create one."))
	}

	return m.renderPanel("Profiles", strings.Join(blocks, "\n"), "Enter switch • e edit • n new • Esc cancel")
}

func (m Model) renderProfileFormMode() string {
	data := m.modeData.(*profileFormMode)

	var lines []string
	for i, label := range data.labels {
		if i == data.selected {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true).Render("  "+label))
		} else {
			lines = append(lines, dimStyle.Render("  "+label))
		}
		lines = append(lines, "  "+data.inputs[i].View(), "")
	}

	return m.renderPanel(data.title, strings.Join(lines, "\n"), "Tab move • Enter save • Esc cancel")
}

func (m Model) renderTextInputMode() string {
	data := m.modeData.(*textInputMode)

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		dimStyle.Render("  "+data.help),
		"",
		"  "+data.input.View(),
	)

	return m.renderPanel(data.title, body, "Enter save • Esc cancel")
}

func (m Model) renderPermissionsMode() string {
	data := m.modeData.(*permissionMode)

	var lines []string

	if strings.TrimSpace(data.assistantContent) != "" {
		lines = append(lines, dimStyle.Render("  "+truncateText(data.assistantContent, max(m.width-6, 40))), "")
	}

	for i, toolCall := range data.toolCalls {
		check := "○"
		if data.approvals[i] {
			check = "✓"
		}
		summary := tools.SummarizeCall(toolCall.Function.Name, toolCall.Function.Arguments)
		if i == data.selected {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render("  "+check+" "+summary))
		} else {
			lines = append(lines, dimStyle.Render("  "+check+" "+summary))
		}
	}

	return m.renderPanel("Approve Tools", strings.Join(lines, "\n"), "Space toggle • a allow • r deny • A/R all • Enter run • Esc deny all")
}

func (m Model) getVisibleMessages() []api.Message {
	windowSize := m.messageWindowSize()

	if len(m.messages) <= windowSize {
		return m.messages
	}

	offset := m.scrollOffset
	if offset > m.maxScrollOffset() {
		offset = m.maxScrollOffset()
	}
	if offset < 0 {
		offset = 0
	}

	start := len(m.messages) - windowSize - offset
	if start < 0 {
		start = 0
	}
	end := start + windowSize
	if end > len(m.messages) {
		end = len(m.messages)
	}

	return m.messages[start:end]
}

func (m Model) messageWindowSize() int {
	if m.height <= 0 {
		return 10
	}
	reserved := 8
	if len(m.suggestions) > 0 {
		reserved += len(m.suggestions)
	}
	if m.busy {
		reserved += 1
	}
	size := m.height - reserved
	if size < 3 {
		return 3
	}
	return size
}

func (m Model) maxScrollOffset() int {
	offset := len(m.messages) - m.messageWindowSize()
	if offset < 0 {
		return 0
	}
	return offset
}

func roleMeta(msg api.Message) (string, lipgloss.Color) {
	switch msg.Role {
	case "user":
		return "You", lipgloss.Color("4")
	case "tool":
		label := "Tool"
		if msg.Name != "" {
			label = "Tool: " + msg.Name
		}
		return label, lipgloss.Color("5")
	default:
		return "Cardinal", lipgloss.Color("2")
	}
}

func compactEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "not set"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return truncateText(raw, 40)
	}
	value := parsed.Host
	if parsed.Path != "" && parsed.Path != "/" {
		value += parsed.Path
	}
	return truncateText(value, 40)
}

func truncateText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
