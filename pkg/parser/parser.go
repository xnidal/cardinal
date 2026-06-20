// Package parser detects and extracts XML-formatted tool calls from
// model output text. This package detects and extracts those calls
// so they can be routed into the normal ToolHandler pipeline.
package parser

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ParsedToolCall represents a single tool call extracted from model output.
type ParsedToolCall struct {
	Name string
	Args string // JSON-encoded string, matching tools.ToolCall convention
}

// ParseResult holds the outcome of parsing model text for XML tool calls.
type ParseResult struct {
	ToolCalls    []ParsedToolCall
	Content      string // remaining text after tool call blocks are stripped
	HasToolCalls bool
}

var (
	toolCallRe            = regexp.MustCompile(`(?s)<tool_call\s+name="([^"]*)"(?:\s+id="[^"]*")?\s*>(.*?)</tool_call>`)
	toolCallSelfClosingRe = regexp.MustCompile(`<tool_call\s+name="([^"]*)"(?:\s+id="[^"]*")?\s*/>`)
	argRe                 = regexp.MustCompile(`(?s)<arg\s+name="([^"]*)"\s*>(.*?)</arg>`)
	invokeRe              = regexp.MustCompile(`(?s)<invoke\s+name="([^"]*)"\s*>(.*?)</invoke>`)
	invokeArgRe           = regexp.MustCompile(`(?s)<parameter\s+name="([^"]*)"\s*>(.*?)</parameter>`)
	containsRe            = regexp.MustCompile(`(?:<tool_call\s+name="[^"]*"|<invoke\s+name="[^"]*")`)
)

// Parse extracts XML-formatted tool calls from model output text.
func Parse(text string) ParseResult {
	var calls []ParsedToolCall
	cleaned := text

	// Self-closing tool_call form
	for _, m := range toolCallSelfClosingRe.FindAllStringSubmatch(cleaned, -1) {
		calls = append(calls, ParsedToolCall{Name: m[1], Args: "{}"})
	}
	cleaned = toolCallSelfClosingRe.ReplaceAllString(cleaned, "")

	// Full tool_call form
	for _, m := range toolCallRe.FindAllStringSubmatch(cleaned, -1) {
		calls = append(calls, ParsedToolCall{
			Name: m[1],
			Args: argsToJSON(parseArgBody(m[2], argRe)),
		})
	}
	cleaned = toolCallRe.ReplaceAllString(cleaned, "")

	// Invoke form
	for _, m := range invokeRe.FindAllStringSubmatch(cleaned, -1) {
		calls = append(calls, ParsedToolCall{
			Name: m[1],
			Args: argsToJSON(parseArgBody(m[2], invokeArgRe)),
		})
	}
	cleaned = invokeRe.ReplaceAllString(cleaned, "")

	return ParseResult{
		ToolCalls:    calls,
		Content:      strings.TrimSpace(cleaned),
		HasToolCalls: len(calls) > 0,
	}
}

// ContainsToolCalls is a cheap pre-check that returns true if the text
// likely contains one or more XML-formatted tool calls.
func ContainsToolCalls(text string) bool {
	return containsRe.MatchString(text) || LooksLikeJSONToolArgs(text)
}

// LooksLikeJSONToolArgs catches model output that is only a JSON object that
// looks like function-call arguments, e.g. {"path":"pkg/api"}. Models
// sometimes emit this instead of using the function calling API.
func LooksLikeJSONToolArgs(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil || len(obj) == 0 {
		return false
	}
	toolArgKeys := map[string]bool{
		"path": true, "command": true, "pattern": true, "content": true,
		"oldText": true, "newText": true, "expression": true, "query": true,
		"include": true, "recursive": true, "offset": true, "limit": true,
	}
	for key := range obj {
		if toolArgKeys[key] {
			return true
		}
	}
	return false
}

// parseArgBody extracts all key-value pairs from a tool call body.
func parseArgBody(body string, argRegex *regexp.Regexp) map[string]string {
	args := make(map[string]string)
	for _, m := range argRegex.FindAllStringSubmatch(body, -1) {
		args[m[1]] = strings.TrimSpace(m[2])
	}
	return args
}

// argsToJSON converts a map of string values to a JSON object string.
func argsToJSON(args map[string]string) string {
	if len(args) == 0 {
		return "{}"
	}
	data, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(data)
}
