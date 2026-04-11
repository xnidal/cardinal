package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"cardinal/pkg/api"

	"github.com/charmbracelet/lipgloss"
)

var (
	accentColor  = lipgloss.Color("6")
	primaryColor = lipgloss.Color("5")
	successColor = lipgloss.Color("2")
	warningColor = lipgloss.Color("3")
	errorColor   = lipgloss.Color("1")
	dimColor     = lipgloss.Color("8")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			Italic(true)

	dimStyle = lipgloss.NewStyle().Foreground(dimColor)

	errorStyle = lipgloss.NewStyle().
			Foreground(errorColor).
			Background(lipgloss.Color("52")).
			Bold(true).
			Padding(0, 1)

	toolStyle = lipgloss.NewStyle().
			Foreground(warningColor).
			Background(lipgloss.Color("235"))

	successStyle = lipgloss.NewStyle().
			Foreground(successColor).
			Bold(true)

	userMessageStyle = lipgloss.NewStyle().
				Foreground(successColor).
				PaddingLeft(2)

	assistantMessageStyle = lipgloss.NewStyle().
				Foreground(primaryColor).
				PaddingLeft(2)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(dimColor).
			Padding(0, 1)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).
			Padding(0, 1)

	headerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(accentColor).
			BorderBottom(true).
			MarginBottom(1)
)

func (m Model) View() string {
	if m.mode != "" {
		return m.renderMode()
	}
	return m.renderMainView()
}

func (m Model) renderMainView() string {
	sections := []string{m.renderHeader()}

	if m.useViewport && m.viewport.Height > 0 {
		sections = append(sections, m.viewport.View())
	} else {
		sections = append(sections, m.renderConversation())
	}

	if len(m.suggestions) > 0 {
		sections = append(sections, m.renderSuggestions())
	}

	sections = append(sections, "")

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
	throbberStyle := lipgloss.NewStyle().Foreground(color).Bold(true)

	spinner := throbberStyle.Render(m.spinner.View())
	statusText := lipgloss.NewStyle().Foreground(color).Render(status)

	return spinner + " " + statusText
}

func getStatusColor(status string) lipgloss.Color {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "thinking") || strings.Contains(lower, "hmmm") || strings.Contains(lower, "ponder") || strings.Contains(lower, "comput") || strings.Contains(lower, "process") || strings.Contains(lower, "load"):
		return lipgloss.Color("81")
	case strings.Contains(lower, "receiving") || strings.Contains(lower, "writing"):
		return lipgloss.Color("177")
	case strings.Contains(lower, "running") || strings.Contains(lower, "continu"):
		return lipgloss.Color("215")
	case strings.Contains(lower, "error") || strings.Contains(lower, "fail") || strings.Contains(lower, "denied"):
		return lipgloss.Color("196")
	case strings.Contains(lower, "permission") || strings.Contains(lower, "approve"):
		return lipgloss.Color("213")
	case strings.Contains(lower, "ready") || strings.Contains(lower, "updated"):
		return lipgloss.Color("78")
	default:
		return lipgloss.Color("248")
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
	info := subtitleStyle.Render(
		" " + m.cfg.ActiveProfileName() + " > " + m.cfg.Model + " @ " + compactEndpoint(m.cfg.APIURL),
	)

	title := lipgloss.NewStyle().
		Foreground(accentColor).
		Bold(true).
		Render("Cardinal")

	return lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().MarginBottom(1).Render(title),
		info,
		"",
	)
}

func (m Model) renderConversation() string {
	hasStreaming := strings.TrimSpace(m.streaming) != ""
	hasThinking := strings.TrimSpace(m.thinking) != ""

	if len(m.messages) == 0 && !hasStreaming && !hasThinking && m.err == nil {
		return m.renderWelcome()
	}

	return m.renderChatHistory(m.messages, m.scrollOffset, hasStreaming, hasThinking, m.err)
}

