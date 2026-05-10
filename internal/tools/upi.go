package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/sentinel/sentinel-go/internal/llm"
)

// FetchUPIBalance retrieves the current UPI wallet balance for a given VPA
// (Virtual Payment Address / UPI ID).
//
// Phase 1: simulates a 50 ms network round-trip and returns synthetic data.
// Phase 2: replace executeStub with an authenticated call to the bank's
//
//	UPI PSP API (OAuth2 + HMAC-SHA256 request signing per NPCI spec).
type FetchUPIBalance struct{}

func NewFetchUPIBalance() *FetchUPIBalance { return &FetchUPIBalance{} }

func (f *FetchUPIBalance) Schema() llm.ToolSchema {
	return llm.ToolSchema{
		Name:        "FetchUPIBalance",
		Description: "Retrieves the current UPI wallet balance for a given UPI ID (VPA). Returns the balance in INR.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"upi_id": map[string]any{
					"type":        "string",
					"description": "The UPI Virtual Payment Address to query, e.g. user@upi or name@okicici",
				},
			},
			"required": []string{"upi_id"},
		},
	}
}

func (f *FetchUPIBalance) Execute(ctx context.Context, params map[string]any) (string, error) {
	upiID, ok := params["upi_id"].(string)
	if !ok || upiID == "" {
		return "", fmt.Errorf("FetchUPIBalance: missing or invalid 'upi_id' parameter")
	}

	// Simulate network latency; respect context cancellation so the agent's
	// deadline propagates all the way into the tool execution layer.
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("FetchUPIBalance: %w", ctx.Err())
	case <-time.After(50 * time.Millisecond):
	}

	// Phase 2: swap this with the real PSP API response.
	balance := 12_450.75

	return fmt.Sprintf(
		`{"upi_id":%q,"balance_inr":%.2f,"currency":"INR","fetched_at":%q}`,
		upiID, balance, time.Now().UTC().Format(time.RFC3339),
	), nil
}
