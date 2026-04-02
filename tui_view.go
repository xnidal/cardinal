package main

import (
	"fmt"
	"net/url"
	"path/filepath"
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
	statusStyle := lipgloss.NewStyle().Foreground(color)

	spinner := throbberStyle.Render("[ " + m.spinner.View() + " ]")
	statusText := statusStyle.Render(status)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 2).
		Render(spinner + " " + statusText)

	return box
}

func getStatusColor(status string) lipgloss.Color {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "thinking") || strings.Contains(lower, "hmmm") || strings.Contains(lower, "ponder") || strings.Contains(lower, "comput") || strings.Contains(lower, "process") || strings.Contains(lower, "load"):
		return lipgloss.Color("81")
	case strings.Contains(lower, "receiving") || strings.Contains(lower, "writing"):
		return lipgloss.Color("177")
	case strings.Contains(lower, "running") || strings.Contains(lower, "tools") || strings.Contains(lower, "continu"):
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
	status := m.status
	if status == "" {
		status = "Ready"
	}

	statusColor := getStatusColor(status)

	var statusText string
	if m.autoApprove {
		statusText = lipgloss.NewStyle().
			Foreground(statusColor).
			Bold(true).
			Render("[ " + status + " ] auto-approve")
	} else {
		statusText = lipgloss.NewStyle().
			Foreground(statusColor).
			Bold(true).
			Render("[ " + status + " ]")
	}

	info := subtitleStyle.Render(
		"  " + m.cfg.ActiveProfileName() + " > " + m.cfg.Model + " @ " + compactEndpoint(m.cfg.APIURL),
	)

	title := lipgloss.NewStyle().
		Foreground(accentColor).
		Bold(true).
		Render("Cardinal")

	return lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().MarginBottom(1).Render(
			lipgloss.JoinHorizontal(
				lipgloss.Top,
				title,
				lipgloss.NewStyle().PaddingLeft(2).Render(statusText),
			),
		),
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
		blocks = append(blocks,
			lipgloss.NewStyle().
				Foreground(dimColor).
				Italic(true).
				Render("↑ "+fmt.Sprintf("%d older message%s", m.scrollOffset, pluralize(m.scrollOffset))),
		)
	}

	for _, message := range m.getVisibleMessages() {
		if rendered := m.renderMessage(message); rendered != "" {
			blocks = append(blocks, rendered, "")
		}
	}

	if hasStreaming {
		blocks = append(blocks, m.renderStreamingMessage())
	}

	if m.err != nil {
		errorBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(errorColor).
			Padding(0, 1).
			Render("⚠ Error: " + m.err.Error())
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

	var icon string
	if msg.Role == "user" {
		icon = ">"
	} else {
		icon = "-"
	}

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

	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			toolName := tc.Function.Name
			args := strings.TrimSpace(tc.Function.Arguments)

			var toolDisplay string
			if args != "" && args != "{}" {
				truncated := truncate(args, m.width-12)
				toolDisplay = lipgloss.NewStyle().
					Foreground(warningColor).
					Render("  [tool] "+toolName) +
					lipgloss.NewStyle().
						Foreground(dimColor).
						Render(" "+truncated)
			} else {
				toolDisplay = lipgloss.NewStyle().
					Foreground(warningColor).
					Render("  [tool] " + toolName)
			}
			blocks = append(blocks, toolDisplay)
		}
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
				Foreground(successColor).
				Render("[ok] read ") +
				lipgloss.NewStyle().
					Foreground(accentColor).
					Render(displayPath) +
				lipgloss.NewStyle().
					Foreground(dimColor).
					Render(" ("+linesInfo+")")
		} else {
			content = lipgloss.NewStyle().
				Foreground(successColor).
				Render("[ok] read ") +
				lipgloss.NewStyle().
					Foreground(accentColor).
					Render(displayPath)
		}

	case "list_files":
		path := extractPathFromToolResult(msg.Content)
		displayPath := m.formatPath(path)
		content = lipgloss.NewStyle().
			Foreground(successColor).
			Render("[ok] list ") +
			lipgloss.NewStyle().
				Foreground(accentColor).
				Render(displayPath)

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
			Foreground(successColor).
			Render("[ok] edit_soul")

	case "calculate":
		content = m.formatCalculateOutput(msg.Content, maxHeight, maxWidth)

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
	formattedLines = append(formattedLines,
		lipgloss.NewStyle().
			Foreground(successColor).
			Render("[ok] bash:"),
	)

	for _, line := range lines {
		if len(line) > maxWidth {
			line = line[:maxWidth-3] + "..."
		}
		if strings.TrimSpace(line) != "" {
			formattedLines = append(formattedLines,
				lipgloss.NewStyle().
					Foreground(dimColor).
					Render("    "+line),
			)
		}
	}

	return strings.Join(formattedLines, "\n")
}

