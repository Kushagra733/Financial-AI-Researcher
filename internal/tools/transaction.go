package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/sentinel/sentinel-go/internal/llm"
	"github.com/sentinel/sentinel-go/internal/safety"
)

// PlaceUPITransaction executes a UPI money transfer.
//
// Safety gate: the TransactionGuard blocks execution and prompts the operator
// for explicit approval when the amount exceeds the configured INR threshold
// (default ₹500). This implements the Human-in-the-loop requirement.
//
// Phase 1: stub execution (simulates success after approval).
// Phase 2: Replace the stub with an authenticated call to the bank's UPI PSP
//
//	API (OAuth2 bearer token + HMAC-SHA256 per NPCI spec).
type PlaceUPITransaction struct {
	guard *safety.TransactionGuard
}

func NewPlaceUPITransaction(guard *safety.TransactionGuard) *PlaceUPITransaction {
	return &PlaceUPITransaction{guard: guard}
}

func (f *PlaceUPITransaction) Schema() llm.ToolSchema {
	return llm.ToolSchema{
		Name: "PlaceUPITransaction",
		Description: "Executes a UPI payment from one VPA to another. " +
			"Transactions above INR 500 require explicit human operator approval before execution. " +
			"Use only when the user has explicitly requested a payment.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from_upi_id": map[string]any{
					"type":        "string",
					"description": "Sender UPI VPA, e.g. user@oksbi",
				},
				"to_upi_id": map[string]any{
					"type":        "string",
					"description": "Recipient UPI VPA, e.g. merchant@icici",
				},
				"amount_inr": map[string]any{
					"type":        "number",
					"description": "Amount in INR (must be > 0)",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Optional transaction note shown on the recipient's statement",
				},
			},
			"required": []string{"from_upi_id", "to_upi_id", "amount_inr"},
		},
	}
}

func (f *PlaceUPITransaction) Execute(ctx context.Context, params map[string]any) (string, error) {
	fromID, _ := params["from_upi_id"].(string)
	toID, _ := params["to_upi_id"].(string)
	amountINR, _ := params["amount_inr"].(float64)
	note, _ := params["note"].(string)

	if fromID == "" || toID == "" {
		return "", fmt.Errorf("PlaceUPITransaction: from_upi_id and to_upi_id are required")
	}
	if amountINR <= 0 {
		return "", fmt.Errorf("PlaceUPITransaction: amount_inr must be greater than 0")
	}

	desc := fmt.Sprintf("Transfer INR %.2f from %s to %s", amountINR, fromID, toID)
	if note != "" {
		desc += " — " + note
	}

	// Human-in-the-loop gate: blocks until operator types "yes" or ctx cancels.
	if err := f.guard.RequestApproval(ctx, desc, amountINR); err != nil {
		return "", fmt.Errorf("PlaceUPITransaction: %w", err)
	}

	// Phase 2: authenticated NPCI UPI API call goes here.
	txnID := fmt.Sprintf("TXN%d", time.Now().UnixMicro())

	return fmt.Sprintf(
		`{"txn_id":%q,"status":"SUCCESS","from":%q,"to":%q,"amount_inr":%.2f,"note":%q,"timestamp":%q}`,
		txnID, fromID, toID, amountINR, note, time.Now().UTC().Format(time.RFC3339),
	), nil
}