func (m Model) renderChatHistory(messages []api.Message, scrollOffset int, hasStreaming, hasThinking bool, chatErr error) string {
	if len(messages) == 0 && !hasStreaming && !hasThinking && chatErr == nil {
		return m.renderWelcome()
	}

	var blocks []string

	if scrollOffset > 0 {
		blocks = append(blocks,
			lipgloss.NewStyle().
				Foreground(dimColor).
				Italic(true).
				Render("↑ "+fmt.Sprintf("%d older message%s", scrollOffset, pluralize(scrollOffset))),
		)
	}

	visible := messages
	if scrollOffset > 0 && scrollOffset < len(messages) {
		visible = messages[scrollOffset:]
	}
	for i, message := range visible {
		if rendered := m.renderMessage(message); rendered != "" {
			blocks = append(blocks, rendered)
		}
		if i < len(visible)-1 {
			blocks = append(blocks, "")
		}
	}

	if hasThinking || hasStreaming {
		blocks = append(blocks, m.renderStreamingMessage())
	}

	if chatErr != nil {
		errorBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(errorColor).
			Padding(0, 1).
			Render("⚠ Error: " + chatErr.Error())
		blocks = append(blocks, errorBox)
	}

	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

func (m Model) renderWelcome() string {
	commands := []struct {
		cmd  string
		desc string
	}{
		{"/profiles", "switch provider"},
		{"/models", "pick a model"},
		{"/autoapprove", "toggle auto-approve"},
		{"/help", "all commands"},
	}

	var lines []string
	lines = append(lines,
		lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true).
			Render("Start a conversation"),
		"",
	)

	for _, c := range commands {
		lines = append(lines,
			lipgloss.NewStyle().
				Foreground(primaryColor).
				Render("  "+c.cmd)+
				lipgloss.NewStyle().
					Foreground(dimColor).
					Render(" -> "+c.desc),
		)
	}

	return lipgloss.NewStyle().
		Padding(1, 0).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
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

	if content == "" && len(msg.ToolCalls) == 0 {
		return ""
	}

	var icon string = ">"

	labelLine := lipgloss.NewStyle().
		Bold(true).
		Foreground(color).
		Render(icon + " " + label)

	contentBox := lipgloss.NewStyle().
		PaddingLeft(3).
		Width(max(m.width-4, 20)).
		Render(content)

	var blocks []string
	blocks = append(blocks, labelLine)

	if content != "" {
		blocks = append(blocks, contentBox)
	}

	return lipgloss.NewStyle().
		MarginBottom(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, blocks...))
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
			content = lipgloss.NewStyle().
				Foreground(accentColor).
				Render("> read " + displayPath + " [" + linesInfo + "]")
		} else {
			content = lipgloss.NewStyle().
				Foreground(accentColor).
				Render("> read " + displayPath)
		}

	case "list_files":
		path := extractPathFromToolResult(msg.Content)
		displayPath := m.formatPath(path)
		if displayPath == "" {
			displayPath = "."
		}

		lines := strings.Split(msg.Content, "\n")
		if len(lines) > maxHeight {
			lines = append(lines[:maxHeight],
				lipgloss.NewStyle().
					Foreground(warningColor).
					Italic(true).
					Render(fmt.Sprintf("  ... %d more results", len(lines)-maxHeight)),
			)
		}

		var formattedLines []string
		for _, line := range lines {
			if len(line) > maxWidth {
				line = line[:maxWidth-3] + "..."
			}
			if strings.TrimSpace(line) != "" {
				formattedLines = append(formattedLines, line)
			}
		}

		header := lipgloss.NewStyle().
			Foreground(accentColor).
			Render("> list_files " + displayPath)

		outputBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(dimColor).
			Padding(0, 1).
			Width(maxWidth).
			Render(strings.Join(formattedLines, "\n"))

		content = header + "\n" + outputBox

	case "bash":
		content = m.formatBashOutput(msg.Content, maxHeight, maxWidth)

	case "write_file":
		content = m.formatWriteFileOutput(msg.Content, maxHeight, maxWidth)

	case "edit_file":
		content = m.formatEditFileOutput(msg.Content, maxHeight, maxWidth)

	case "grep":
		content = m.formatGrepOutput(msg.Content, maxHeight, maxWidth)

	case "glob":
		content = m.formatGlobOutput(msg.Content, maxHeight, maxWidth)

	case "file_info":
		content = m.formatFileInfoOutput(msg.Content, maxHeight, maxWidth)

	case "edit_soul":
		content = lipgloss.NewStyle().
			Foreground(accentColor).
			Render("> edit_soul")

	case "calculate":
		content = m.formatCalculateOutput(msg.Content, maxHeight, maxWidth)

	case "subagent", "subagent_status", "subagent_list", "subagent_clear":
		content = m.formatSubagentOutput(msg.Content, maxHeight, maxWidth)

	default:
		content = m.formatDefaultToolOutput(msg.Content, maxHeight, maxWidth)
	}

	return lipgloss.NewStyle().
		PaddingLeft(2).
		Render(content)
}

