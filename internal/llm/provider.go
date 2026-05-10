// Package llm defines the shared types and Provider interface used by all
// LLM backend implementations (OpenAI, Anthropic, …).
//
// Concrete implementations live in openai.go and anthropic.go.
// The agent layer imports only this file — it never knows which backend is running.
package llm

import "context"

// ToolSchema is the JSON Schema definition sent to the LLM so it knows the
// name, description, and expected parameters for each callable tool.
// Shape follows the OpenAI function-calling / Anthropic tool-use specification.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema "object"
}

// ToolCall represents a single tool invocation requested by the LLM.
// ID is assigned by the provider and must be echoed back in the tool result
// message so the LLM can correlate request → response.
type ToolCall struct {
	ID     string         // "call_abc123" (OpenAI) | "toolu_xxx" (Anthropic)
	Name   string         // Matches ToolSchema.Name
	Params map[string]any // Parsed parameter map
}

// Message is one conversational turn in our provider-agnostic internal format.
// Each implementation converts this to its own wire format.
//
//   role="user"      → human turn or tool result (ToolCallID set)
//   role="assistant" → LLM turn; ToolCalls non-empty when requesting tools
//   role="tool"      → tool result: ToolCallID + Name identify the originating call
type Message struct {
	Role       string     // "user" | "assistant" | "tool"
	Content    string     // text content of the turn
	ToolCalls  []ToolCall // non-empty for assistant messages that request tool calls
	ToolCallID string     // for role="tool": which ToolCall.ID this result answers
	Name       string     // for role="tool": the tool's name
}

// Request is the full input to one LLM completion step.
type Request struct {
	System   string       // system-level instructions
	Messages []Message    // conversation history, grows each ReAct step
	Tools    []ToolSchema // available tools this step (may be empty)
}

// Response is the structured output of one LLM completion.
// Exactly one of (ToolCalls) or (Content + IsTerminal=true) is populated.
type Response struct {
	Content    string     // final text answer — set when IsTerminal is true
	ToolCalls  []ToolCall // tool calls to execute — set when IsTerminal is false
	IsTerminal bool       // true when the LLM has finished reasoning
}

// Provider is the capability contract for LLM backends.
// All implementations must be safe for concurrent use.
type Provider interface {
	// CompleteWithTools runs one ReAct step: given a full conversation history
	// and tool schemas, returns the LLM's next action or final answer.
	CompleteWithTools(ctx context.Context, req Request) (Response, error)
}
