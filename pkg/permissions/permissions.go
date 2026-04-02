package permissions

import "strings"

type Mode string

const (
	Allow Mode = "allow"
	Ask   Mode = "ask"
	Deny  Mode = "deny"
)

type Policy struct {
	Bash      Mode `json:"bash"`
	ListFiles Mode `json:"list_files"`
	ReadFile  Mode `json:"read_file"`
	WriteFile Mode `json:"write_file"`
}

func DefaultPolicy() Policy {
	return Policy{
		Bash:      Ask,
		ListFiles: Allow,
		ReadFile:  Allow,
		WriteFile: Ask,
	}
}

func Normalize(policy Policy) Policy {
	defaults := DefaultPolicy()
	if !isValidMode(policy.Bash) {
		policy.Bash = defaults.Bash
	}
	if !isValidMode(policy.ListFiles) {
		policy.ListFiles = defaults.ListFiles
	}
	if !isValidMode(policy.ReadFile) {
		policy.ReadFile = defaults.ReadFile
	}
	if !isValidMode(policy.WriteFile) {
		policy.WriteFile = defaults.WriteFile
	}
	return policy
}

func (p Policy) ModeFor(toolName string) Mode {
	p = Normalize(p)
	switch strings.TrimSpace(toolName) {
	case "bash":
		return p.Bash
	case "list_files":
		return p.ListFiles
	case "read_file":
		return p.ReadFile
	case "write_file":
		return p.WriteFile
	default:
		return Deny
	}
}

func (p Policy) Set(toolName string, mode Mode) Policy {
	p = Normalize(p)
	if !isValidMode(mode) {
		return p
	}

	switch strings.TrimSpace(toolName) {
	case "bash":
		p.Bash = mode
	case "list_files":
		p.ListFiles = mode
	case "read_file":
		p.ReadFile = mode
	case "write_file":
		p.WriteFile = mode
	}

	return p
}

func isValidMode(mode Mode) bool {
	switch mode {
	case Allow, Ask, Deny:
		return true
	default:
		return false
	}
}
