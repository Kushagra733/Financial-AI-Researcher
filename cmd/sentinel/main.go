package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sentinel/sentinel-go/internal/agent"
	"github.com/sentinel/sentinel-go/internal/config"
	"github.com/sentinel/sentinel-go/internal/llm"
	"github.com/sentinel/sentinel-go/internal/orchestrator"
	"github.com/sentinel/sentinel-go/internal/safety"
	"github.com/sentinel/sentinel-go/internal/tools"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmsgprefix)

	// ── Config ─────────────────────────────────────────────────────────────
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// ── Graceful shutdown ──────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── LLM Provider ───────────────────────────────────────────────────────
	var provider llm.Provider
	switch cfg.LLMProvider {
	case "anthropic":
		provider = llm.NewAnthropicProvider(cfg.AnthropicKey)
	case "gemini":
		provider = llm.NewGeminiProvider(cfg.GeminiKey)
	default:
		provider = llm.NewOpenAIProvider(cfg.OpenAIKey)
	}
	log.Printf("LLM provider: %s | workers: %d", cfg.LLMProvider, cfg.MaxWorkers)

	// ── Safety guard ───────────────────────────────────────────────────────
	guard := safety.NewTransactionGuard()
	guard.ThresholdINR = cfg.TransactionThresholdINR

	// ── Tool registry ──────────────────────────────────────────────────────
	// Research tools (read-only, no approval needed)
	researchRegistry := tools.NewRegistry()
	researchRegistry.Register(tools.NewFetchStockQuote())
	researchRegistry.Register(tools.NewFetchMarketNews())
	researchRegistry.Register(tools.NewFetchUPIBalance())

	// Execution tools (write operations, guarded by TransactionGuard)
	executionRegistry := tools.NewRegistry()
	executionRegistry.Register(tools.NewPlaceUPITransaction(guard))

	// ── Orchestrator ───────────────────────────────────────────────────────
	orch := orchestrator.New(cfg.MaxWorkers)

	orch.RegisterFactory(agent.TaskTypeResearch, func() agent.Agent {
		return agent.NewResearchAgent(newID("research"), provider, researchRegistry)
	})
	orch.RegisterFactory(agent.TaskTypeVerification, func() agent.Agent {
		return agent.NewVerificationAgent(newID("verify"), provider)
	})
	orch.RegisterFactory(agent.TaskTypeExecution, func() agent.Agent {
		return agent.NewExecutionAgent(newID("execute"), provider, executionRegistry)
	})

	// ══════════════════════════════════════════════════════════════════════
	// Stage 1 — Research
	// ══════════════════════════════════════════════════════════════════════
	printStage("1", "RESEARCH", "Fetching live data and generating analysis")

	researchResults, err := orch.Run(ctx, []agent.Task{
		{
			ID:   "research-reliance",
			Type: agent.TaskTypeResearch,
			Description: `Research Reliance Industries listed on NSE (symbol: RELIANCE.NS).

Steps to follow:
1. Fetch the current stock quote for RELIANCE.NS.
2. Fetch recent news headlines for "Reliance Industries".
3. Based on the live data, produce a structured research report with:
   - Current price, change %, 52-week range
   - Key news summary (sentiment: bullish / bearish / neutral)
   - Technical assessment (price vs 52-week range)
   - Final recommendation: BUY / SELL / HOLD with a one-paragraph justification`,
			Deadline: time.Now().Add(3 * time.Minute),
		},
	})
	if err != nil {
		log.Printf("research stage error: %v", err)
	}

	if len(researchResults) == 0 || researchResults[0].Err != nil {
		errMsg := "no results"
		if len(researchResults) > 0 {
			errMsg = researchResults[0].Err.Error()
		}
		log.Fatalf("research failed: %s", errMsg)
	}

	researchOutput := researchResults[0].Output
	printResult("Research", researchResults[0])

	// ══════════════════════════════════════════════════════════════════════
	// Stage 2 — Verification
	// ══════════════════════════════════════════════════════════════════════
	printStage("2", "VERIFICATION", "Critically reviewing the research report")

	verifyResults, err := orch.Run(ctx, []agent.Task{
		{
			ID:   "verify-reliance",
			Type: agent.TaskTypeVerification,
			Description: fmt.Sprintf(
				"Review and verify the following research report. Provide your structured JSON verdict.\n\n---\n%s\n---",
				researchOutput,
			),
			Deadline: time.Now().Add(2 * time.Minute),
		},
	})
	if err != nil {
		log.Printf("verification stage error: %v", err)
	}

	if len(verifyResults) == 0 || verifyResults[0].Err != nil {
		log.Printf("verification failed — skipping execution")
	} else {
		verifyOutput := verifyResults[0].Output
		printResult("Verification", verifyResults[0])

		// ══════════════════════════════════════════════════════════════════
		// Stage 3 — Execution (only if verification approved)
		// ══════════════════════════════════════════════════════════════════
		if strings.Contains(verifyOutput, "APPROVED") {
			printStage("3", "EXECUTION", "Placing a small test transaction (INR 100 — auto-approved)")

			execResults, err := orch.Run(ctx, []agent.Task{
				{
					ID:   "exec-test-payment",
					Type: agent.TaskTypeExecution,
					Description: `Place a UPI payment with these exact details:
- from_upi_id: user@oksbi
- to_upi_id:   broker@icici
- amount_inr:  100
- note:        "Research subscription fee — Sentinel-Go"

Confirm the transaction and report the result.`,
					Deadline: time.Now().Add(2 * time.Minute),
				},
			})
			if err != nil {
				log.Printf("execution stage error: %v", err)
			}
			if len(execResults) > 0 {
				printResult("Execution", execResults[0])
			}
		} else {
			printStage("3", "EXECUTION", "Skipped — verification did not return APPROVED")
		}
	}

	fmt.Println("\n── Sentinel-Go pipeline complete ─────────────────────────")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func printStage(num, name, desc string) {
	fmt.Printf("\n╔══ Stage %s: %s ══════════════════════════════════════════\n", num, name)
	fmt.Printf("║  %s\n", desc)
	fmt.Println("╚═══════════════════════════════════════════════════════════")
}

func printResult(label string, r agent.Result) {
	status := "OK"
	if r.Err != nil {
		status = "ERROR: " + r.Err.Error()
	}
	fmt.Printf("\n[%s Result] agent=%s | duration=%s | status=%s\n",
		label, r.AgentID, r.Duration.Round(time.Millisecond), status)
	if r.Output != "" {
		fmt.Println("─────────────────────────────────────────────────────────")
		fmt.Println(r.Output)
		fmt.Println("─────────────────────────────────────────────────────────")
	}
}

func newID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}