func (m Model) formatBashOutput(content string, maxHeight, maxWidth int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > maxHeight {
		lines = append(lines[:maxHeight],
			lipgloss.NewStyle().
				Foreground(warningColor).
				Italic(true).
				Render(fmt.Sprintf("  ... %d more lines", len(lines)-maxHeight)),
		)
	}

	var formattedLines []string
	for _, line := range lines {
		if len(line) > maxWidth {
			line = line[:maxWidth-3] + "..."
		}
		if strings.TrimSpace(line) != "" {
			formattedLines = append(formattedLines, line)
		}
	}

	header := lipgloss.NewStyle().
		Foreground(accentColor).
		Render("> bash")

	outputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dimColor).
		Padding(0, 1).
		Width(maxWidth).
		Render(strings.Join(formattedLines, "\n"))

	return header + "\n" + outputBox
}

func (m Model) formatWriteFileOutput(content string, maxHeight, maxWidth int) string {
	path := extractPathFromToolResult(content)
	displayPath := m.formatPath(path)

	return lipgloss.NewStyle().
		Foreground(accentColor).
		Render("> write " + displayPath)
}

func (m Model) formatCalculateOutput(content string, maxHeight, maxWidth int) string {
	lines := strings.Split(content, "\n")
	var outputLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "[") {
			outputLines = append(outputLines, line)
		}
	}
	if len(outputLines) > maxHeight {
		outputLines = append(outputLines[:maxHeight], fmt.Sprintf("... %d more", len(outputLines)-maxHeight))
	}
	if len(outputLines) > 0 {
		return lipgloss.NewStyle().
			Foreground(accentColor).
			Render("> calculate: " + strings.Join(outputLines, " "))
	}
	return lipgloss.NewStyle().Foreground(accentColor).Render("> calculate")
}

func (m Model) formatEditFileOutput(content string, maxHeight, maxWidth int) string {
	path := extractPathFromToolResult(content)
	displayPath := m.formatPath(path)

	lines := strings.Split(content, "\n")
	var diffLines []string
	for _, line := range lines {
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+") {
			diffLines = append(diffLines, line)
		}
	}

	if len(diffLines) > maxHeight {
		diffLines = append(diffLines[:maxHeight], fmt.Sprintf("... %d more changes", len(lines)-maxHeight))
	}

	if len(diffLines) > 0 {
		var coloredDiff []string
		for _, line := range diffLines {
			if strings.HasPrefix(line, "-") {
				coloredDiff = append(coloredDiff,
					lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("    "+line),
				)
			} else {
				coloredDiff = append(coloredDiff,
					lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("    "+line),
				)
			}
		}
		return lipgloss.NewStyle().
			Foreground(accentColor).
			Render("> edit "+displayPath+"\n") +
			strings.Join(coloredDiff, "\n")
	}
	return lipgloss.NewStyle().
		Foreground(accentColor).
		Render("> edit " + displayPath)
}

