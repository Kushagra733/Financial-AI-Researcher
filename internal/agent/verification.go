package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sentinel/sentinel-go/internal/llm"
)

const verificationSystemPrompt = `You are a senior financial analyst and risk officer at Sentinel.
Your sole job is to critically verify research reports produced by junior agents.

When reviewing a research report, assess:
1. DATA QUALITY  — Is the data recent, from a reliable source, and internally consistent?
2. LOGIC         — Does the reasoning follow from the data? Are conclusions justified?
3. RISK FACTORS  — Are there material risks the report missed (macro, regulatory, liquidity)?
4. RECOMMENDATION — Is the buy/sell/hold call proportionate to the evidence presented?

Respond with a structured JSON verdict:
{
  "verdict":     "APPROVED" | "REJECTED",
  "confidence":  0.0-1.0,
  "summary":     "one-sentence assessment",
  "risk_notes":  "key risks or missing information",
  "reasoning":   "detailed justification for your verdict"
}`

// VerificationAgent reviews a research report and produces a structured verdict.
// It uses a single LLM call (no tools needed — pure reasoning over provided text).
// State path: Idle → Verifying → Done | Failed
type VerificationAgent struct {
	id       string
	sm       *StateMachine
	provider llm.Provider
}

func NewVerificationAgent(id string, provider llm.Provider) *VerificationAgent {
	return &VerificationAgent{
		id:       id,
		sm:       NewStateMachine(StateIdle),
		provider: provider,
	}
}

func (a *VerificationAgent) ID() string          { return a.id }
func (a *VerificationAgent) CurrentState() State { return a.sm.Current() }

func (a *VerificationAgent) Run(ctx context.Context, task Task) (Result, error) {
	start := time.Now()

	if err := a.sm.Transition(StateVerifying); err != nil {
		return Result{}, fmt.Errorf("agent %s: %w", a.id, err)
	}

	resp, err := a.provider.CompleteWithTools(ctx, llm.Request{
		System: verificationSystemPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: task.Description},
		},
		// No tools — verification is pure LLM reasoning over the report text.
	})
	if err != nil {
		_ = a.sm.Transition(StateFailed)
		return Result{}, fmt.Errorf("agent %s: llm: %w", a.id, err)
	}

	output := resp.Content
	if output == "" {
		output = "verification produced no output"
	}

	log.Printf("[%s] verdict: %s", a.id, output)
	_ = a.sm.Transition(StateDone)

	return Result{
		AgentID:  a.id,
		TaskID:   task.ID,
		Output:   output,
		Duration: time.Since(start),
	}, nil
}