func (m Model) formatWriteFileOutput(content string, maxHeight, maxWidth int) string {
	path := extractPathFromToolResult(content)
	displayPath := m.formatPath(path)

	return lipgloss.NewStyle().
		Foreground(successColor).
		Render("[ok] write ") +
		lipgloss.NewStyle().
			Foreground(accentColor).
			Render(displayPath)
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
			Foreground(successColor).
			Render("[ok] calculate: ") +
			lipgloss.NewStyle().
				Foreground(accentColor).
				Render(strings.Join(outputLines, " "))
	}
	return lipgloss.NewStyle().Foreground(successColor).Render("[ok] calculate")
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
			Foreground(successColor).
			Render("[ok] edit "+displayPath+"\\n") +
			strings.Join(coloredDiff, "\\n")
	}
	return lipgloss.NewStyle().
		Foreground(successColor).
		Render("[ok] edit ") +
		lipgloss.NewStyle().
			Foreground(accentColor).
			Render(displayPath)
}

func (m Model) formatGrepOutput(content string, maxHeight, maxWidth int) string {
	lines := strings.Split(content, "\\n")
	if len(lines) > maxHeight {
		lines = append(lines[:maxHeight],
			lipgloss.NewStyle().
				Foreground(warningColor).
				Italic(true).
				Render(fmt.Sprintf("  ... %d more lines", len(lines)-maxHeight)),
		)
	}

	var formattedLines []string
	formattedLines = append(formattedLines,
		lipgloss.NewStyle().Foreground(successColor).Render("[ok] grep:"),
	)

	for _, line := range lines {
		if len(line) > maxWidth {
			line = line[:maxWidth-3] + "..."
		}
		if strings.TrimSpace(line) != "" {
			formattedLines = append(formattedLines,
				lipgloss.NewStyle().Foreground(dimColor).Render("    "+line),
			)
		}
	}

	return strings.Join(formattedLines, "\\n")
}

func (m Model) formatGlobOutput(content string, maxHeight, maxWidth int) string {
	lines := strings.Split(content, "\\n")
	if len(lines) > maxHeight {
		lines = append(lines[:maxHeight],
			lipgloss.NewStyle().
				Foreground(warningColor).
				Italic(true).
				Render(fmt.Sprintf("  ... %d more results", len(lines)-maxHeight)),
		)
	}

	var formattedLines []string
	formattedLines = append(formattedLines,
		lipgloss.NewStyle().Foreground(successColor).Render("[ok] glob:"),
	)

	for _, line := range lines {
		if len(line) > maxWidth {
			line = line[:maxWidth-3] + "..."
		}
		if strings.TrimSpace(line) != "" {
			formattedLines = append(formattedLines,
				lipgloss.NewStyle().Foreground(dimColor).Render("    "+line),
			)
		}
	}

	return strings.Join(formattedLines, "\\n")
}

func (m Model) formatFileInfoOutput(content string, maxHeight, maxWidth int) string {
	return lipgloss.NewStyle().Foreground(successColor).Render("[ok] file_info")
}

func (m Model) formatDefaultToolOutput(content string, maxHeight, maxWidth int) string {
	lines := strings.Split(content, "\\n")
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

	return strings.Join(formattedLines, "\\n")
}

func extractPathFromToolResult(content string) string {
	lines := strings.Split(content, "\n")
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
	content := strings.TrimSpace(m.streaming)
	if content == "" {
		return ""
	}

	// If we have thinking content, show it
	if m.thinking != "" {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(" Assistant") +
			"\n" +
			lipgloss.NewStyle().PaddingLeft(4).Width(max(m.width-4, 20)).Render(content)
	}

	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5")).Render(" Assistant") +
		"\n" +
		lipgloss.NewStyle().PaddingLeft(4).Width(max(m.width-4, 20)).Render(content)
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
		return "Assistant", lipgloss.Color("5")
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

func (m Model) getVisibleMessages() []api.Message {
	if m.scrollOffset >= len(m.messages) {
		return nil
	}
	return m.messages[m.scrollOffset:]
}
