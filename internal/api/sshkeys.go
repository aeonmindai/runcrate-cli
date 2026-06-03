package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

type SSHKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	CreatedAt string `json:"created_at"`
}

type AddSSHKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

func (c *Client) ListSSHKeys() ([]SSHKey, error) {
	resp, err := c.do("GET", "/api/v1/ssh-keys", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[[]SSHKey]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}

func (c *Client) AddSSHKey(req AddSSHKeyRequest) (*SSHKey, error) {
	body, _ := json.Marshal(req)
	resp, err := c.do("POST", "/api/v1/ssh-keys", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[SSHKey]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

func (c *Client) DeleteSSHKey(id string) error {
	resp, err := c.do("DELETE", "/api/v1/ssh-keys/"+id, nil)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return nil
	}
	return checkHTTPStatus(resp)
}
