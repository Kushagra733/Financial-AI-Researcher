package agent

import (
	"context"
	"time"
)

// TaskType identifies which kind of agent should handle a Task.
// The Orchestrator uses this to pick the right factory.
type TaskType string

const (
	TaskTypeResearch     TaskType = "research"     // gather data, produce analysis
	TaskTypeVerification TaskType = "verification" // validate research output
	TaskTypeExecution    TaskType = "execution"    // carry out a transaction
)

// State represents the current lifecycle phase of an agent.
type State uint8

const (
	StateIdle        State = iota // Waiting for a task
	StateResearching              // Gathering data via tool calls
	StateVerifying                // Validating gathered findings
	StateExecuting                // Taking a consequential action
	StateDone                     // Task completed successfully
	StateFailed                   // Terminal failure
)

func (s State) String() string {
	names := [...]string{"idle", "researching", "verifying", "executing", "done", "failed"}
	if int(s) >= len(names) {
		return "unknown"
	}
	return names[s]
}

// Task is the unit of work dispatched to an Agent.
type Task struct {
	ID          string
	Type        TaskType
	Description string
	Payload     map[string]any
	Deadline    time.Time
}

// Result carries the outcome of a completed (or failed) Task.
type Result struct {
	AgentID  string
	TaskID   string
	Output   string
	Err      error
	Duration time.Duration
}

// Agent is the core capability contract every agent must satisfy.
type Agent interface {
	// ID returns a unique identifier for this agent instance.
	ID() string

	// Run executes a task, blocking until done or the context is cancelled.
	// Implementations must honour ctx cancellation at every step boundary.
	Run(ctx context.Context, task Task) (Result, error)

	// CurrentState exposes the agent's state for monitoring.
	CurrentState() State
}