func (m Model) formatGrepOutput(content string, maxHeight, maxWidth int) string {
	lines := strings.Split(content, "\n")

	// Extract pattern from first line if it's a summary
	var pattern string
	if len(lines) > 0 && !strings.HasPrefix(lines[0], "===") && !strings.HasPrefix(lines[0], "---") {
		pattern = strings.TrimSpace(lines[0])
		lines = lines[1:]
	}

	// Count actual matches
	matchCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "> ") {
			matchCount++
		}
	}

	// Build header with pattern and count
	headerText := "* Grep"
	if pattern != "" {
		headerText = "* Grep \"" + pattern + "\""
	}
	if matchCount > 0 {
		headerText += fmt.Sprintf(" (%d match%s)", matchCount, pluralize(matchCount))
	}

	return lipgloss.NewStyle().
		Foreground(accentColor).
		Render(headerText)
}

func (m Model) formatGlobOutput(content string, maxHeight, maxWidth int) string {
	path := extractPathFromToolResult(content)
	displayPath := m.formatPath(path)
	if displayPath == "" {
		displayPath = "."
	}

	lines := strings.Split(content, "\n")
	var files []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			files = append(files, line)
		}
	}

	if len(files) > maxHeight {
		files = append(files[:maxHeight],
			lipgloss.NewStyle().
				Foreground(warningColor).
				Italic(true).
				Render(fmt.Sprintf(" ... %d more results", len(files)-maxHeight)),
		)
	}

	var formattedLines []string
	for _, line := range files {
		if len(line) > maxWidth-4 {
			line = line[:maxWidth-7] + "..."
		}
		formattedLines = append(formattedLines,
			lipgloss.NewStyle().
				Foreground(dimColor).
				Render("  "+line),
		)
	}

	header := lipgloss.NewStyle().
		Foreground(accentColor).
		Render(fmt.Sprintf("* Glob \"%s\" (%d result%s)", displayPath, len(files), pluralize(len(files))))

	if len(formattedLines) == 0 {
		return header
	}

	outputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dimColor).
		Padding(0, 1).
		Width(maxWidth).
		Render(strings.Join(formattedLines, "\n"))

	return header + "\n" + outputBox
}

func (m Model) formatFileInfoOutput(content string, maxHeight, maxWidth int) string {
	return lipgloss.NewStyle().Foreground(accentColor).Render("> file_info")
}

func (m Model) formatDefaultToolOutput(content string, maxHeight, maxWidth int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > maxHeight {
		lines = append(lines[:maxHeight],
			lipgloss.NewStyle().
				Foreground(warningColor).
				Italic(true).
				Render(fmt.Sprintf("  ... %d more lines", len(lines)-maxHeight)),
		)
	}

	var formattedLines []string
	for _, line := range lines {
		if len(line) > maxWidth {
			line = line[:maxWidth-3] + "..."
		}
		if strings.TrimSpace(line) != "" {
			formattedLines = append(formattedLines,
				lipgloss.NewStyle().Foreground(dimColor).Render("  "+line),
			)
		}
	}

	return strings.Join(formattedLines, "\n")
}

