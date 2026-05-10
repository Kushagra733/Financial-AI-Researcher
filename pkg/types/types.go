package types

import (
	"time"
)

// Agent represents the autonomous agent with LLM capabilities
type Agent struct {
	ID          string
	Name        string
	LLM         LLMProvider
	Safety      SafetyChecker
	Memory      MemoryStore
	Tools       []Tool
	MaxRetries  int
	Timeout     time.Duration
	Context     map[string]interface{}
}

// LLMProvider defines the interface for language model providers
type LLMProvider interface {
	GenerateResponse(prompt string, context map[string]interface{}) (string, error)
	SetModel(model string)
	GetModel() string
}

// SafetyChecker defines the interface for safety validation
type SafetyChecker interface {
	CheckPrompt(prompt string) (bool, string, error)
	CheckResponse(response string) (bool, string, error)
	AddRule(rule string)
}

// MemoryStore defines the interface for agent memory
type MemoryStore interface {
	Store(key string, value interface{}) error
	Retrieve(key string) (interface{}, error)
	Delete(key string) error
	GetHistory() []HistoryEntry
}

// HistoryEntry represents a single entry in agent history
type HistoryEntry struct {
	Timestamp   time.Time
	Action      string
	Input       string
	Output      string
	Status      string
}

// Tool represents a tool that the agent can use
type Tool interface {
	Name() string
	Description() string
	Execute(params map[string]interface{}) (interface{}, error)
}

// TaskRequest represents a request for the agent to complete
type TaskRequest struct {
	ID        string
	Task      string
	Priority  int
	Deadline  time.Time
	Context   map[string]interface{}
}

// TaskResult represents the result of a completed task
type TaskResult struct {
	TaskID    string
	Success   bool
	Result    interface{}
	Error     string
	Duration  time.Duration
	Timestamp time.Time
}
