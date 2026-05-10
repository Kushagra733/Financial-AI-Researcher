package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sentinel/sentinel-go/internal/llm"
)

// FetchStockQuote retrieves live market data for an NSE/BSE listed stock
// using the Yahoo Finance chart API (no API key required).
//
// Symbol conventions:
//
//	NSE → append .NS   e.g. RELIANCE.NS, TCS.NS, INFY.NS
//	BSE → append .BO   e.g. RELIANCE.BO
type FetchStockQuote struct {
	client *http.Client
}

func NewFetchStockQuote() *FetchStockQuote {
	return &FetchStockQuote{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *FetchStockQuote) Schema() llm.ToolSchema {
	return llm.ToolSchema{
		Name:        "FetchStockQuote",
		Description: "Fetches the current market price, change %, volume, 52-week high/low, and market cap for a stock listed on NSE or BSE India. Use suffix .NS for NSE (e.g. RELIANCE.NS) and .BO for BSE.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{
					"type":        "string",
					"description": "Stock ticker with exchange suffix. Examples: RELIANCE.NS, TCS.NS, INFY.NS, HDFCBANK.NS",
				},
			},
			"required": []string{"symbol"},
		},
	}
}

func (f *FetchStockQuote) Execute(ctx context.Context, params map[string]any) (string, error) {
	symbol, ok := params["symbol"].(string)
	if !ok || symbol == "" {
		return "", fmt.Errorf("FetchStockQuote: missing or invalid 'symbol' parameter")
	}

	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1d&range=1d",
		symbol,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("FetchStockQuote: build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SentinelGo/1.0; +https://github.com/sentinel/sentinel-go)")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("FetchStockQuote: http: %w", err)
	}
	defer resp.Body.Close()

	var data yahooChartResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("FetchStockQuote: decode: %w", err)
	}
	if data.Chart.Err != nil {
		return "", fmt.Errorf("FetchStockQuote: Yahoo Finance: %s", data.Chart.Err.Description)
	}
	if len(data.Chart.Result) == 0 {
		return "", fmt.Errorf("FetchStockQuote: no data returned for symbol %q", symbol)
	}

	m := data.Chart.Result[0].Meta
	change := m.RegularMarketPrice - m.ChartPreviousClose
	changePct := 0.0
	if m.ChartPreviousClose != 0 {
		changePct = (change / m.ChartPreviousClose) * 100
	}

	out, _ := json.Marshal(map[string]any{
		"symbol":              m.Symbol,
		"name":                m.LongName,
		"exchange":            m.FullExchangeName,
		"currency":            m.Currency,
		"price":               m.RegularMarketPrice,
		"previous_close":      m.ChartPreviousClose,
		"change":              roundTo(change, 2),
		"change_percent":      roundTo(changePct, 2),
		"volume":              m.RegularMarketVolume,
		"market_cap":          m.MarketCap,
		"52_week_high":        m.FiftyTwoWeekHigh,
		"52_week_low":         m.FiftyTwoWeekLow,
		"fetched_at":          time.Now().UTC().Format(time.RFC3339),
	})
	return string(out), nil
}

// Yahoo Finance /v8/finance/chart response types

type yahooChartResp struct {
	Chart struct {
		Result []struct {
			Meta yahooMeta `json:"meta"`
		} `json:"result"`
		Err *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

type yahooMeta struct {
	Symbol              string  `json:"symbol"`
	LongName            string  `json:"longName"`
	Currency            string  `json:"currency"`
	FullExchangeName    string  `json:"fullExchangeName"`
	RegularMarketPrice  float64 `json:"regularMarketPrice"`
	ChartPreviousClose  float64 `json:"chartPreviousClose"`
	RegularMarketVolume int64   `json:"regularMarketVolume"`
	MarketCap           float64 `json:"marketCap"`
	FiftyTwoWeekHigh    float64 `json:"fiftyTwoWeekHigh"`
	FiftyTwoWeekLow     float64 `json:"fiftyTwoWeekLow"`
}

func roundTo(v float64, decimals int) float64 {
	p := 1.0
	for range decimals {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}