func (m Model) formatSubagentOutput(content string, maxHeight, maxWidth int) string {
	re := regexp.MustCompile(`<subagent_task id="([^"]*)" profile="([^"]*)" status="([^"]*)">`)
	matches := re.FindStringSubmatch(content)

	var taskID, profile, status string
	if len(matches) >= 4 {
		taskID = matches[1]
		profile = matches[2]
		status = matches[3]
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(accentColor).
		Bold(true)

	statusColor := successColor
	if status == "running" {
		statusColor = warningColor
	} else if status == "failed" {
		statusColor = errorColor
	}

	header := headerStyle.Render("> subagent") + " " +
		lipgloss.NewStyle().Foreground(dimColor).Render(taskID) + " " +
		lipgloss.NewStyle().Foreground(statusColor).Render("["+status+"]") + " " +
		lipgloss.NewStyle().Foreground(accentColor).Render(profile)

	var bodyLines []string
	rePrompt := regexp.MustCompile(`<prompt>([^<]*)</prompt>`)
	reThinking := regexp.MustCompile(`<thinking>([^<]*)</thinking>`)
	reMessage := regexp.MustCompile(`<message role="([^"]*)">([^<]*)</message>`)
	reToolCall := regexp.MustCompile(`<tool_call name="([^"]*)">([^<]*)</tool_call>`)
	reToolResult := regexp.MustCompile(`<tool_result name="([^"]*)">([^<]*)</tool_result>`)
	reResult := regexp.MustCompile(`<result>([^<]*)</result>`)
	reError := regexp.MustCompile(`<error>([^<]*)</error>`)

	if promptMatch := rePrompt.FindStringSubmatch(content); len(promptMatch) >= 2 {
		bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("  ▼ Prompt"))
		truncated := promptMatch[1]
		if len(truncated) > maxWidth-4 {
			truncated = truncated[:maxWidth-7] + "..."
		}
		bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(dimColor).Render("    "+truncated))
	}

	for _, match := range reThinking.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			thinking := match[1]
			if len(thinking) > maxWidth-4 {
				thinking = thinking[:maxWidth-7] + "..."
			}
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render("  ◍ thinking"))
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Italic(true).Render("    "+thinking))
		}
	}

	for _, match := range reMessage.FindAllStringSubmatch(content, -1) {
		if len(match) >= 3 {
			role := match[1]
			msgContent := match[2]
			if len(msgContent) > maxWidth-4 {
				msgContent = msgContent[:maxWidth-7] + "..."
			}
			roleColor := successColor
			if role == "user" {
				roleColor = accentColor
			}
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(roleColor).Render("  > "+role))
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(dimColor).Render("    "+msgContent))
		}
	}

	for _, match := range reToolCall.FindAllStringSubmatch(content, -1) {
		if len(match) >= 3 {
			toolName := match[1]
			toolArgs := match[2]
			if len(toolArgs) > maxWidth-4 {
				toolArgs = toolArgs[:maxWidth-7] + "..."
			}
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(warningColor).Render("  ◉ tool: "+toolName))
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(dimColor).Render("    "+toolArgs))
		}
	}

	for _, match := range reToolResult.FindAllStringSubmatch(content, -1) {
		if len(match) >= 3 {
			toolName := match[1]
			toolResult := match[2]
			if len(toolResult) > maxWidth-4 {
				toolResult = toolResult[:maxWidth-7] + "..."
			}
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(successColor).Render("  ◉ result: "+toolName))
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(dimColor).Render("    "+toolResult))
		}
	}

	if resultMatch := reResult.FindStringSubmatch(content); len(resultMatch) >= 2 {
		result := resultMatch[1]
		resultLines := strings.Split(result, "\n")
		for i, line := range resultLines {
			if len(line) > maxWidth-4 {
				line = line[:maxWidth-7] + "..."
			}
			if i == 0 {
				bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(successColor).Bold(true).Render("  ✓ Result"))
			}
			bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(dimColor).Render("    "+line))
		}
	}

	if errorMatch := reError.FindStringSubmatch(content); len(errorMatch) >= 2 {
		bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(errorColor).Bold(true).Render("  ✕ Error"))
		bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(errorColor).Render("    "+errorMatch[1]))
	}

	if len(bodyLines) > maxHeight {
		keepCount := maxHeight - 1
		truncLine := lipgloss.NewStyle().
			Foreground(warningColor).
			Italic(true).
			Render(fmt.Sprintf("  ... %d more lines", len(bodyLines)-keepCount))
		bodyLines = append([]string{truncLine}, bodyLines[len(bodyLines)-keepCount:]...)
	}

	var formattedBody []string
	for _, line := range bodyLines {
		formattedBody = append(formattedBody, line)
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dimColor).
		Padding(0, 1).
		Width(maxWidth)

	return header + "\n" + boxStyle.Render(strings.Join(formattedBody, "\n"))
}

