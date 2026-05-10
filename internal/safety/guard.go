package safety

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

const defaultThresholdINR = 500.0

// TransactionGuard enforces the human-in-the-loop requirement for any
// financial transaction exceeding the configured INR threshold.
//
// Design: the blocking stdin read runs in a separate goroutine so that
// context cancellation (e.g. a global shutdown signal) is always respected —
// the main goroutine selects between the answer channel and ctx.Done()
// and never blocks forever.
type TransactionGuard struct {
	ThresholdINR float64
	scanner      *bufio.Scanner
}

func NewTransactionGuard() *TransactionGuard {
	return &TransactionGuard{
		ThresholdINR: defaultThresholdINR,
		scanner:      bufio.NewScanner(os.Stdin),
	}
}

// RequiresApproval returns true when amount exceeds the safety threshold.
func (g *TransactionGuard) RequiresApproval(amountINR float64) bool {
	return amountINR > g.ThresholdINR
}

// RequestApproval blocks until the operator types "yes" / "y" or until ctx
// is cancelled. Returns nil only on explicit approval; any other outcome
// (rejection, EOF, cancellation) returns a descriptive non-nil error.
//
// Call site pattern (inside an Executing-state agent):
//
//	if err := guard.RequestApproval(ctx, "Sell 10 HDFC shares", 8500.0); err != nil {
//	    return Result{Err: err}, err   // abort the transaction
//	}
//	// proceed with execution
func (g *TransactionGuard) RequestApproval(ctx context.Context, description string, amountINR float64) error {
	if !g.RequiresApproval(amountINR) {
		return nil
	}

	fmt.Printf("\n[!] HUMAN APPROVAL REQUIRED\n")
	fmt.Printf("    Transaction : %s\n", description)
	fmt.Printf("    Amount      : INR %.2f (threshold: INR %.2f)\n", amountINR, g.ThresholdINR)
	fmt.Printf("    Approve? [yes/no]: ")

	type answer struct {
		approved bool
		err      error
	}

	ch := make(chan answer, 1)
	go func() {
		if g.scanner.Scan() {
			text := strings.TrimSpace(strings.ToLower(g.scanner.Text()))
			ch <- answer{approved: text == "yes" || text == "y"}
		} else {
			ch <- answer{err: fmt.Errorf("stdin closed before approval was given")}
		}
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("approval cancelled: %w", ctx.Err())
	case a := <-ch:
		if a.err != nil {
			return fmt.Errorf("approval read error: %w", a.err)
		}
		if !a.approved {
			return fmt.Errorf("transaction rejected by operator: %q (INR %.2f)", description, amountINR)
		}
		fmt.Println("[+] Transaction approved by operator.")
		return nil
	}
}
