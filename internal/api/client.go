package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var Version = "dev"

type Client struct {
	baseURL     string
	apiKey      string
	accessToken string
	projectID   string
	environment string
	httpClient  *http.Client
}

// APIError is a non-2xx response from the v1 API. Status is the HTTP status code;
// Code and Message come from the { error: { code, message } } envelope when present.
type APIError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	if e.Status == http.StatusPaymentRequired && strings.EqualFold(e.Code, "BUDGET_REACHED") {
		return "this environment's monthly budget is reached; an owner or admin can raise it in the dashboard (HTTP 402)"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	return fmt.Sprintf("API error (HTTP %d)", e.Status)
}

// IsBudgetReached reports whether err is the 402 BUDGET_REACHED returned when the
// environment has used its monthly budget.
func IsBudgetReached(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) &&
		apiErr.Status == http.StatusPaymentRequired &&
		strings.EqualFold(apiErr.Code, "BUDGET_REACHED")
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func NewOAuthClient(baseURL, accessToken, projectID, environment string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		accessToken: accessToken,
		projectID:   projectID,
		environment: environment,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *Client) do(method, path string, body io.Reader) (*http.Response, error) {
	return c.doCtx(context.Background(), method, path, body)
}

// doCtx is do with a request context, for calls that can be cancelled or need
// a deadline other than the client's default timeout.
func (c *Client) doCtx(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
		req.Header.Set("X-Project-Id", c.projectID)
		if c.environment != "" {
			req.Header.Set("X-Environment", c.environment)
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	req.Header.Set("User-Agent", "runcrate-cli/"+Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

func (c *Client) DoRaw(method, path string, body io.Reader) (*http.Response, error) {
	return c.do(method, path, body)
}

func (c *Client) ValidateKey() error {
	resp, err := c.do("GET", "/api/v1/billing/balance", nil)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("API error (HTTP %d)", resp.StatusCode)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid API key: %s", envelope.Error.Message)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("rate limited: %s", envelope.Error.Message)
	}

	return fmt.Errorf("API error (%d): %s", resp.StatusCode, envelope.Error.Message)
}

// ── OAuth token exchange ───────────────────────────────────────────────────

type OAuthWorkspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	IsDefault bool   `json:"is_default"`
}

// OAuthEnvironment is the one environment an agent token acts in.
type OAuthEnvironment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OAuthTokenResponse is what /api/auth/oauth/token answers. Since agent tokens
// (rca_...), the access token is opaque, lasts until ExpiresAt and has no refresh
// token; RefreshToken and the Supabase fields are only set by older servers.
type OAuthTokenResponse struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token"`
	ExpiresAt    int64             `json:"expires_at"`
	UserEmail    string            `json:"user_email"`
	UserName     string            `json:"user_name"`
	Workspaces   []OAuthWorkspace  `json:"workspaces"`
	Environment  *OAuthEnvironment `json:"environment"`
	SupabaseURL  string            `json:"supabase_url"`
	SupabaseAnon string            `json:"supabase_anon_key"`
}

func ExchangeCodeOAuth(baseURL, code string) (*OAuthTokenResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/auth/oauth/token"

	payload := fmt.Sprintf(`{"code":"%s"}`, code)
	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "runcrate-cli/"+Version)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("token exchange failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("token exchange failed (HTTP %d)", resp.StatusCode)
	}

	var token OAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if token.AccessToken == "" {
		return nil, fmt.Errorf("server returned empty access token")
	}

	return &token, nil
}