func extractPathFromToolResult(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) >= 2 {
		pathLine := strings.TrimSpace(lines[1])
		if pathLine != "" && !strings.HasPrefix(pathLine, "[") && !strings.HasPrefix(pathLine, "Error:") {
			return pathLine
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "[") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) >= 2 {
				rest := parts[1]
				if !strings.HasPrefix(rest, "Error:") {
					rest = strings.TrimSuffix(rest, "\n")
					if strings.Contains(rest, "\n") {
						rest = strings.SplitN(rest, "\n", 2)[0]
					}
					return rest
				}
			}
		}
		if strings.Contains(line, "path=") {
			idx := strings.Index(line, "path=")
			rest := line[idx+5:]
			if idx := strings.Index(rest, "\""); idx > 0 {
				rest = rest[:idx]
			}
			return rest
		}
	}
	return ""
}

func extractLinesFromToolResult(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, "(") && strings.Contains(line, ")") {
			start := strings.Index(line, "(")
			end := strings.Index(line, ")")
			if start > 0 && end > start {
				return line[start+1 : end]
			}
		}
		if strings.Contains(line, "lines=") {
			idx := strings.Index(line, "lines=")
			rest := line[idx+6:]
			if idx := strings.Index(rest, "\""); idx > 0 {
				rest = rest[:idx]
			}
			return rest
		}
	}
	return ""
}

func (m Model) formatPath(path string) string {
	// Make path relative to working directory if possible
	if strings.HasPrefix(path, m.working) {
		rel, err := filepath.Rel(m.working, path)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return "./" + rel
		}
	}
	return path
}

func (m Model) renderStreamingMessage() string {
	thinking := strings.TrimSpace(m.thinking)
	streaming := strings.TrimSpace(m.streaming)

	// If we have thinking content, show it
	if thinking != "" {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("> Cardinal [thinking]") +
			"\n" +
			lipgloss.NewStyle().PaddingLeft(4).Width(max(m.width-4, 20)).Render(thinking)
	}

	if streaming != "" {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5")).Render("> Cardinal [streaming]") +
			"\n" +
			lipgloss.NewStyle().PaddingLeft(4).Width(max(m.width-4, 20)).Render(streaming)
	}

	return ""
}

func (m Model) renderSuggestions() string {
	if len(m.suggestions) == 0 {
		return ""
	}

	var items []string
	for i, s := range m.suggestions {
		if i == m.suggSelected {
			items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render("→ "+s))
		} else {
			items = append(items, dimStyle.Render("  "+s))
		}
	}

	return "\n" + strings.Join(items, "\n")
}

