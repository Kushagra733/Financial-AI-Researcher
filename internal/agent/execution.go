package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sentinel/sentinel-go/internal/llm"
	"github.com/sentinel/sentinel-go/internal/tools"
)

const executionSystemPrompt = `You are the execution agent for Sentinel, a financial automation platform.
You receive a verified instruction and carry it out using available tools.

Rules — follow them strictly:
- Execute exactly what is specified. Do not infer or expand the scope.
- Use PlaceUPITransaction only for explicit payment instructions.
- After a tool call, report the outcome clearly.
- If a tool returns an error, report it and do NOT retry automatically.

When done, provide a concise execution summary.`

// ExecutionAgent takes a verified instruction and executes it using tools.
// It supports the same multi-step ReAct loop as ResearchAgent so it can
// handle complex executions requiring multiple tool calls.
// State path: Idle → Executing → Done | Failed
type ExecutionAgent struct {
	id       string
	sm       *StateMachine
	provider llm.Provider
	registry *tools.Registry
}

func NewExecutionAgent(id string, provider llm.Provider, registry *tools.Registry) *ExecutionAgent {
	return &ExecutionAgent{
		id:       id,
		sm:       NewStateMachine(StateIdle),
		provider: provider,
		registry: registry,
	}
}

func (a *ExecutionAgent) ID() string          { return a.id }
func (a *ExecutionAgent) CurrentState() State { return a.sm.Current() }

func (a *ExecutionAgent) Run(ctx context.Context, task Task) (Result, error) {
	start := time.Now()

	if err := a.sm.Transition(StateExecuting); err != nil {
		return Result{}, fmt.Errorf("agent %s: %w", a.id, err)
	}

	messages := []llm.Message{
		{Role: "user", Content: task.Description},
	}

	var finalAnswer string

	for step := range maxReActSteps {
		select {
		case <-ctx.Done():
			_ = a.sm.Transition(StateFailed)
			return Result{AgentID: a.id, TaskID: task.ID, Err: ctx.Err(), Duration: time.Since(start)}, ctx.Err()
		default:
		}

		resp, err := a.provider.CompleteWithTools(ctx, llm.Request{
			System:   executionSystemPrompt,
			Messages: messages,
			Tools:    a.registry.Schemas(),
		})
		if err != nil {
			_ = a.sm.Transition(StateFailed)
			return Result{}, fmt.Errorf("agent %s step %d: llm: %w", a.id, step, err)
		}

		if resp.IsTerminal {
			finalAnswer = resp.Content
			break
		}

		messages = append(messages, llm.Message{
			Role:      "assistant",
			ToolCalls: resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			log.Printf("[%s] executing %q with params=%v", a.id, tc.Name, tc.Params)

			result, toolErr := a.registry.Execute(ctx, tc)
			if toolErr != nil {
				result = fmt.Sprintf("TOOL ERROR from %s: %v", tc.Name, toolErr)
				log.Printf("[%s] tool %q failed: %v", a.id, tc.Name, toolErr)
			} else {
				log.Printf("[%s] tool %q succeeded: %s", a.id, tc.Name, result)
			}

			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    result,
			})
		}
	}

	if finalAnswer == "" {
		finalAnswer = "execution agent reached max steps without completing"
		_ = a.sm.Transition(StateFailed)
	} else {
		_ = a.sm.Transition(StateDone)
	}

	return Result{
		AgentID:  a.id,
		TaskID:   task.ID,
		Output:   finalAnswer,
		Duration: time.Since(start),
	}, nil
}
