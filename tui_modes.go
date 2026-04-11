package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"cardinal/pkg/api"
	"cardinal/pkg/config"
)

type textInputMode struct {
	title string
	help  string
	input textinput.Model
}

type modelsMode struct {
	models       []api.Model
	selected     int
	filter       string
	filterInput  textinput.Model
	scroll       int
	visibleLines int
}

type profilesMode struct {
	profiles []config.Profile
	selected int
}

type profileFormMode struct {
	title    string
	help     string
	labels   []string
	inputs   []textinput.Model
	selected int
}

type permissionMode struct {
	assistantContent string
	toolCalls        []api.ToolCall
	approvals        []bool
	selected         int
}

func (m Model) handleSlashCommand(cmd string) (tea.Model, tea.Cmd) {
	cmd = strings.TrimSpace(cmd)
	m.input.SetValue("")
	m.suggestions = nil
	m.err = nil

	switch cmd {
	case "/clear":
		m.messages = nil
		m.streaming = ""
		m.pendingToolCalls = nil
		m.err = nil
		m.scrollOffset = 0
		m.useViewport = false
		m.status = "Cleared conversation"
		return m, nil

	case "/undo":
		return m.undoLastMessage()

	case "/help":
		help := `Available commands:
/clear       - Clear chat history
/undo        - Undo last message pair (user + assistant)
/models      - Choose a model from the active profile endpoint
/profiles    - Switch or edit saved profiles
/profile new - Create and activate a new profile
/profile edit- Edit the active profile
/endpoint    - Update the active profile endpoint
/apikey      - Update the active profile API key
/tools       - List available tools
/autoapprove - Toggle auto-approve all tool calls
/help        - Show this help`
		m.messages = append(m.messages, api.Message{Role: "assistant", Content: help})
		m.status = "Ready"
		return m, nil

	case "/models":
		m.busy = true
		m.status = "Loading models"
		return m, m.fetchModels()

	case "/profiles", "/profile":
		m = m.openProfilesMode()
		return m, nil

	case "/profile new":
		m = m.openProfileForm(config.Profile{APIURL: m.cfg.APIURL, APIKey: m.cfg.APIKey, Model: m.cfg.Model}, false)
		return m, nil

	case "/profile edit":
		m = m.openProfileForm(config.Profile{Name: m.cfg.ActiveProfileName(), APIURL: m.cfg.APIURL, APIKey: m.cfg.APIKey, Model: m.cfg.Model}, true)
		return m, nil

	case "/endpoint":
		m.mode = "endpoint"
		m.modeData = newTextInputMode(m.width, "Set API Endpoint", "Changes are saved to the active profile.", m.cfg.APIURL, false)
		m.resize(m.width, m.height)
		return m, nil

	case "/apikey":
		m.mode = "apikey"
		m.modeData = newTextInputMode(m.width, "Set API Key", "Leave blank to clear the stored key.", m.cfg.APIKey, true)
		m.resize(m.width, m.height)
		return m, nil

	case "/autoapprove":
		m.autoApprove = !m.autoApprove
		status := "disabled"
		if m.autoApprove {
			status = "enabled"
		}
		m.messages = append(m.messages, api.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("Auto-approve tool calls %s. All tool calls will be executed without prompting.", status),
		})
		m.status = "Ready"
		return m, nil

	default:
		m.err = fmt.Errorf("unknown command: %s", cmd)
		return m, nil
	}
}

func (m Model) undoLastMessage() (tea.Model, tea.Cmd) {
	if len(m.messages) == 0 {
		m.status = "Nothing to undo"
		return m, nil
	}

	// Find the last user message and remove it along with the assistant response
	// Messages are in order: system, user, assistant, user, assistant, ...
	// We need to remove the last user-assistant pair

	// Find the last user message index
	lastUserIdx := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	if lastUserIdx == -1 {
		m.status = "No user message to undo"
		return m, nil
	}

	// Remove from the last user message to the end
	// This removes the user message and any following assistant/tool messages
	m.messages = m.messages[:lastUserIdx]
	m.scrollOffset = 0
	m.useViewport = false
	m.status = "Undid last message"
	return m, nil
}

