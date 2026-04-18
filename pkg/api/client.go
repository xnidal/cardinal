package api

import (
	"context"
	"encoding/json"
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

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
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

func (c *Client) ChatStreamChannel(model string, messages []Message, tools []Tool) <-chan StreamEvent {
	return c.ChatStreamChannelCtx(context.Background(), model, messages, tools)
}

func (c *Client) ChatStreamChannelCtx(ctx context.Context, model string, messages []Message, tools []Tool) <-chan StreamEvent {
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

		params := openai.ChatCompletionNewParams{
			Messages:  chatMessages,
			Model:     openai.ChatModel(model),
			MaxTokens: openai.Int(4096),
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			},
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
			ch <- StreamEvent{Type: "error", Error: err}
			return
		}

		emitToolCalls()
		ch <- StreamEvent{Type: "done"}
	}()

	return ch
}

func (c *Client) Chat(model string, messages []Message, tools []Tool) (Message, Usage, error) {
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
			chatMessages[i] = openai.ToolMessage(msg.Name, msg.Content)
		default:
			chatMessages[i] = openai.UserMessage(msg.Content)
		}
	}

	params := openai.ChatCompletionNewParams{
		Messages: chatMessages,
		Model:    openai.ChatModel(model),
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
		return Message{}, Usage{}, err
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
		return nil, err
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
