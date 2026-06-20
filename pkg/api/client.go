package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Thinking   string     `json:"thinking,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolArgs   string     `json:"tool_args,omitempty"`
	// MetaLines is an optional structured payload attached by the tool layer
	// (e.g. per-file line ranges for read_files). It is not sent to the
	// model; the UI uses it to render tool cards without re-parsing bodies.
	MetaLines string `json:"meta_lines,omitempty"`
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
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
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

// APIError captures everything needed to diagnose an upstream API failure:
// the HTTP status, the full request/response dumps, headers, raw response body,
// and the structured error fields (code/message/param/type) returned upstream.
type APIError struct {
	StatusCode   int               `json:"status_code"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Message      string            `json:"message"`
	ErrorCode    string            `json:"error_code,omitempty"`
	ErrorParam   string            `json:"error_param,omitempty"`
	ErrorType    string            `json:"error_type,omitempty"`
	RequestBody  string            `json:"request_body,omitempty"`
	RequestDump  string            `json:"request_dump,omitempty"`
	ResponseRaw  string            `json:"response_raw,omitempty"`
	ResponseDump string            `json:"response_dump,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
}

// Error returns a short, single-line representation suitable for the TUI/CLI.
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.ErrorCode != "" {
		return fmt.Sprintf("api error %d (%s): %s", e.StatusCode, e.ErrorCode, e.Message)
	}
	return fmt.Sprintf("api error %d: %s", e.StatusCode, e.Message)
}

// DetailedError returns a multi-line, human-readable dump with the relevant
// wire-level details. Safe to print to a terminal.
func (e *APIError) DetailedError() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "API error %d %s\n", e.StatusCode, http.StatusText(e.StatusCode))
	fmt.Fprintf(&b, "Request: %s %s\n", e.Method, e.URL)
	if e.ErrorCode != "" || e.ErrorType != "" || e.ErrorParam != "" {
		fmt.Fprintf(&b, "Upstream error: code=%q type=%q param=%q\n", e.ErrorCode, e.ErrorType, e.ErrorParam)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, "Message: %s\n", e.Message)
	}
	if len(e.Headers) > 0 {
		fmt.Fprintln(&b, "Response headers:")
		for k, v := range e.Headers {
			fmt.Fprintf(&b, "  %s: %s\n", k, v)
		}
	}
	if e.ResponseRaw != "" {
		fmt.Fprintf(&b, "Response body:\n%s\n", e.ResponseRaw)
	}
	return strings.TrimRight(b.String(), "\n")
}

// fromAPIError converts an OpenAI SDK error into our APIError using the public
// *openai.Error type. The SDK documents errors.As plus DumpRequest/DumpResponse
// as the supported way to access the serialized request/response.
func fromAPIError(err error) *APIError {
	if err == nil {
		return nil
	}

	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return nil
	}

	out := &APIError{
		StatusCode: apiErr.StatusCode,
		Message:    apiErr.Message,
		ErrorCode:  apiErr.Code,
		ErrorParam: apiErr.Param,
		ErrorType:  apiErr.Type,
	}

	if apiErr.Request != nil {
		out.Method = apiErr.Request.Method
		if apiErr.Request.URL != nil {
			out.URL = apiErr.Request.URL.String()
		}

		if dump := apiErr.DumpRequest(true); len(dump) > 0 {
			out.RequestDump = string(dump)
			out.RequestBody = extractHTTPBody(out.RequestDump)
		}

		if out.RequestBody == "" && apiErr.Request.GetBody != nil {
			if rc, gerr := apiErr.Request.GetBody(); gerr == nil && rc != nil {
				if data, rerr := io.ReadAll(rc); rerr == nil {
					out.RequestBody = string(data)
				}
				_ = rc.Close()
			}
		}
	}

	if apiErr.Response != nil {
		headers := make(map[string]string, len(apiErr.Response.Header))
		for k, v := range apiErr.Response.Header {
			headers[strings.ToLower(k)] = strings.Join(v, ", ")
		}
		out.Headers = headers

		if dump := apiErr.DumpResponse(true); len(dump) > 0 {
			out.ResponseDump = string(dump)
			out.ResponseRaw = extractHTTPBody(out.ResponseDump)
		}
	}

	applyStructuredErrorBody(out)

	if out.Message == "" {
		out.Message = strings.TrimSpace(err.Error())
	}
	if out.Message == "" && out.ResponseRaw != "" {
		out.Message = out.ResponseRaw
	}

	return out
}

func wrapAPIError(err error) error {
	if apiErr := fromAPIError(err); apiErr != nil {
		return apiErr
	}
	return err
}

func extractHTTPBody(dump string) string {
	if dump == "" {
		return ""
	}
	if _, body, ok := strings.Cut(dump, "\r\n\r\n"); ok {
		return strings.TrimRight(body, "\r\n")
	}
	if _, body, ok := strings.Cut(dump, "\n\n"); ok {
		return strings.TrimRight(body, "\r\n")
	}
	return ""
}

func applyStructuredErrorBody(out *APIError) {
	if out == nil || strings.TrimSpace(out.ResponseRaw) == "" {
		return
	}

	decoder := json.NewDecoder(strings.NewReader(out.ResponseRaw))
	decoder.UseNumber()

	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return
	}

	if rawErr, ok := body["error"]; ok {
		switch e := rawErr.(type) {
		case map[string]any:
			setAPIErrorFields(out, e)
		case string:
			if out.Message == "" {
				out.Message = e
			}
		}
	}

	setAPIErrorFields(out, body)
}

func setAPIErrorFields(out *APIError, fields map[string]any) {
	if out == nil || fields == nil {
		return
	}
	if out.Message == "" {
		out.Message = jsonValueString(fields["message"])
	}
	if out.ErrorCode == "" {
		out.ErrorCode = jsonValueString(fields["code"])
	}
	if out.ErrorParam == "" {
		out.ErrorParam = jsonValueString(fields["param"])
	}
	if out.ErrorType == "" {
		out.ErrorType = jsonValueString(fields["type"])
	}
}

func jsonValueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		data, err := json.Marshal(x)
		if err == nil {
			return string(data)
		}
		return fmt.Sprint(x)
	}
}

// IsRetryable reports whether the API error is one of the well-known
// retryable upstream failures.
func (e *APIError) IsRetryable() bool {
	if e == nil {
		return false
	}
	switch e.StatusCode {
	case 408, 409, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

type Model struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type Client struct {
	client openai.Client
}

func NewClient(baseURL, apiKey string) *Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" && baseURL != "https://api.openai.com/v1" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{
		client: openai.NewClient(opts...),
	}
}

func (c *Client) GetOpenAIClient() *openai.Client {
	return &c.client
}

func (c *Client) ChatStreamChannel(model string, messages []Message, tools []Tool, maxTokens int) <-chan StreamEvent {
	return c.ChatStreamChannelCtx(context.Background(), model, messages, tools, maxTokens)
}

func CalculateMaxTokens(messages []Message, tools []Tool, contextLimit int) int {
	estimatedPrompt := EstimateTokens(messages, tools)
	maxTokens := max(contextLimit-estimatedPrompt, 1)
	return maxTokens
}

func EstimateTokens(messages []Message, tools []Tool) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4
		total += len(msg.Role) / 4
		if msg.Thinking != "" {
			total += len(msg.Thinking) / 4
			total += 5
		}
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name) / 4
			total += len(tc.Function.Arguments) / 4
		}
		total += 10
	}
	for _, tool := range tools {
		total += len(tool.Function.Name) / 4
		total += len(tool.Function.Description) / 4
		total += 20
	}
	return total
}

func (c *Client) ChatStreamChannelCtx(ctx context.Context, model string, messages []Message, tools []Tool, maxTokens int) <-chan StreamEvent {
	ch := make(chan StreamEvent, 100)

	go func() {
		defer close(ch)

		chatMessages := make([]openai.ChatCompletionMessageParamUnion, len(messages))
		for i, msg := range messages {
			switch msg.Role {
			case "user":
				chatMessages[i] = openai.UserMessage(msg.Content)
			case "assistant":
				if msg.Thinking != "" {
					combined := "<thinking>\n" + msg.Thinking + "\n</thinking>\n\n" + msg.Content
					chatMessages[i] = openai.AssistantMessage(combined)
				} else {
					chatMessages[i] = openai.AssistantMessage(msg.Content)
				}
			case "system":
				chatMessages[i] = openai.SystemMessage(msg.Content)
			case "tool":
				chatMessages[i] = openai.ToolMessage(msg.Content, msg.ToolCallID)
			default:
				chatMessages[i] = openai.UserMessage(msg.Content)
			}
		}

		if maxTokens < 1 {
			maxTokens = 4096
		}

		params := openai.ChatCompletionNewParams{
			Messages:  chatMessages,
			Model:     openai.ChatModel(model),
			MaxTokens: openai.Int(int64(maxTokens)),
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			},
			ParallelToolCalls: openai.Bool(true),
		}

		if len(tools) > 0 {
			chatTools := make([]openai.ChatCompletionToolUnionParam, len(tools))
			for i, tool := range tools {
				chatTools[i] = openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
					Name:        tool.Function.Name,
					Description: openai.String(tool.Function.Description),
					Parameters:  tool.Function.Parameters,
				})
			}
			params.Tools = chatTools
		}

		stream := c.client.Chat.Completions.NewStreaming(ctx, params)

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

		for stream.Next() {
			// Check if context was cancelled
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: "done"}
				return
			default:
			}

			chunk := stream.Current()

			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				ch <- StreamEvent{
					Type: "usage",
					Usage: &Usage{
						PromptTokens:     int(chunk.Usage.PromptTokens),
						CompletionTokens: int(chunk.Usage.CompletionTokens),
						TotalTokens:      int(chunk.Usage.TotalTokens),
					},
				}
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]

			if choice.Delta.Content != "" {
				ch <- StreamEvent{Type: "content", Content: choice.Delta.Content}
			}

			// Handle reasoning_content field (for models like z-ai/glm4.7)
			raw := choice.Delta.RawJSON()
			if strings.Contains(raw, `"reasoning_content":`) {
				var delta struct {
					ReasoningContent string `json:"reasoning_content"`
				}
				if err := json.Unmarshal([]byte(raw), &delta); err == nil && delta.ReasoningContent != "" {
					ch <- StreamEvent{Type: "thinking", Thinking: delta.ReasoningContent}
				}
			}

			for _, deltaToolCall := range choice.Delta.ToolCalls {
				index := int(deltaToolCall.Index)
				current, exists := pendingToolCalls[index]
				if !exists {
					current.Index = index
					toolOrder = append(toolOrder, index)
					ch <- StreamEvent{
						Type:            "tool_call_writing",
						ToolCallWriting: true,
						ToolCallName:    deltaToolCall.Function.Name,
					}
				}
				if deltaToolCall.ID != "" {
					current.ID = deltaToolCall.ID
				}
				if deltaToolCall.Function.Name != "" {
					current.Function.Name = deltaToolCall.Function.Name
				}
				if deltaToolCall.Function.Arguments != "" {
					current.Function.Arguments += deltaToolCall.Function.Arguments
				}
				pendingToolCalls[index] = current
				ch <- StreamEvent{
					Type:            "tool_call_writing",
					ToolCallWriting: true,
					ToolCallName:    current.Function.Name,
					ToolCallArgsLen: len(current.Function.Arguments),
				}
			}

			if choice.FinishReason == "tool_calls" {
				emitToolCalls()
			}
		}

		if err := stream.Err(); err != nil {
			// Check if the error is due to context cancellation
			if ctx.Err() != nil {
				ch <- StreamEvent{Type: "done"}
				return
			}
			ch <- StreamEvent{Type: "error", Error: wrapAPIError(err)}
			return
		}

		emitToolCalls()
		ch <- StreamEvent{Type: "done"}
	}()

	return ch
}

func (c *Client) Chat(model string, messages []Message, tools []Tool, maxTokens int) (Message, Usage, error) {
	ctx := context.Background()

	chatMessages := make([]openai.ChatCompletionMessageParamUnion, len(messages))
	for i, msg := range messages {
		switch msg.Role {
		case "user":
			chatMessages[i] = openai.UserMessage(msg.Content)
		case "assistant":
			if msg.Thinking != "" {
				combined := "<thinking>\n" + msg.Thinking + "\n</thinking>\n\n" + msg.Content
				chatMessages[i] = openai.AssistantMessage(combined)
			} else {
				chatMessages[i] = openai.AssistantMessage(msg.Content)
			}
		case "system":
			chatMessages[i] = openai.SystemMessage(msg.Content)
		case "tool":
			chatMessages[i] = openai.ToolMessage(msg.Content, msg.ToolCallID)
		default:
			chatMessages[i] = openai.UserMessage(msg.Content)
		}
	}

	if maxTokens < 1 {
		maxTokens = 4096
	}

	params := openai.ChatCompletionNewParams{
		Messages:  chatMessages,
		Model:     openai.ChatModel(model),
		MaxTokens: openai.Int(int64(maxTokens)),
	}

	if len(tools) > 0 {
		chatTools := make([]openai.ChatCompletionToolUnionParam, len(tools))
		for i, tool := range tools {
			chatTools[i] = openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
				Name:        tool.Function.Name,
				Description: openai.String(tool.Function.Description),
				Parameters:  tool.Function.Parameters,
			})
		}
		params.Tools = chatTools
	}

	completion, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return Message{}, Usage{}, wrapAPIError(err)
	}

	if len(completion.Choices) == 0 {
		return Message{}, Usage{}, nil
	}

	choice := completion.Choices[0]
	msg := Message{
		Role:    string(choice.Message.Role),
		Content: choice.Message.Content,
	}

	for _, tc := range choice.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: string(tc.Type),
			Function: struct {
				Name      string `json:"name,omitempty"`
				Arguments string `json:"arguments,omitempty"`
			}{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	usage := Usage{
		PromptTokens:     int(completion.Usage.PromptTokens),
		CompletionTokens: int(completion.Usage.CompletionTokens),
		TotalTokens:      int(completion.Usage.TotalTokens),
	}

	return msg, usage, nil
}

func (c *Client) ListModels() ([]Model, error) {
	ctx := context.Background()
	models, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, wrapAPIError(err)
	}
	result := make([]Model, len(models.Data))
	for i, m := range models.Data {
		result[i] = Model{
			ID:     m.ID,
			Object: string(m.Object),
		}
	}
	return result, nil
}