func (m Model) updateMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case "models":
		return m.updateModelsMode(msg)
	case "profiles":
		return m.updateProfilesMode(msg)
	case "profileForm":
		return m.updateProfileFormMode(msg)
	case "endpoint", "apikey":
		return m.updateTextInputMode(msg)
	case "permissions":
		return m.updatePermissionsMode(msg)
	default:
		return m, nil
	}
}

func (m Model) openProfilesMode() Model {
	profiles := m.cfg.ListProfiles()
	selected := 0
	for i, profile := range profiles {
		if profile.Name == m.cfg.ActiveProfileName() {
			selected = i
			break
		}
	}
	m.mode = "profiles"
	m.modeData = &profilesMode{profiles: profiles, selected: selected}
	m.resize(m.width, m.height)
	return m
}

func (m Model) openProfileForm(profile config.Profile, editing bool) Model {
	title := "Create Profile"
	help := "Enter saves and activates the profile."
	if editing {
		title = "Edit Profile"
		help = "Enter saves changes. Changing the name creates a new profile."
	}
	m.mode = "profileForm"
	m.modeData = newProfileFormMode(m.width, title, help, profile)
	m.resize(m.width, m.height)
	return m
}

func (m Model) updateModelsMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	data := m.modeData.(*modelsMode)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape, tea.KeyCtrlC:
			m.mode = ""
			m.modeData = nil
			return m, nil

		case tea.KeyTab:
			data.filter = strings.TrimSpace(data.filterInput.Value())
			data.selected = 0
			data.scroll = 0
			return m, nil

		case tea.KeyUp:
			filtered := filterModels(data.models, data.filter)
			if len(filtered) > 0 && data.selected > 0 {
				data.selected--
				if data.selected < data.scroll {
					data.scroll = data.selected
				}
			}
			return m, nil

		case tea.KeyDown:
			filtered := filterModels(data.models, data.filter)
			if len(filtered) > 0 && data.selected < len(filtered)-1 {
				data.selected++
				visibleLines := data.visibleLines
				if visibleLines <= 0 {
					visibleLines = 15
				}
				if data.selected >= data.scroll+visibleLines {
					data.scroll = data.selected - visibleLines + 1
				}
			}
			return m, nil

		case tea.KeyCtrlU:
			data.selected = max(0, data.selected-5)
			data.scroll = max(0, data.scroll-5)
			return m, nil

		case tea.KeyCtrlD:
			filtered := filterModels(data.models, data.filter)
			visibleLines := data.visibleLines
			if visibleLines <= 0 {
				visibleLines = 15
			}
			data.selected = min(len(filtered)-1, data.selected+5)
			data.scroll = min(max(0, len(filtered)-visibleLines), data.scroll+5)
			return m, nil

		case tea.KeyEnter:
			filtered := filterModels(data.models, data.filter)
			if len(filtered) == 0 {
				return m, nil
			}
			// Ensure selected is within bounds
			if data.selected >= len(filtered) {
				data.selected = len(filtered) - 1
			}
			selectedModel := filtered[data.selected].ID
			m.cfg.SetModel(selectedModel)
			m.messages = append(m.messages, api.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("Model set to %s.", selectedModel),
			})
			m.mode = ""
			m.modeData = nil
			m.status = "Updated model"
			return m, nil
		}
	}

	var cmd tea.Cmd
	data.filterInput, cmd = data.filterInput.Update(msg)
	data.filter = strings.TrimSpace(data.filterInput.Value())

	// Clamp selected within filtered list
	filtered := filterModels(data.models, data.filter)
	if len(filtered) > 0 && data.selected >= len(filtered) {
		data.selected = len(filtered) - 1
		if data.selected < 0 {
			data.selected = 0
		}
	}

	return m, cmd
}

