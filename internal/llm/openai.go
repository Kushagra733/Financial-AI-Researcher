package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const openAIEndpoint = "https://api.openai.com/v1/chat/completions"

// OpenAIProvider implements Provider using the OpenAI Chat Completions API
// with native function/tool calling (finish_reason: "tool_calls").
// Safe for concurrent use — the embedded http.Client is shared.
type OpenAIProvider struct {
	APIKey string
	Model  string
	client *http.Client
}

func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		APIKey: apiKey,
		Model:  "gpt-4o",
		client: &http.Client{Timeout: 90 * time.Second},
	}
}

func (p *OpenAIProvider) SetModel(m string) { p.Model = m }
func (p *OpenAIProvider) GetModel() string  { return p.Model }

// GenerateResponse satisfies the legacy LLMProvider interface in pkg/types.
func (p *OpenAIProvider) GenerateResponse(prompt string, _ map[string]interface{}) (string, error) {
	return fmt.Sprintf("Response to: %s (using %s)", prompt, p.Model), nil
}

// CompleteWithTools sends a single chat completion request and returns either
// the tool calls the LLM wants to invoke, or the final answer text.
func (p *OpenAIProvider) CompleteWithTools(ctx context.Context, req Request) (Response, error) {
	if p.APIKey == "" {
		return Response{}, fmt.Errorf("openai: OPENAI_API_KEY is not configured")
	}

	body := oaiRequest{
		Model:    p.Model,
		Messages: toOAIMessages(req.System, req.Messages),
	}
	if len(req.Tools) > 0 {
		body.Tools = toOAITools(req.Tools)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIEndpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("openai: build http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai: http: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("openai: read body: %w", err)
	}

	var oaiResp oaiResponse
	if err := json.Unmarshal(raw, &oaiResp); err != nil {
		return Response{}, fmt.Errorf("openai: unmarshal response: %w", err)
	}
	if oaiResp.Error != nil {
		return Response{}, fmt.Errorf("openai API error [%s]: %s", oaiResp.Error.Type, oaiResp.Error.Message)
	}

	return fromOAIResponse(oaiResp)
}

// ---------------------------------------------------------------------------
// OpenAI wire types
// ---------------------------------------------------------------------------

type oaiRequest struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
	Tools    []oaiTool    `json:"tools,omitempty"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    *string       `json:"content"`                // null when tool_calls present
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // role=tool only
	Name       string        `json:"name,omitempty"`         // role=tool only
}

type oaiTool struct {
	Type     string      `json:"type"` // always "function"
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function oaiFuncCall `json:"function"`
}

type oaiFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded string
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Error   *oaiError   `json:"error,omitempty"`
}

type oaiChoice struct {
	Message      oaiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type oaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// ---------------------------------------------------------------------------
// Format converters: internal ↔ OpenAI wire
// ---------------------------------------------------------------------------

func toOAIMessages(system string, msgs []Message) []oaiMessage {
	var out []oaiMessage

	if system != "" {
		out = append(out, oaiMessage{Role: "system", Content: strPtr(system)})
	}

	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, oaiMessage{Role: "user", Content: strPtr(m.Content)})

		case "assistant":
			if len(m.ToolCalls) > 0 {
				calls := make([]oaiToolCall, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					args, _ := json.Marshal(tc.Params)
					calls[i] = oaiToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: oaiFuncCall{
							Name:      tc.Name,
							Arguments: string(args),
						},
					}
				}
				// content must be null (not empty string) when tool_calls are present
				out = append(out, oaiMessage{Role: "assistant", ToolCalls: calls})
			} else {
				out = append(out, oaiMessage{Role: "assistant", Content: strPtr(m.Content)})
			}

		case "tool":
			out = append(out, oaiMessage{
				Role:       "tool",
				Content:    strPtr(m.Content),
				ToolCallID: m.ToolCallID,
				Name:       m.Name,
			})
		}
	}

	return out
}

func toOAITools(schemas []ToolSchema) []oaiTool {
	tools := make([]oaiTool, len(schemas))
	for i, s := range schemas {
		tools[i] = oaiTool{
			Type: "function",
			Function: oaiFunction{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		}
	}
	return tools
}

func fromOAIResponse(resp oaiResponse) (Response, error) {
	if len(resp.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: response contained no choices")
	}

	choice := resp.Choices[0]

	if choice.FinishReason == "tool_calls" {
		calls := make([]ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			var params map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &params)
			calls[i] = ToolCall{
				ID:     tc.ID,
				Name:   tc.Function.Name,
				Params: params,
			}
		}
		return Response{ToolCalls: calls}, nil
	}

	// finish_reason == "stop" → final answer
	content := ""
	if choice.Message.Content != nil {
		content = *choice.Message.Content
	}
	return Response{Content: content, IsTerminal: true}, nil
}

func strPtr(s string) *string { return &s }
