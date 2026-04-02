package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Name      string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Index    int    `json:"index,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Parameters  map[string]interface{} `json:"parameters"`
	} `json:"function"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	Tools    []Tool    `json:"tools,omitempty"`
}

type ChatChoice struct {
	Message      Message `json:"message"`
	Delta        Delta   `json:"delta"`
	FinishReason string  `json:"finish_reason"`
}

type Delta struct {
	Content   string     `json:"content"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	Choices []ChatChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

type StreamChunk struct {
	Choices []ChatChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
}

type Model struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type ModelsResponse struct {
	Data []Model `json:"data"`
}

// APIError represents an error from the API with status code
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Message)
}

type Client struct {
	BaseURL string
	APIKey  string
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
	}
}

func (c *Client) doRequest(method, path string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reqBody)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}
	}

	return respBody, resp.StatusCode, nil
}

func (c *Client) Chat(model string, messages []Message, tools []Tool) (Message, Usage, error) {
	reqBody := ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Tools:    tools,
	}

	respBody, _, err := c.doRequest("POST", "/chat/completions", reqBody)
	if err != nil {
		return Message{}, Usage{}, err
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return Message{}, Usage{}, err
	}

	if len(chatResp.Choices) == 0 {
		return Message{}, Usage{}, fmt.Errorf("no response from API")
	}

	return chatResp.Choices[0].Message, chatResp.Usage, nil
}

type StreamEvent struct {
	Type            string
	Content         string
	Thinking        string
	Tool            *ToolCall
	Error           error
	ToolCallWriting bool
	ToolCallName    string
	ToolCallArgsLen int
	Usage           *Usage
}

func (c *Client) ChatStreamChannel(model string, messages []Message, tools []Tool) <-chan StreamEvent {
	ch := make(chan StreamEvent, 100)

	go func() {
		defer close(ch)

		reqBody := ChatRequest{
			Model:    model,
			Messages: messages,
			Stream:   true,
			Tools:    tools,
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			ch <- StreamEvent{Type: "error", Error: err}
			return
		}

		req, err := http.NewRequest("POST", c.BaseURL+"/chat/completions", bytes.NewReader(jsonBody))
		if err != nil {
			ch <- StreamEvent{Type: "error", Error: err}
			return
		}

		req.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			ch <- StreamEvent{Type: "error", Error: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			ch <- StreamEvent{
				Type: "error",
				Error: &APIError{
					StatusCode: resp.StatusCode,
					Message:    string(body),
				},
			}
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

		pendingToolCalls := make(map[int]ToolCall)
		var toolOrder []int

		emitToolCalls := func() {
			for _, index := range toolOrder {
				toolCall := pendingToolCalls[index]
				ch <- StreamEvent{Type: "tool_call", Tool: &toolCall}
			}
			pendingToolCalls = make(map[int]ToolCall)
			toolOrder = nil
		}

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				emitToolCalls()
				ch <- StreamEvent{Type: "done"}
				return
			}

			var chunk StreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if chunk.Usage != nil {
				ch <- StreamEvent{Type: "usage", Usage: chunk.Usage}
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]

			if choice.Delta.Content != "" {
				ch <- StreamEvent{Type: "content", Content: choice.Delta.Content}
			}

			if choice.Delta.Reasoning != "" {
				ch <- StreamEvent{Type: "thinking", Thinking: choice.Delta.Reasoning}
			}

			for _, deltaToolCall := range choice.Delta.ToolCalls {
				current, exists := pendingToolCalls[deltaToolCall.Index]
				if !exists {
					current.Index = deltaToolCall.Index
					toolOrder = append(toolOrder, deltaToolCall.Index)
					ch <- StreamEvent{Type: "tool_call_writing", ToolCallWriting: true, ToolCallName: deltaToolCall.Function.Name}
				}

				if deltaToolCall.ID != "" {
					current.ID = deltaToolCall.ID
				}
				if deltaToolCall.Type != "" {
					current.Type = deltaToolCall.Type
				}
				if deltaToolCall.Function.Name != "" {
					current.Function.Name = deltaToolCall.Function.Name
				}
				if deltaToolCall.Function.Arguments != "" {
					current.Function.Arguments += deltaToolCall.Function.Arguments
				}

				pendingToolCalls[deltaToolCall.Index] = current

				ch <- StreamEvent{Type: "tool_call_writing", ToolCallWriting: true, ToolCallName: current.Function.Name, ToolCallArgsLen: len(current.Function.Arguments)}
			}

			if choice.FinishReason == "tool_calls" {
				emitToolCalls()
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Type: "error", Error: err}
		}
	}()

	return ch
}

func (c *Client) ListModels() ([]Model, error) {
	respBody, _, err := c.doRequest("GET", "/models", nil)
	if err != nil {
		return nil, err
	}

	var modelsResp ModelsResponse
	if err := json.Unmarshal(respBody, &modelsResp); err != nil {
		return nil, err
	}

	return modelsResp.Data, nil
}