func filterModels(models []api.Model, filter string) []api.Model {
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

func (m Model) updateProfilesMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	data := m.modeData.(*profilesMode)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape, tea.KeyCtrlC:
			m.mode = ""
			m.modeData = nil
			return m, nil

		case tea.KeyUp:
			if data.selected > 0 {
				data.selected--
			}
			return m, nil

		case tea.KeyDown:
			if data.selected < len(data.profiles)-1 {
				data.selected++
			}
			return m, nil

		case tea.KeyEnter:
			if len(data.profiles) == 0 {
				return m, nil
			}
			selectedProfile := data.profiles[data.selected]
			if err := m.cfg.SwitchProfile(selectedProfile.Name); err != nil {
				m.err = err
				return m, nil
			}
			m.client = api.NewClient(m.cfg.APIURL, m.cfg.APIKey)
			m.messages = append(m.messages, api.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("Active profile set to %s.", selectedProfile.Name),
			})
			m.mode = ""
			m.modeData = nil
			m.status = "Updated profile"
			return m, nil
		}

		switch strings.ToLower(msg.String()) {
		case "n":
			m = m.openProfileForm(config.Profile{APIURL: m.cfg.APIURL, APIKey: m.cfg.APIKey, Model: m.cfg.Model}, false)
			return m, nil
		case "e":
			if len(data.profiles) == 0 {
				return m, nil
			}
			m = m.openProfileForm(data.profiles[data.selected], true)
			return m, nil
		}
	}

	return m, nil
}

func (m Model) updateProfileFormMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	data := m.modeData.(*profileFormMode)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape, tea.KeyCtrlC:
			m.mode = ""
			m.modeData = nil
			return m, nil

		case tea.KeyEnter:
			profile := config.Profile{
				Name:   strings.TrimSpace(data.inputs[0].Value()),
				APIURL: strings.TrimSpace(data.inputs[1].Value()),
				APIKey: strings.TrimSpace(data.inputs[2].Value()),
				Model:  strings.TrimSpace(data.inputs[3].Value()),
			}
			if err := m.cfg.SaveProfile(profile, true); err != nil {
				m.err = err
				return m, nil
			}
			m.client = api.NewClient(m.cfg.APIURL, m.cfg.APIKey)
			m.messages = append(m.messages, api.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("Saved and activated profile %s.", profile.Name),
			})
			m.mode = ""
			m.modeData = nil
			m.status = "Updated profile"
			return m, nil

		case tea.KeyTab, tea.KeyShiftTab, tea.KeyUp, tea.KeyDown:
			data.selected = nextFormIndex(data.selected, len(data.inputs), msg.Type)
			focusProfileInput(data, data.selected)
			return m, nil
		}
	}

	var cmd tea.Cmd
	data.inputs[data.selected], cmd = data.inputs[data.selected].Update(msg)
	return m, cmd
}

func (m Model) updateTextInputMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	data := m.modeData.(*textInputMode)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape, tea.KeyCtrlC:
			m.mode = ""
			m.modeData = nil
			return m, nil

		case tea.KeyEnter:
			value := strings.TrimSpace(data.input.Value())
			switch m.mode {
			case "endpoint":
				if value == "" {
					m.err = fmt.Errorf("endpoint cannot be empty")
					return m, nil
				}
				m.cfg.SetAPIURL(value)
				m.client = api.NewClient(m.cfg.APIURL, m.cfg.APIKey)
				m.messages = append(m.messages, api.Message{Role: "assistant", Content: fmt.Sprintf("Endpoint set to %s.", value)})
				m.status = "Updated endpoint"

			case "apikey":
				m.cfg.SetAPIKey(value)
				m.client = api.NewClient(m.cfg.APIURL, m.cfg.APIKey)
				message := "API key cleared."
				if value != "" {
					message = "API key updated."
				}
				m.messages = append(m.messages, api.Message{Role: "assistant", Content: message})
				m.status = "Updated API key"
			}
			m.mode = ""
			m.modeData = nil
			return m, nil
		}
	}

	var cmd tea.Cmd
	data.input, cmd = data.input.Update(msg)
	return m, cmd
}

