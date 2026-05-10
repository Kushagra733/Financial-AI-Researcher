package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sentinel/sentinel-go/internal/llm"
	"github.com/sentinel/sentinel-go/internal/tools"
)

const (
	// maxReActSteps caps the reasoning loop to bound LLM API costs.
	maxReActSteps = 10

	researchSystemPrompt = `You are Sentinel, an autonomous financial research agent specialising in Indian markets.

You have access to tools. Use them to gather real data before drawing conclusions.
Never fabricate prices, volumes, or news — rely solely on tool outputs.

Think step-by-step:
1. Identify what data you need.
2. Call the appropriate tool(s).
3. Analyse the returned data.
4. Produce a clear, structured research report with a recommendation.

Always end with a concise recommendation: BUY / SELL / HOLD with reasoning.`
)

// ResearchAgent implements Agent using a ReAct (Reasoning + Acting) loop.
//
// On each step it calls the LLM with the full conversation history and
// available tool schemas. If the LLM returns tool calls, it executes them,
// appends the observations, and loops. When the LLM signals IsTerminal it
// returns the final answer.
//
// Concurrency: ResearchAgent instances are single-use. The Orchestrator
// creates a fresh instance per task via a registered factory.
type ResearchAgent struct {
	id       string
	sm       *StateMachine
	provider llm.Provider
	registry *tools.Registry
}

func NewResearchAgent(id string, provider llm.Provider, registry *tools.Registry) *ResearchAgent {
	return &ResearchAgent{
		id:       id,
		sm:       NewStateMachine(StateIdle),
		provider: provider,
		registry: registry,
	}
}

func (a *ResearchAgent) ID() string          { return a.id }
func (a *ResearchAgent) CurrentState() State { return a.sm.Current() }

func (a *ResearchAgent) Run(ctx context.Context, task Task) (Result, error) {
	start := time.Now()

	if err := a.sm.Transition(StateResearching); err != nil {
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
			System:   researchSystemPrompt,
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

		// Append the assistant's tool-call intent to history.
		messages = append(messages, llm.Message{
			Role:      "assistant",
			ToolCalls: resp.ToolCalls,
		})

		// Execute every requested tool and append results.
		for _, tc := range resp.ToolCalls {
			log.Printf("[%s] → tool %q params=%v", a.id, tc.Name, tc.Params)

			result, toolErr := a.registry.Execute(ctx, tc)
			if toolErr != nil {
				result = fmt.Sprintf("error from %s: %v", tc.Name, toolErr)
				log.Printf("[%s] tool %q error: %v", a.id, tc.Name, toolErr)
			} else {
				log.Printf("[%s] ← tool %q: %s", a.id, tc.Name, result)
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
		finalAnswer = "research agent reached max reasoning steps without a conclusive answer"
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
