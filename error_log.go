package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cardinal/pkg/api"
)

func logBadRequestError(err error, model string, messages []api.Message, tools []api.Tool) {
	if err == nil {
		return
	}
	apiErr, ok := err.(*api.APIError)
	if !ok || apiErr.StatusCode != 400 {
		return
	}

	dir := filepath.Join(".", ".cardinal")
	if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
		fmt.Fprintf(os.Stderr, "failed to create .cardinal directory: %v\n", mkErr)
		return
	}

	logPath := filepath.Join(dir, "errors.txt")

	type logEntry struct {
		Timestamp  string        `json:"timestamp"`
		Model      string        `json:"model"`
		Error      string        `json:"error"`
		StatusCode int           `json:"status_code"`
		Messages   []api.Message `json:"messages"`
		Tools      []api.Tool    `json:"tools,omitempty"`
	}

	entry := logEntry{
		Timestamp:  time.Now().Format(time.RFC3339),
		Model:      model,
		Error:      apiErr.Message,
		StatusCode: apiErr.StatusCode,
		Messages:   messages,
		Tools:      tools,
	}

	data, jsonErr := json.MarshalIndent(entry, "", "  ")
	if jsonErr != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal error log: %v\n", jsonErr)
		return
	}

	f, openErr := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if openErr != nil {
		fmt.Fprintf(os.Stderr, "failed to open error log: %v\n", openErr)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "%s\n---\n", string(data))
}