func (m Model) updatePermissionsMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	data := m.modeData.(*permissionMode)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape, tea.KeyCtrlC:
			approvals := make([]bool, len(data.toolCalls))
			m.mode = ""
			m.modeData = nil
			m.busy = true
			m.status = "Continuing without tool execution"
			return m, m.executeToolPlanCmd(data.assistantContent, data.toolCalls, approvals)

		case tea.KeyUp:
			if data.selected > 0 {
				data.selected--
			}
			return m, nil

		case tea.KeyDown:
			if data.selected < len(data.toolCalls)-1 {
				data.selected++
			}
			return m, nil

		case tea.KeySpace:
			if len(data.approvals) > 0 {
				data.approvals[data.selected] = !data.approvals[data.selected]
			}
			return m, nil

		case tea.KeyEnter:
			approvals := append([]bool(nil), data.approvals...)
			m.mode = ""
			m.modeData = nil
			m.busy = true
			if approvedToolCount(approvals) == 0 {
				m.status = "Continuing without tool execution"
			} else {
				m.status = "Running approved tools"
			}
			return m, m.executeToolPlanCmd(data.assistantContent, data.toolCalls, approvals)
		}

		switch msg.String() {
		case "A":
			for i := range data.approvals {
				data.approvals[i] = true
			}
			return m, nil
		case "R":
			for i := range data.approvals {
				data.approvals[i] = false
			}
			return m, nil
		}

		switch strings.ToLower(msg.String()) {
		case "a":
			if len(data.approvals) > 0 {
				data.approvals[data.selected] = true
			}
			return m, nil
		case "r":
			if len(data.approvals) > 0 {
				data.approvals[data.selected] = false
			}
			return m, nil
		}
	}

	return m, nil
}

func newTextInputMode(width int, title, help, value string, secret bool) *textInputMode {
	input := textinput.New()
	input.SetValue(value)
	input.CursorEnd()
	input.Focus()
	input.Width = max(width-8, 20)
	if secret {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
	}
	return &textInputMode{title: title, help: help, input: input}
}

func newProfileFormMode(width int, title, help string, profile config.Profile) *profileFormMode {
	labels := []string{"Name", "API URL", "API Key", "Model"}
	values := []string{profile.Name, profile.APIURL, profile.APIKey, profile.Model}

	inputs := make([]textinput.Model, len(labels))
	for i := range labels {
		input := textinput.New()
		input.SetValue(values[i])
		input.CursorEnd()
		input.Width = max(width-18, 20)
		if i == 2 {
			input.EchoMode = textinput.EchoPassword
			input.EchoCharacter = '•'
		}
		inputs[i] = input
	}

	data := &profileFormMode{
		title:    title,
		help:     help,
		labels:   labels,
		inputs:   inputs,
		selected: 0,
	}
	focusProfileInput(data, 0)
	return data
}

func focusProfileInput(data *profileFormMode, selected int) {
	for i := range data.inputs {
		if i == selected {
			data.inputs[i].Focus()
			continue
		}
		data.inputs[i].Blur()
	}
	data.selected = selected
}

func nextFormIndex(current, total int, key tea.KeyType) int {
	if total == 0 {
		return 0
	}
	switch key {
	case tea.KeyShiftTab, tea.KeyUp:
		if current == 0 {
			return total - 1
		}
		return current - 1
	default:
		return (current + 1) % total
	}
}

func newPermissionMode(assistantContent string, toolCalls []api.ToolCall) *permissionMode {
	return &permissionMode{
		assistantContent: assistantContent,
		toolCalls:        append([]api.ToolCall(nil), toolCalls...),
		approvals:        defaultToolApprovals(toolCalls),
	}
}
