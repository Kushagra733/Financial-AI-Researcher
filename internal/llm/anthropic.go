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

const (
	anthropicEndpoint = "https://api.anthropic.com/v1/messages"
	anthropicVersion  = "2023-06-01"
)

// AnthropicProvider implements Provider using the Anthropic Messages API
// with native tool_use content blocks.
// Safe for concurrent use.
type AnthropicProvider struct {
	APIKey string
	Model  string
	client *http.Client
}

func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		APIKey: apiKey,
		Model:  "claude-opus-4-6",
		client: &http.Client{Timeout: 90 * time.Second},
	}
}

func (p *AnthropicProvider) SetModel(m string) { p.Model = m }
func (p *AnthropicProvider) GetModel() string  { return p.Model }

// GenerateResponse satisfies the legacy LLMProvider interface in pkg/types.
func (p *AnthropicProvider) GenerateResponse(prompt string, _ map[string]interface{}) (string, error) {
	return fmt.Sprintf("Claude response to: %s (using %s)", prompt, p.Model), nil
}

// CompleteWithTools sends a messages request to the Anthropic API and returns
// either tool_use blocks (when stop_reason="tool_use") or final text.
func (p *AnthropicProvider) CompleteWithTools(ctx context.Context, req Request) (Response, error) {
	if p.APIKey == "" {
		return Response{}, fmt.Errorf("anthropic: ANTHROPIC_API_KEY is not configured")
	}

	body := antRequest{
		Model:     p.Model,
		MaxTokens: 4096,
		System:    req.System,
		Messages:  toAntMessages(req.Messages),
	}
	if len(req.Tools) > 0 {
		body.Tools = toAntTools(req.Tools)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: build http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: http: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic: read body: %w", err)
	}

	var antResp antResponse
	if err := json.Unmarshal(raw, &antResp); err != nil {
		return Response{}, fmt.Errorf("anthropic: unmarshal response: %w", err)
	}
	if antResp.Error != nil {
		return Response{}, fmt.Errorf("anthropic API error [%s]: %s", antResp.Error.Type, antResp.Error.Message)
	}

	return fromAntResponse(antResp)
}

// ---------------------------------------------------------------------------
// Anthropic wire types
// ---------------------------------------------------------------------------

type antRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []antMessage `json:"messages"`
	Tools     []antTool    `json:"tools,omitempty"`
}

// antMessage.Content can be a plain string or a []antBlock (content array).
type antMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type antBlock struct {
	Type      string         `json:"type"`                 // "text"|"tool_use"|"tool_result"
	Text      string         `json:"text,omitempty"`       // type=text
	ID        string         `json:"id,omitempty"`         // type=tool_use
	Name      string         `json:"name,omitempty"`       // type=tool_use
	Input     map[string]any `json:"input,omitempty"`      // type=tool_use
	ToolUseID string         `json:"tool_use_id,omitempty"` // type=tool_result
	Content   string         `json:"content,omitempty"`    // type=tool_result
}

type antTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type antResponse struct {
	Content    []antBlock `json:"content"`
	StopReason string     `json:"stop_reason"` // "end_turn" | "tool_use"
	Error      *antError  `json:"error,omitempty"`
}

type antError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// Format converters: internal ↔ Anthropic wire
// ---------------------------------------------------------------------------

// toAntMessages converts internal Messages to Anthropic's format.
// Key difference from OpenAI: tool results travel as role="user" with a
// content array of tool_result blocks (not as a separate "tool" role).
func toAntMessages(msgs []Message) []antMessage {
	var out []antMessage

	i := 0
	for i < len(msgs) {
		m := msgs[i]

		switch m.Role {
		case "user":
			out = append(out, antMessage{Role: "user", Content: m.Content})
			i++

		case "assistant":
			if len(m.ToolCalls) > 0 {
				var blocks []antBlock
				if m.Content != "" {
					blocks = append(blocks, antBlock{Type: "text", Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, antBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Params,
					})
				}
				out = append(out, antMessage{Role: "assistant", Content: blocks})
			} else {
				out = append(out, antMessage{Role: "assistant", Content: m.Content})
			}
			i++

		case "tool":
			// Collect all consecutive tool result messages into a single
			// role="user" content array (Anthropic requirement).
			var blocks []antBlock
			for i < len(msgs) && msgs[i].Role == "tool" {
				blocks = append(blocks, antBlock{
					Type:      "tool_result",
					ToolUseID: msgs[i].ToolCallID,
					Content:   msgs[i].Content,
				})
				i++
			}
			out = append(out, antMessage{Role: "user", Content: blocks})
		default:
			i++
		}
	}

	return out
}

func toAntTools(schemas []ToolSchema) []antTool {
	tools := make([]antTool, len(schemas))
	for i, s := range schemas {
		tools[i] = antTool{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: s.Parameters,
		}
	}
	return tools
}

func fromAntResponse(resp antResponse) (Response, error) {
	if resp.StopReason == "tool_use" {
		var calls []ToolCall
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				calls = append(calls, ToolCall{
					ID:     b.ID,
					Name:   b.Name,
					Params: b.Input,
				})
			}
		}
		return Response{ToolCalls: calls}, nil
	}

	// stop_reason == "end_turn" → collect all text blocks
	var text string
	for _, b := range resp.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return Response{Content: text, IsTerminal: true}, nil
}
