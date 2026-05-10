package tools

import (
	"context"
	"fmt"
	"sync"

	"github.com/sentinel/sentinel-go/internal/llm"
)

// Executor is the interface every callable tool must implement.
// Implementations must be safe for concurrent use.
type Executor interface {
	// Schema returns the JSON schema definition sent to the LLM so it knows
	// how to invoke this tool. Shape follows the OpenAI function-calling spec.
	Schema() llm.ToolSchema

	// Execute runs the tool with the given parameters and returns a
	// human-readable (or JSON) string that is appended to the agent's
	// observation history.
	Execute(ctx context.Context, params map[string]any) (string, error)
}

// Registry is a thread-safe map from tool name → Executor.
// Tools are registered once at startup and then read-only during agent execution.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Executor
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Executor)}
}

// Register adds a tool. Panics on duplicate names — same discipline as
// http.HandleFunc, so wiring errors surface at startup rather than silently
// shadowing the first registration.
func (r *Registry) Register(t Executor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Schema().Name
	if _, dup := r.tools[name]; dup {
		panic(fmt.Sprintf("tools: duplicate registration for %q", name))
	}
	r.tools[name] = t
}

// Schemas returns the JSON schema for every registered tool.
// Called once per ReAct step to populate the LLM request.
func (r *Registry) Schemas() []llm.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Schema())
	}
	return out
}

// Execute looks up the tool named by call.Name and invokes it.
// Returns an error if the tool does not exist or if execution fails.
func (r *Registry) Execute(ctx context.Context, call llm.ToolCall) (string, error) {
	r.mu.RLock()
	t, ok := r.tools[call.Name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("tools: unknown tool %q", call.Name)
	}
	return t.Execute(ctx, call.Params)
}
