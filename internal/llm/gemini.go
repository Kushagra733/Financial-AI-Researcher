package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s"

// GeminiProvider implements Provider using the Google Gemini API
// with native function calling (functionCall / functionResponse parts).
// Safe for concurrent use.
type GeminiProvider struct {
	APIKey string
	Model  string
	client *http.Client
}

func NewGeminiProvider(apiKey string) *GeminiProvider {
	return &GeminiProvider{
		APIKey: apiKey,
		Model:  "gemini-2.5-flash",
		client: &http.Client{Timeout: 90 * time.Second},
	}
}

func (p *GeminiProvider) SetModel(m string) { p.Model = m }
func (p *GeminiProvider) GetModel() string  { return p.Model }

// GenerateResponse satisfies the legacy LLMProvider interface in pkg/types.
func (p *GeminiProvider) GenerateResponse(prompt string, _ map[string]interface{}) (string, error) {
	return fmt.Sprintf("Gemini response to: %s (using %s)", prompt, p.Model), nil
}

// CompleteWithTools sends a generateContent request to the Gemini API and
// returns either function calls the model wants to invoke, or the final text.
func (p *GeminiProvider) CompleteWithTools(ctx context.Context, req Request) (Response, error) {
	if p.APIKey == "" {
		return Response{}, fmt.Errorf("gemini: GEMINI_API_KEY is not configured")
	}

	body := geminiRequest{
		SystemInstruction: &geminiSystemInstruction{
			Parts: []geminiPart{{Text: req.System}},
		},
		Contents: toGeminiContents(req.Messages),
		GenerationConfig: geminiGenerationConfig{
			Temperature: 0.2,
		},
	}
	if len(req.Tools) > 0 {
		body.Tools = []geminiToolSet{{FunctionDeclarations: toGeminiFunctions(req.Tools)}}
		body.ToolConfig = &geminiToolConfig{
			FunctionCallingConfig: geminiCallingConfig{Mode: "AUTO"},
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := fmt.Sprintf(geminiEndpoint, p.Model, p.APIKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("gemini: build http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("gemini: http: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("gemini: read body: %w", err)
	}

	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return Response{}, fmt.Errorf("gemini: unmarshal response: %w", err)
	}
	if gr.Error != nil {
		return Response{}, fmt.Errorf("gemini API error [%d]: %s", gr.Error.Code, gr.Error.Message)
	}

	return fromGeminiResponse(gr)
}

// ---------------------------------------------------------------------------
// Gemini wire types
// ---------------------------------------------------------------------------

type geminiRequest struct {
	SystemInstruction *geminiSystemInstruction `json:"system_instruction,omitempty"`
	Contents          []geminiContent          `json:"contents"`
	Tools             []geminiToolSet          `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig        `json:"tool_config,omitempty"`
	GenerationConfig  geminiGenerationConfig   `json:"generation_config"`
}

type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

type geminiContent struct {
	Role  string       `json:"role"` // "user" | "model"
	Parts []geminiPart `json:"parts"`
}

// geminiPart is a union type — only one field is set per instance.
type geminiPart struct {
	Text             string                `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall   `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResponse   `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFuncResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolSet struct {
	FunctionDeclarations []geminiFunction `json:"function_declarations"`
}

type geminiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiCallingConfig `json:"function_calling_config"`
}

type geminiCallingConfig struct {
	Mode string `json:"mode"` // "AUTO" | "ANY" | "NONE"
}

type geminiGenerationConfig struct {
	Temperature float64 `json:"temperature"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ---------------------------------------------------------------------------
// Format converters: internal ↔ Gemini wire
// ---------------------------------------------------------------------------

// toGeminiContents converts our internal Message slice to Gemini's contents array.
//
// Key differences from OpenAI/Anthropic:
//   - Gemini uses "model" (not "assistant") for the model role.
//   - Tool call results travel as role="user" parts with functionResponse
//     (similar to Anthropic, not OpenAI's separate "tool" role).
//   - Gemini has no tool call IDs — the function name is the correlation key.
func toGeminiContents(msgs []Message) []geminiContent {
	var out []geminiContent

	i := 0
	for i < len(msgs) {
		m := msgs[i]

		switch m.Role {
		case "user":
			out = append(out, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})
			i++

		case "assistant":
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: tc.Params,
					},
				})
			}
			out = append(out, geminiContent{Role: "model", Parts: parts})
			i++

		case "tool":
			// Collect consecutive tool results into a single role="user" turn.
			var parts []geminiPart
			for i < len(msgs) && msgs[i].Role == "tool" {
				parts = append(parts, geminiPart{
					FunctionResponse: &geminiFuncResponse{
						Name:     msgs[i].Name,
						Response: map[string]any{"result": msgs[i].Content},
					},
				})
				i++
			}
			out = append(out, geminiContent{Role: "user", Parts: parts})

		default:
			i++
		}
	}

	return out
}

func toGeminiFunctions(schemas []ToolSchema) []geminiFunction {
	fns := make([]geminiFunction, len(schemas))
	for i, s := range schemas {
		fns[i] = geminiFunction{
			Name:        s.Name,
			Description: s.Description,
			Parameters:  s.Parameters,
		}
	}
	return fns
}

// fromGeminiResponse parses a Gemini API response into our internal Response.
// Gemini signals tool calls via functionCall parts (not via finishReason like OpenAI).
func fromGeminiResponse(resp geminiResponse) (Response, error) {
	if len(resp.Candidates) == 0 {
		return Response{}, fmt.Errorf("gemini: response contained no candidates")
	}

	parts := resp.Candidates[0].Content.Parts

	var toolCalls []ToolCall
	var textParts []string

	for _, p := range parts {
		if p.FunctionCall != nil {
			toolCalls = append(toolCalls, ToolCall{
				// Gemini has no call ID — use function name so the agent can
				// echo it back in the functionResponse correlation field.
				ID:     p.FunctionCall.Name,
				Name:   p.FunctionCall.Name,
				Params: p.FunctionCall.Args,
			})
		}
		if p.Text != "" {
			textParts = append(textParts, p.Text)
		}
	}

	if len(toolCalls) > 0 {
		return Response{ToolCalls: toolCalls}, nil
	}

	return Response{Content: strings.Join(textParts, ""), IsTerminal: true}, nil
}
