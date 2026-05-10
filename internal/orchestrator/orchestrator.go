// Package orchestrator fans tasks out to agents concurrently using a
// factory-based worker pool.
//
// Design decisions:
//
//   - Factory per TaskType: each task gets a *fresh* agent instance created
//     by the registered factory. This avoids the single-use StateMachine
//     problem (agents cannot be reset) and keeps the Orchestrator stateless.
//
//   - Fan-out with semaphore: every task spawns a goroutine immediately
//     (no head-of-line blocking), but a buffered semaphore channel caps
//     actual concurrent LLM calls at maxWorkers.
//
//   - WaitGroup → close → range drain: the collector goroutine closes
//     resultCh only after wg.Wait(), so the `for r := range resultCh`
//     in Run terminates naturally without select/nil-channel tricks.
package orchestrator

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/sentinel/sentinel-go/internal/agent"
)

// Orchestrator routes tasks to fresh agent instances created by registered factories.
type Orchestrator struct {
	mu         sync.RWMutex
	factories  map[agent.TaskType]func() agent.Agent
	maxWorkers int
}

// New creates an Orchestrator. maxWorkers is clamped to 1 if ≤ 0.
func New(maxWorkers int) *Orchestrator {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	return &Orchestrator{
		factories:  make(map[agent.TaskType]func() agent.Agent),
		maxWorkers: maxWorkers,
	}
}

// RegisterFactory associates a factory function with a TaskType.
// Call this before Run. Safe to call from multiple goroutines.
func (o *Orchestrator) RegisterFactory(taskType agent.TaskType, factory func() agent.Agent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.factories[taskType] = factory
}

// Run fans all tasks out concurrently, collects every result, and returns
// when all tasks finish or the context is cancelled.
//
// A partial result slice is returned alongside ctx.Err() on cancellation,
// so callers can inspect what completed before the deadline.
func (o *Orchestrator) Run(ctx context.Context, tasks []agent.Task) ([]agent.Result, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	// Counting semaphore: up to maxWorkers goroutines hold a token at once.
	sem := make(chan struct{}, o.maxWorkers)
	resultCh := make(chan agent.Result, len(tasks))

	var wg sync.WaitGroup
	wg.Add(len(tasks))

	for _, task := range tasks {
		t := task // per-iteration capture

		go func() {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				resultCh <- agent.Result{TaskID: t.ID, Err: ctx.Err()}
				return
			}

			o.mu.RLock()
			factory, ok := o.factories[t.Type]
			o.mu.RUnlock()

			if !ok {
				resultCh <- agent.Result{
					TaskID: t.ID,
					Err:    fmt.Errorf("orchestrator: no factory registered for task type %q", t.Type),
				}
				return
			}

			a := factory()
			log.Printf("[orchestrator] task %q (type=%s) → agent %q", t.ID, t.Type, a.ID())

			result, err := a.Run(ctx, t)
			if err != nil {
				result.Err = fmt.Errorf("agent %s: %w", a.ID(), err)
			}
			resultCh <- result
		}()
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	results := make([]agent.Result, 0, len(tasks))
	for r := range resultCh {
		results = append(results, r)
	}

	return results, ctx.Err()
}
