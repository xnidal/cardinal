package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cardinal/pkg/api"
	"cardinal/pkg/storage"
)

func logBadRequestError(err error, model string, messages []api.Message, tools []api.Tool) {
	logAPIError(err, model, messages, tools)
}

func logAPIError(err error, model string, messages []api.Message, tools []api.Tool) {
	if err == nil {
		return
	}

	dir := filepath.Join(storage.GetConfigDir(), "debug")
	if mkErr := os.MkdirAll(dir, 0700); mkErr != nil {
		fmt.Fprintf(os.Stderr, "failed to create debug directory: %v\n", mkErr)
		return
	}

	statusCode := 0
	errorMsg := err.Error()
	if apiErr, ok := err.(*api.APIError); ok {
		statusCode = apiErr.StatusCode
		errorMsg = apiErr.Message
	}

	logPath := filepath.Join(dir, "errors.log")

	type logEntry struct {
		Timestamp  string        `json:"timestamp"`
		Model      string        `json:"model"`
		Error      string        `json:"error"`
		StatusCode int           `json:"status_code"`
		Messages   []api.Message `json:"messages,omitempty"`
		Tools      []api.Tool    `json:"tools,omitempty"`
	}

	entry := logEntry{
		Timestamp:  time.Now().Format(time.RFC3339),
		Model:      model,
		Error:      errorMsg,
		StatusCode: statusCode,
	}

	// Only include full messages/tools for client errors (4xx) to keep log size sane
	if statusCode >= 400 && statusCode < 500 {
		entry.Messages = messages
		entry.Tools = tools
	}

	data, jsonErr := json.MarshalIndent(entry, "", "  ")
	if jsonErr != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal error log: %v\n", jsonErr)
		return
	}

	f, openErr := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if openErr != nil {
		fmt.Fprintf(os.Stderr, "failed to open error log: %v\n", openErr)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "%s\n---\n", string(data))
}
