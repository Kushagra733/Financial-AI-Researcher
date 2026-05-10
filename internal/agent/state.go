package agent

import (
	"fmt"
	"sync"
)

// validTransitions encodes every permitted edge in the agent state graph.
//
//	Idle ──► Researching ──► Verifying ──► Executing ──► Done
//	  │            │               │               │
//	  ├──────────► │               │               │
//	  │            └───────────────┴───────────────┴──► Failed
//	  │
//	  ├──────────────────────────► Verifying  (VerificationAgent fast-path)
//	  └──────────────────────────────────────► Executing (ExecutionAgent fast-path)
//
// Done and Failed are terminal; they have no outgoing edges.
var validTransitions = map[State][]State{
	StateIdle:        {StateResearching, StateVerifying, StateExecuting},
	StateResearching: {StateVerifying, StateDone, StateFailed},
	StateVerifying:   {StateExecuting, StateResearching, StateDone, StateFailed},
	StateExecuting:   {StateDone, StateFailed},
}

// StateMachine is a thread-safe state machine for agent lifecycle management.
type StateMachine struct {
	mu      sync.RWMutex
	current State
}

func NewStateMachine(initial State) *StateMachine {
	return &StateMachine{current: initial}
}

func (sm *StateMachine) Current() State {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// Transition moves to next, returning an error if the transition is not in
// validTransitions. The machine stays in its current state on error.
func (sm *StateMachine) Transition(next State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	allowed, ok := validTransitions[sm.current]
	if !ok {
		return fmt.Errorf("state %q is terminal; no outgoing transitions", sm.current)
	}
	for _, s := range allowed {
		if s == next {
			sm.current = next
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %s → %s", sm.current, next)
}
