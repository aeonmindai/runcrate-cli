package api

import (
	"encoding/json"
	"fmt"
)

// Secret is one of GET /api/v1/secrets: a secret set in the environment the
// client acts in. The API returns names only; values are set in the dashboard
// and never sent back.
type Secret struct {
	Key       string `json:"key"`
	Sensitive bool   `json:"sensitive"`
	UpdatedAt string `json:"updated_at"`
}

// ListSecrets returns the secrets every machine launched in the client's
// environment gets, sorted by name.
func (c *Client) ListSecrets() ([]Secret, error) {
	resp, err := c.do("GET", "/api/v1/secrets", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[[]Secret]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}
