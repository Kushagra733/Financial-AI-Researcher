package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/sentinel/sentinel-go/internal/llm"
)

// FetchMarketNews retrieves recent financial news for a stock or company
// using the Yahoo Finance search endpoint (no API key required).
type FetchMarketNews struct {
	client *http.Client
}

func NewFetchMarketNews() *FetchMarketNews {
	return &FetchMarketNews{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *FetchMarketNews) Schema() llm.ToolSchema {
	return llm.ToolSchema{
		Name:        "FetchMarketNews",
		Description: "Fetches up to 5 recent financial news headlines for a given stock ticker or company name. Use this to understand recent sentiment and events affecting a stock.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Stock symbol or company name. Examples: 'RELIANCE.NS', 'Tata Motors', 'HDFC Bank'",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (f *FetchMarketNews) Execute(ctx context.Context, params map[string]any) (string, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("FetchMarketNews: missing or invalid 'query' parameter")
	}

	endpoint := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v1/finance/search?q=%s&newsCount=5&enableFuzzyQuery=false&enableCb=false",
		url.QueryEscape(query),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("FetchMarketNews: build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SentinelGo/1.0)")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("FetchMarketNews: http: %w", err)
	}
	defer resp.Body.Close()

	var data yahooSearchResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("FetchMarketNews: decode: %w", err)
	}

	type article struct {
		Title       string `json:"title"`
		Publisher   string `json:"publisher"`
		Link        string `json:"link"`
		PublishedAt string `json:"published_at"`
	}

	articles := make([]article, 0, len(data.News))
	for _, n := range data.News {
		articles = append(articles, article{
			Title:       n.Title,
			Publisher:   n.Publisher,
			Link:        n.Link,
			PublishedAt: time.Unix(n.ProviderPublishTime, 0).UTC().Format(time.RFC3339),
		})
	}

	out, _ := json.Marshal(map[string]any{
		"query":    query,
		"count":    len(articles),
		"articles": articles,
	})
	return string(out), nil
}

type yahooSearchResp struct {
	News []struct {
		Title               string `json:"title"`
		Publisher           string `json:"publisher"`
		Link                string `json:"link"`
		ProviderPublishTime int64  `json:"providerPublishTime"`
	} `json:"news"`
}
