# Sentinel-Go — System Architecture

> Autonomous multi-agent engine for financial research and execution in India.
> Language: Go · LLM: Gemini / OpenAI / Anthropic · Pattern: ReAct loop

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [High-Level Architecture](#2-high-level-architecture)
3. [Component Breakdown](#3-component-breakdown)
4. [Pipeline: Request Lifecycle](#4-pipeline-request-lifecycle)
5. [Agent State Machine](#5-agent-state-machine)
6. [ReAct Loop (Reasoning + Acting)](#6-react-loop-reasoning--acting)
7. [Concurrency Model](#7-concurrency-model)
8. [LLM Provider Abstraction](#8-llm-provider-abstraction)
9. [Tool Registry](#9-tool-registry)
10. [Safety Layer](#10-safety-layer)
11. [Configuration System](#11-configuration-system)
12. [Directory Structure](#12-directory-structure)
13. [Data Flow Diagrams](#13-data-flow-diagrams)
14. [Phase Roadmap](#14-phase-roadmap)
15. [Key Design Decisions](#15-key-design-decisions)

---

## 1. System Overview

Sentinel-Go is a **3-stage autonomous pipeline** for Indian financial markets:

```
User Task
    │
    ▼
┌──────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  Stage 1     │────▶│  Stage 2         │────▶│  Stage 3         │
│  RESEARCH    │     │  VERIFICATION    │     │  EXECUTION       │
│              │     │                  │     │                  │
│ Fetch live   │     │ Critically review│     │ Execute trade /  │
│ market data  │     │ research output  │     │ payment with     │
│ + LLM analyse│     │ + risk check     │     │ human approval   │
└──────────────┘     └──────────────────┘     └──────────────────┘
      │                      │                        │
      ▼                      ▼                        ▼
  Research              APPROVED /               Transaction
   Report                REJECTED               Confirmation
```

**Core properties:**
- Zero external Go dependencies — pure standard library
- Every agent runs as an isolated goroutine
- Context cancellation propagates from orchestrator → agent → tool → HTTP call
- Human-in-the-loop gate for any financial transaction above ₹500
- Swap LLM providers (Gemini / OpenAI / Anthropic) via a single `.env` change

---

## 2. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          cmd/sentinel/main.go                           │
│                                                                         │
│   Config ──► Provider ──► Registries ──► Orchestrator ──► Run Pipeline │
└───────────────────────────────┬─────────────────────────────────────────┘
                                │
                    ┌───────────▼───────────┐
                    │      Orchestrator      │
                    │                       │
                    │  Factory Map          │
                    │  research  ──► func() │
                    │  verify    ──► func() │
                    │  execute   ──► func() │
                    │                       │
                    │  Semaphore (maxWorkers)│
                    │  WaitGroup + resultCh │
                    └──┬──────────┬─────────┘
                       │          │
          ┌────────────┘          └────────────┐
          ▼                                    ▼
  ┌───────────────┐                  ┌───────────────────┐
  │ ResearchAgent │                  │ VerificationAgent │
  │               │                  │                   │
  │ ReAct Loop    │                  │ Single LLM call   │
  │ (max 10 steps)│                  │ (pure reasoning)  │
  └───────┬───────┘                  └─────────┬─────────┘
          │                                    │
          ▼                                    ▼
  ┌───────────────┐              ┌─────────────────────────┐
  │ Tool Registry │              │     llm.Provider         │
  │               │              │                         │
  │ FetchStock    │              │  ┌─────────────────┐    │
  │ FetchNews     │              │  │ GeminiProvider  │    │
  │ FetchUPI      │              │  │ OpenAIProvider  │    │
  └───────────────┘              │  │AnthropicProvider│    │
                                 │  └─────────────────┘    │
                                 └─────────────────────────┘

  ┌───────────────────┐
  │  ExecutionAgent   │
  │                   │
  │  ReAct Loop       │
  │  + Safety Gate    │
  └────────┬──────────┘
           │
           ▼
  ┌────────────────────────┐
  │ Execution Tool Registry│
  │                        │
  │  PlaceUPITransaction   │
  │        │               │
  │        ▼               │
  │  TransactionGuard      │
  │  (Human-in-the-loop    │
  │   for amounts > ₹500)  │
  └────────────────────────┘
```

---

## 3. Component Breakdown

### 3.1 Orchestrator (`internal/orchestrator/`)

The central dispatcher. It owns no business logic — it only routes and coordinates.

| Responsibility | Mechanism |
|---|---|
| Route tasks to correct agent type | `map[TaskType]func() Agent` factory map |
| Bound concurrent LLM calls | Buffered semaphore channel `sem := make(chan struct{}, maxWorkers)` |
| Collect results from all goroutines | `resultCh` + `sync.WaitGroup` |
| Propagate cancellation | `context.Context` passed to every `agent.Run()` |
| Agent lifecycle isolation | Fresh agent instance created per task via factory |

**Why factory-based (not instance pool)?**
Agent `StateMachine` is single-use by design (terminal states have no outgoing edges). Factories guarantee each task gets a clean, idle agent without state reset logic.

---

### 3.2 Agent Layer (`internal/agent/`)

Three specialised agent types sharing the same `Agent` interface:

```go
type Agent interface {
    ID()           string
    Run(ctx, task) (Result, error)
    CurrentState() State
}
```

| Agent | State Path | Tools Used | LLM Calls |
|---|---|---|---|
| `ResearchAgent` | Idle → Researching → Done/Failed | FetchStockQuote, FetchMarketNews, FetchUPIBalance | Multi-step ReAct |
| `VerificationAgent` | Idle → Verifying → Done/Failed | None (pure reasoning) | Single shot |
| `ExecutionAgent` | Idle → Executing → Done/Failed | PlaceUPITransaction | Multi-step ReAct |

---

### 3.3 LLM Layer (`internal/llm/`)

```
provider.go          — shared types + Provider interface
openai.go            — OpenAI Chat Completions (gpt-4o)
anthropic.go         — Anthropic Messages API (claude-opus-4-6)
gemini.go            — Google Gemini API (gemini-2.5-flash)  ← active
```

**Shared wire types** (provider-agnostic):

```go
type Message  struct { Role, Content, ToolCalls, ToolCallID, Name }
type ToolCall struct { ID, Name, Params }
type Request  struct { System, Messages, Tools }
type Response struct { Content, ToolCalls, IsTerminal }
```

Each provider converts these to/from its own native format internally. The agent layer never knows which backend is running.

---

### 3.4 Tool Layer (`internal/tools/`)

```
tool.go          — Executor interface + Registry (thread-safe)
stock.go         — FetchStockQuote     (Yahoo Finance, no key needed)
news.go          — FetchMarketNews     (Yahoo Finance search)
upi.go           — FetchUPIBalance     (stub → Phase 2: NPCI PSP API)
transaction.go   — PlaceUPITransaction (stub + TransactionGuard)
```

All tools implement:
```go
type Executor interface {
    Schema()  llm.ToolSchema            // JSON Schema sent to LLM
    Execute(ctx, params) (string, error) // called by agent ReAct loop
}
```

---

### 3.5 Safety Layer (`internal/safety/`)

```
checker.go  — keyword-based prompt/response safety rules
guard.go    — TransactionGuard: human-in-the-loop for amounts > ₹500
```

`TransactionGuard.RequestApproval()` spawns a goroutine for the blocking stdin read and selects on both the answer channel and `ctx.Done()` — the process never hangs on a human approval timeout.

---

### 3.6 Config (`internal/config/`)

Single `config.Load(".env")` call at startup. Reads `.env`, sets env vars (existing shell vars win), then hydrates a typed `Config` struct. No third-party libraries needed.

```
GEMINI_API_KEY        → cfg.GeminiKey
OPENAI_API_KEY        → cfg.OpenAIKey
ANTHROPIC_API_KEY     → cfg.AnthropicKey
LLM_PROVIDER          → cfg.LLMProvider  ("gemini" | "openai" | "anthropic")
MAX_WORKERS           → cfg.MaxWorkers
TRANSACTION_THRESHOLD_INR → cfg.TransactionThresholdINR
```

---

## 4. Pipeline: Request Lifecycle

```
main.go
  │
  ├─ Stage 1: orch.Run(ctx, []Task{researchTask})
  │     │
  │     ├─ Orchestrator picks factory for TaskTypeResearch
  │     ├─ Creates fresh ResearchAgent
  │     ├─ Acquires semaphore slot
  │     └─ Calls agent.Run(ctx, task)
  │           │
  │           └─ ReAct Loop (up to 10 steps):
  │                 Step 0: LLM → "call FetchStockQuote"
  │                 Step 0: Tool → live price from Yahoo Finance
  │                 Step 1: LLM → "call FetchMarketNews"
  │                 Step 1: Tool → news headlines from Yahoo Finance
  │                 Step 2: LLM → final answer (IsTerminal=true)
  │                 └─ Return Result{Output: research_report}
  │
  ├─ Stage 2: orch.Run(ctx, []Task{verifyTask})
  │     │     (task.Description = research_report from Stage 1)
  │     │
  │     └─ VerificationAgent.Run(ctx, task)
  │           │
  │           └─ Single LLM call (no tools)
  │                 └─ Return Result{Output: {"verdict":"APPROVED"|"REJECTED",...}}
  │
  └─ Stage 3: orch.Run(ctx, []Task{execTask})   ← only if APPROVED
        │
        └─ ExecutionAgent.Run(ctx, task)
              │
              └─ ReAct Loop:
                    Step 0: LLM → "call PlaceUPITransaction"
                    Step 0: Tool → TransactionGuard check
                              ├─ amount ≤ ₹500 → auto-approved
                              └─ amount > ₹500 → block, prompt operator stdin
                    Step 1: LLM → final answer (execution summary)
```

---

## 5. Agent State Machine

```
                    ┌─────────┐
                    │  IDLE   │◄──── initial state (all agents)
                    └────┬────┘
                         │
           ┌─────────────┼──────────────────┐
           │             │                  │
           ▼             ▼                  ▼
    ┌────────────┐  ┌──────────┐  ┌──────────────┐
    │ RESEARCHING│  │VERIFYING │  │  EXECUTING   │
    │            │  │          │  │              │
    │ ResearchAgt│  │ VerifyAgt│  │  ExecAgent   │
    └──────┬─────┘  └────┬─────┘  └──────┬───────┘
           │             │               │
           │       ┌─────┘               │
           ▼       ▼                     ▼
        ┌──────────────┐          ┌──────────┐
        │   VERIFYING  │          │   DONE   │◄─── terminal
        │  (optional   │          └──────────┘
        │  transition) │
        └──────┬───────┘
               │
         ┌─────┴──────┐
         ▼            ▼
    ┌─────────┐  ┌──────────┐
    │EXECUTING│  │   DONE   │◄─── terminal
    └────┬────┘  └──────────┘
         │
    ┌────┴───┐
    ▼        ▼
┌──────┐ ┌────────┐
│ DONE │ │ FAILED │◄─── terminal (both states)
└──────┘ └────────┘
```

**Valid transitions:**

| From | To |
|---|---|
| Idle | Researching, Verifying, Executing |
| Researching | Verifying, Done, Failed |
| Verifying | Executing, Researching, Done, Failed |
| Executing | Done, Failed |
| Done | — (terminal) |
| Failed | — (terminal) |

All transitions are guarded by a `sync.RWMutex`. Invalid transitions return an error and leave the machine in its current state.

---

## 6. ReAct Loop (Reasoning + Acting)

Each iteration of the loop represents one LLM "think → act" cycle:

```
┌──────────────────────────────────────────────────────────────┐
│                      ReAct Loop (max 10 steps)               │
│                                                              │
│  messages = [{role:"user", content: task}]                   │
│                                                              │
│  for step in range(10):                                      │
│    ┌──────────────────────┐                                  │
│    │  ctx.Done() check    │ ◄── cancellation at every step   │
│    └──────────┬───────────┘                                  │
│               │                                              │
│    ┌──────────▼───────────┐                                  │
│    │  provider.Complete   │ ◄── full conversation + schemas  │
│    │  WithTools(messages) │                                  │
│    └──────────┬───────────┘                                  │
│               │                                              │
│         IsTerminal?                                          │
│          YES ──────────────────────────► return finalAnswer  │
│          NO                                                  │
│               │                                              │
│    ┌──────────▼───────────┐                                  │
│    │  for each ToolCall:  │                                  │
│    │    registry.Execute()│ ◄── real HTTP / stub             │
│    │    append to messages│                                  │
│    └──────────────────────┘                                  │
│    loop ◄──────────────────────────────────────────────────  │
└──────────────────────────────────────────────────────────────┘
```

**Conversation history grows on each step:**

```
Step 0:  [user: task]
           → LLM returns ToolCall{FetchStockQuote}
Step 1:  [user: task]
         [assistant: ToolCall{FetchStockQuote}]
         [tool: {"price": 1436, ...}]
           → LLM returns ToolCall{FetchMarketNews}
Step 2:  [... + assistant: ToolCall{FetchMarketNews}]
         [... + tool: {"articles": [...]}]
           → LLM returns IsTerminal=true, Content="HOLD recommendation..."
```

---

## 7. Concurrency Model

```
main goroutine
     │
     └─ orch.Run(ctx, tasks)
              │
              ├─ go task-1 ──► acquire sem ──► agent.Run() ──► resultCh
              ├─ go task-2 ──► acquire sem ──► agent.Run() ──► resultCh
              ├─ go task-3 ──► BLOCKED on sem (maxWorkers=4 full)
              └─ go collector: wg.Wait() → close(resultCh)

              main: for r := range resultCh { collect }
```

**Semaphore as counting gate:**

```
sem := make(chan struct{}, maxWorkers)

goroutine enters:   sem <- struct{}{}   // blocks if full
goroutine exits:    defer func(){ <-sem }()
```

This means:
- `len(tasks)` goroutines are **spawned immediately** (no head-of-line blocking)
- At most `maxWorkers` goroutines **execute** concurrently (API cost control)
- Cancellation is handled at the `select { case sem <- ...: / case <-ctx.Done(): }` branch

**No mutexes in the hot path.** The orchestrator uses `sync.RWMutex` only for the factory map (written once at startup, then read-only).

---

## 8. LLM Provider Abstraction

```
                     llm.Provider (interface)
                           │
          ┌────────────────┼────────────────┐
          │                │                │
          ▼                ▼                ▼
  GeminiProvider    OpenAIProvider   AnthropicProvider
  gemini-2.5-flash  gpt-4o           claude-opus-4-6
  v1beta API        Chat Completions  Messages API

  Function calling  Function calling  tool_use blocks
  via functionCall  via tool_calls    via tool_use
  parts             finish_reason     stop_reason
```

**Provider wire format differences (handled internally):**

| Concept | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| Model role name | `"assistant"` | `"assistant"` | `"model"` |
| Tool result role | `"tool"` | `"user"` (content array) | `"user"` (functionResponse) |
| Tool call detection | `finish_reason:"tool_calls"` | `stop_reason:"tool_use"` | `functionCall` part present |
| Tool call ID | UUID string | `toolu_xxx` | None (use function name) |
| System prompt | `role:"system"` message | `system` field | `system_instruction` field |

The agent layer uses only the internal `llm.Message` / `llm.Response` types and never sees these differences.

---

## 9. Tool Registry

```
Registry (thread-safe map[name]Executor)
│
├── Register(tool)          ← called at startup, panics on duplicate
├── Schemas() []ToolSchema  ← injected into every LLM request
└── Execute(ctx, ToolCall)  ← called by agent on each ReAct step

Research Registry:               Execution Registry:
  FetchStockQuote                  PlaceUPITransaction
  FetchMarketNews                    └── TransactionGuard
  FetchUPIBalance
```

**Tool JSON Schema** (sent to LLM so it knows how to call the tool):

```json
{
  "name": "FetchStockQuote",
  "description": "Fetches current market price for NSE/BSE stocks...",
  "parameters": {
    "type": "object",
    "properties": {
      "symbol": { "type": "string", "description": "e.g. RELIANCE.NS" }
    },
    "required": ["symbol"]
  }
}
```

**Adding a new tool — 3 steps:**

1. Create `internal/tools/mytool.go` implementing `Executor`
2. `registry.Register(tools.NewMyTool())` in `main.go`
3. Nothing else — the LLM discovers it automatically via the schema

---

## 10. Safety Layer

### 10.1 Keyword Safety Checker

```
SafetyRuleChecker
  ├── CheckPrompt(prompt) → (safe bool, reason string, err)
  └── CheckResponse(response) → (safe bool, reason string, err)

Default blocked keywords: malware, ransomware, illegal, exploit, hack
Custom rules: checker.AddRule("keyword")
```

### 10.2 Transaction Guard (Human-in-the-Loop)

```
PlaceUPITransaction.Execute()
         │
         ▼
TransactionGuard.RequestApproval(ctx, description, amountINR)
         │
    amountINR > threshold (₹500)?
         │
    NO ──┴──► auto-approved, continue
         │
    YES  ▼
    ┌─────────────────────────────┐
    │  Print to terminal:         │
    │  [!] HUMAN APPROVAL REQUIRED│
    │  Transaction: ...           │
    │  Amount: INR 850.00         │
    │  Approve? [yes/no]:         │
    └─────────────────────────────┘
         │
    goroutine reads stdin ──────► answer channel
         │                              │
    ctx.Done() ◄──── select ────────────┘
         │
    "yes" → proceed  |  "no" / timeout → abort with error
```

The stdin read runs in a separate goroutine so `ctx.Done()` is always honoured — the process never hangs indefinitely waiting for human input.

---

## 11. Configuration System

```
.env file (gitignored)        Shell environment
      │                              │
      ▼                              │
config.loadDotEnv()  ──────────────►│
  parse KEY=VALUE                    │
  os.Setenv(key, value)             │
  (skip if already set) ◄───────────┘
      │
      ▼
config.Load() → Config struct
  OpenAIKey, AnthropicKey, GeminiKey
  LLMProvider ("gemini" | "openai" | "anthropic")
  MaxWorkers, TransactionThresholdINR
  RedisURL, PostgresURL
  UPIClientID, UPIClientSecret, UPIMerchantID
```

**Priority:** shell env var > `.env` file > code default

---

## 12. Directory Structure

```
sentinel-go/
│
├── cmd/
│   └── sentinel/
│       └── main.go              ← entry point, pipeline wiring
│
├── internal/                    ← not importable outside this module
│   │
│   ├── agent/
│   │   ├── agent.go             ← Agent interface, Task, Result, TaskType
│   │   ├── state.go             ← StateMachine with enforced transitions
│   │   ├── research.go          ← ResearchAgent (ReAct loop)
│   │   ├── verification.go      ← VerificationAgent (single LLM call)
│   │   └── execution.go         ← ExecutionAgent (ReAct + safety gate)
│   │
│   ├── orchestrator/
│   │   └── orchestrator.go      ← factory-based, fan-out + semaphore
│   │
│   ├── llm/
│   │   ├── provider.go          ← shared types + Provider interface
│   │   ├── gemini.go            ← Google Gemini (gemini-2.5-flash)
│   │   ├── openai.go            ← OpenAI (gpt-4o)
│   │   └── anthropic.go         ← Anthropic (claude-opus-4-6)
│   │
│   ├── tools/
│   │   ├── tool.go              ← Executor interface + Registry
│   │   ├── stock.go             ← FetchStockQuote (Yahoo Finance)
│   │   ├── news.go              ← FetchMarketNews (Yahoo Finance)
│   │   ├── upi.go               ← FetchUPIBalance (stub → Phase 2)
│   │   └── transaction.go       ← PlaceUPITransaction + guard
│   │
│   ├── safety/
│   │   ├── checker.go           ← keyword-based safety rules
│   │   └── guard.go             ← TransactionGuard (human-in-the-loop)
│   │
│   ├── config/
│   │   └── config.go            ← .env loader + typed Config struct
│   │
│   └── storage/
│       └── memory.go            ← in-memory KV store (Phase 1)
│
├── pkg/                         ← importable by external packages
│   └── types/
│       └── types.go             ← shared interfaces (LLMProvider, Tool, etc.)
│
├── .env                         ← secrets (gitignored)
├── .env.example                 ← template (safe to commit)
├── .gitignore
└── go.mod                       ← zero external dependencies
```

---

## 13. Data Flow Diagrams

### 13.1 Research Stage

```
main.go
  │ Task{type:research, "Research RELIANCE.NS..."}
  ▼
Orchestrator
  │ factory() → ResearchAgent
  ▼
ResearchAgent.Run()
  │
  ├──[step 0]──► GeminiProvider.CompleteWithTools()
  │                   │ POST /v1beta/models/gemini-2.5-flash:generateContent
  │                   │ body: {messages, tools:[FetchStockQuote, FetchMarketNews]}
  │                   ▼
  │              Gemini API ──► Response{ToolCalls:[{FetchStockQuote, RELIANCE.NS}]}
  │
  ├──[tool]───► tools.Registry.Execute(FetchStockQuote, {symbol:RELIANCE.NS})
  │                   │
  │                   │ GET https://query1.finance.yahoo.com/v8/finance/chart/RELIANCE.NS
  │                   ▼
  │              Yahoo Finance ──► {price:1436, 52wk_high:1611, volume:8.6M, ...}
  │
  ├──[step 1]──► GeminiProvider.CompleteWithTools()
  │                   │ body: {messages + tool result, tools}
  │                   ▼
  │              Gemini API ──► Response{ToolCalls:[{FetchMarketNews, Reliance Industries}]}
  │
  ├──[tool]───► tools.Registry.Execute(FetchMarketNews, {query:Reliance Industries})
  │                   │
  │                   │ GET https://query2.finance.yahoo.com/v1/finance/search?q=...
  │                   ▼
  │              Yahoo Finance ──► {articles:[{title, publisher, link}, ...]}
  │
  └──[step 2]──► GeminiProvider.CompleteWithTools()
                      │ body: {messages + all tool results, tools}
                      ▼
                 Gemini API ──► Response{IsTerminal:true, Content:"## Research Report..."}
                      │
                      ▼
                 Result{Output: research_report}
```

### 13.2 Verification Stage

```
main.go
  │ Task{type:verification, description: research_report}
  ▼
Orchestrator → VerificationAgent.Run()
  │
  └──[single call]──► GeminiProvider.CompleteWithTools()
                           │ body: {system: verificationPrompt, messages: [report]}
                           │ tools: [] (none — pure reasoning)
                           ▼
                      Gemini API ──► Response{IsTerminal:true, Content:
                           '{"verdict":"REJECTED","confidence":0.95,...}'}
                           │
                           ▼
                      Result{Output: verdict_json}
```

### 13.3 Execution Stage (amount ≤ ₹500)

```
main.go
  │ Task{type:execution, "Transfer INR 100 from user@oksbi to broker@icici"}
  ▼
Orchestrator → ExecutionAgent.Run()
  │
  ├──[step 0]──► GeminiProvider.CompleteWithTools()
  │                   │ tools: [PlaceUPITransaction]
  │                   ▼
  │              Gemini API ──► Response{ToolCalls:[{PlaceUPITransaction, {amount:100,...}}]}
  │
  ├──[tool]───► PlaceUPITransaction.Execute({amount_inr:100, ...})
  │                   │
  │                   ├── TransactionGuard.RequestApproval(ctx, desc, 100)
  │                   │         100 ≤ 500 → auto-approved ✓
  │                   │
  │                   └── Phase 1 stub → TXN{id, status:SUCCESS, ...}
  │
  └──[step 1]──► GeminiProvider.CompleteWithTools()
                      ▼
                 Response{IsTerminal:true, Content:"Transaction TXN123 completed successfully"}
```

---

## 14. Phase Roadmap

### Phase 1 — Complete ✓
- [x] Multi-agent orchestrator (worker pool pattern)
- [x] Agent state machine (Idle → Researching → Verifying → Executing → Done)
- [x] ReAct loop with native LLM tool calling
- [x] Gemini / OpenAI / Anthropic providers (real HTTP)
- [x] Tools: FetchStockQuote, FetchMarketNews, FetchUPIBalance (Yahoo Finance)
- [x] Tool: PlaceUPITransaction (stub with human-in-the-loop guard)
- [x] 3-stage pipeline: Research → Verification → Execution
- [x] Config system (.env loader, typed struct)
- [x] Graceful shutdown (signal.NotifyContext)

### Phase 2 — Real API Integration
- [ ] **Real UPI API** — NPCI PSP integration (OAuth2 + HMAC-SHA256 signing)
- [ ] **Real news source** — Moneycontrol / ET Markets RSS or NewsAPI for India-specific headlines
- [ ] **OpenAI structured outputs** — `response_format: json_schema` for typed tool calls
- [ ] **Anthropic extended thinking** — chain-of-thought for verification agent
- [ ] **Retry + backoff** — exponential backoff on LLM rate limits (429 errors)
- [ ] **Tool result caching** — avoid re-fetching same ticker within a session

### Phase 3 — Persistence & Observability
- [ ] **Redis** — short-term agent state, session cache, tool result cache
- [ ] **PostgreSQL** — full audit trail (every task, result, tool call, transaction)
- [ ] **Structured logging** — JSON log lines with trace IDs (slog package)
- [ ] **Metrics** — Prometheus counters for tasks dispatched, tool calls, LLM latency
- [ ] **Distributed tracing** — OpenTelemetry spans across orchestrator → agent → tool

### Phase 4 — Production Hardening
- [ ] **Multi-node orchestrator** — Redis pub/sub task queue for horizontal scaling
- [ ] **Agent specialisation** — dedicated agents per asset class (equity, FD, mutual fund)
- [ ] **Portfolio agent** — tracks positions, P&L, rebalancing signals
- [ ] **Alert agent** — price triggers, news sentiment alerts via push notification
- [ ] **Web UI** — dashboard for pipeline status, agent logs, approval queue
- [ ] **SEBI compliance layer** — audit logs, KYC verification gate before execution

---

## 15. Key Design Decisions

### Why Go (not Python)?
- Native goroutine concurrency maps perfectly to multi-agent fan-out
- No GIL — true parallel execution of agent goroutines
- `context.Context` provides clean, zero-overhead cancellation propagation
- Single compiled binary — no runtime dependencies in production

### Why zero external dependencies?
- `net/http` handles all LLM and market data API calls
- `encoding/json` handles all serialisation
- No supply chain risk, no `go mod tidy` surprises
- Forces understanding of every line of code

### Why factory-based agents (not instance pool)?
- Agent `StateMachine` is intentionally single-use (terminal states are final)
- Factories guarantee a clean `StateIdle` start for every task
- No state reset logic needed — simplicity over memory efficiency

### Why fan-out with semaphore (not fixed goroutine pool)?
- Fan-out: tasks start immediately (zero queue latency for fast tasks)
- Semaphore: caps concurrent LLM API calls (cost/rate-limit control)
- Fixed pool: slow tasks would starve fast ones by holding a pool slot during I/O

### Why native tool calling (not custom JSON ReAct)?
- All three providers (Gemini, OpenAI, Anthropic) have first-class tool-calling APIs
- More reliable parsing — no prompt engineering to get valid JSON
- Automatic retry on tool call parsing failure by the provider
- Supports parallel tool calls (multiple tools in one step) natively

### Why separate Research and Execution tool registries?
- Principle of least privilege — research agents cannot place transactions
- Execution agents only have access to write-capable tools
- Clear audit boundary — every transaction goes through `ExecutionAgent` + `TransactionGuard`
