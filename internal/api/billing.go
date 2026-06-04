package api

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// Balance mirrors /api/v1/billing/balance (getProjectBalance), which returns
// camelCase creditsBalance + activeUsageCost.
type Balance struct {
	Credits         float64 `json:"creditsBalance"`
	ActiveUsageCost float64 `json:"activeUsageCost"`
}

// UsageSummary mirrors the get_usage_summary RPC behind /api/v1/billing/usage.
// This is inference/token usage; compute charges live in billing transactions.
type UsageSummary struct {
	TotalRequests         int     `json:"total_requests"`
	TotalPromptTokens     int     `json:"total_prompt_tokens"`
	TotalCompletionTokens int     `json:"total_completion_tokens"`
	TotalTokens           int     `json:"total_tokens"`
	TotalCost             float64 `json:"total_cost"`
}

func (c *Client) GetBalance() (*Balance, error) {
	resp, err := c.do("GET", "/api/v1/billing/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Balance]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// GetUsage returns the inference usage summary. `from` is an optional RFC3339
// lower bound (the server filters on from/to, not a "period" string).
func (c *Client) GetUsage(from string) (*UsageSummary, error) {
	path := "/api/v1/billing/usage"
	if from != "" {
		path += "?from=" + url.QueryEscape(from)
	}
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[UsageSummary]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}
