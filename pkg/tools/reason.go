package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ReasonPolicy controls which tools require a reason field in their arguments.
type ReasonPolicy struct {
	RequiredTools map[string]bool
}

// DefaultReasonPolicy returns a policy that requires reasons for
// tools that modify the filesystem or execute commands.
func DefaultReasonPolicy() *ReasonPolicy {
	return &ReasonPolicy{
		RequiredTools: map[string]bool{
			"bash":       true,
			"write_file": true,
			"edit_file":  true,
			"edit_soul":  true,
		},
	}
}

// RequiresReason returns true if the named tool requires a reason field.
func (p *ReasonPolicy) RequiresReason(toolName string) bool {
	return p.RequiredTools[toolName]
}

// ValidateReason checks whether a tool call that requires a reason
// includes one in its arguments. Returns nil if valid, or a ToolResult
// with an error message if the reason is missing. Returns nil for
// tools that do not require a reason (even if parsing fails).
func ValidateReason(toolName, args string, policy *ReasonPolicy) *ToolResult {
	if !policy.RequiresReason(toolName) {
		return nil
	}
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		// If we cannot parse, let the tool itself handle the error
		return nil
	}
	if strings.TrimSpace(params.Reason) == "" {
		return &ToolResult{
			Name:    toolName,
			Success: false,
			Error:   fmt.Sprintf("reason is required for %s tool. Add a \"reason\" field explaining why this tool is being used.", toolName),
		}
	}
	return nil
}

// ExtractReason pulls the reason field from tool call arguments.
// Returns empty string if the field is absent or parsing fails.
func ExtractReason(args string) string {
	var params struct {
		Reason string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ""
	}
	return strings.TrimSpace(params.Reason)
}