func (m Model) renderModelsMode() string {
	data := m.modeData.(*modelsMode)

	var lines []string
	lines = append(lines, titleStyle.Render("Select Model"))
	lines = append(lines, "")

	// Apply filter to models
	filtered := filterModels(data.models, data.filter)

	for i, model := range filtered {
		if i < data.scroll {
			continue
		}
		if i >= data.scroll+data.visibleLines {
			break
		}

		prefix := " "
		if i == data.selected {
			prefix = "→ "
		}

		modelName := model.ID
		if i == data.selected {
			modelName = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(modelName)
		} else {
			modelName = dimStyle.Render(modelName)
		}

		lines = append(lines, prefix+modelName)
	}

	if data.scroll > 0 && len(lines) > 2 {
		lines[2] = dimStyle.Render(" ↑ more above")
	}

	if len(filtered) > data.scroll+data.visibleLines {
		lines = append(lines, dimStyle.Render(" ↓ more below"))
	}

	lines = append(lines, "")
	if data.filter != "" {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("Filter: %s (%d models)", data.filter, len(filtered))))
	}
	lines = append(lines, dimStyle.Render("Enter to select • Esc to cancel • Tab to apply filter"))
	lines = append(lines, "")
	lines = append(lines, "Filter: "+data.filterInput.View())

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderProfilesMode() string {
	data := m.modeData.(*profilesMode)

	var lines []string
	lines = append(lines, titleStyle.Render("Select Profile"))
	lines = append(lines, "")

	for i, profile := range data.profiles {
		prefix := "  "
		if i == data.selected {
			prefix = "→ "
		}

		profileName := profile.Name
		if i == data.selected {
			profileName = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(profileName)
		} else {
			profileName = dimStyle.Render(profileName)
		}

		lines = append(lines, prefix+profileName)
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("Enter to select • n new • Esc to cancel"))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderProfileFormMode() string {
	data := m.modeData.(*profileFormMode)

	var lines []string
	lines = append(lines, titleStyle.Render(data.title))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(data.help))
	lines = append(lines, "")

	for i, label := range data.labels {
		prefix := "  "
		if i == data.selected {
			prefix = "→ "
		}

		input := data.inputs[i].View()
		if i == data.selected {
			input = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(input)
		}

		lines = append(lines, prefix+label+": "+input)
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("Enter to save • Tab to switch fields • Esc to cancel"))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderTextInputMode() string {
	data := m.modeData.(*textInputMode)

	var lines []string
	lines = append(lines, titleStyle.Render(data.title))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(data.help))
	lines = append(lines, "")
	lines = append(lines, " "+data.input.View())
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("Enter to save • Esc to cancel"))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderPermissionsMode() string {
	data := m.modeData.(*permissionMode)

	var lines []string
	lines = append(lines, titleStyle.Render("Tool Approval Required"))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("The assistant wants to run these tools:"))
	lines = append(lines, "")

	for i, toolCall := range data.toolCalls {
		prefix := "  "
		if i == data.selected {
			prefix = "→ "
		}

		toolInfo := fmt.Sprintf("%s(%s)", toolCall.Function.Name, truncate(toolCall.Function.Arguments, 50))
		if data.approvals[i] {
			toolInfo = toolStyle.Render("✓ ") + toolInfo
		} else {
			toolInfo = dimStyle.Render("✗ ") + toolInfo
		}

		if i == data.selected {
			toolInfo = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(toolInfo)
		}

		lines = append(lines, prefix+toolInfo)
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("Space to toggle • Enter to confirm • Esc to cancel all"))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderInput() string {
	inputRendered := m.input.View()

	styledInput := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(0, 1).
		Width(m.width - 4).
		Render(inputRendered)

	return styledInput
}

func (m Model) renderFooter() string {
	scrollHint := ""
	if m.useViewport {
		scrollHint = " • ↑/↓ scroll"
	}

	var contextHint string
	if m.contextUsed > 0 && m.contextLimit > 0 {
		percent := float64(m.contextUsed) / float64(m.contextLimit) * 100
		if percent > 80 {
			contextHint = fmt.Sprintf(" • ctx %.0f%%", percent)
		} else {
			contextHint = fmt.Sprintf(" • ctx %.0f%%", percent)
		}
	}

	hint := fmt.Sprintf(" Ctrl+C quit%s%s • Tab complete • /help", scrollHint, contextHint)
	return "\n" + dimStyle.Render(hint)
}

func roleMeta(msg api.Message) (string, lipgloss.Color) {
	switch msg.Role {
	case "user":
		return "You", lipgloss.Color("2")
	case "assistant":
		return "Cardinal", lipgloss.Color("5")
	case "tool":
		return "Tool", lipgloss.Color("3")
	default:
		return msg.Role, lipgloss.Color("8")
	}
}

func compactEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	return u.Host
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
