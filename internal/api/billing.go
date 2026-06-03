package api

import (
	"encoding/json"
	"fmt"
)

type Balance struct {
	Credits      float64 `json:"credits"`
	Currency     string  `json:"currency"`
	AutoRecharge bool    `json:"auto_recharge_enabled"`
}

type UsageSummary struct {
	TotalSpent float64 `json:"total_spent"`
	Period     string  `json:"period"`
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

func (c *Client) GetUsage(period string) (json.RawMessage, error) {
	path := "/api/v1/billing/usage"
	if period != "" {
		path += "?period=" + period
	}
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}
