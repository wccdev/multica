package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client is a minimal hand-written REST client for the Gitea API. There is
// no official Gitea Go SDK dependency in this module yet; a small
// hand-rolled client is consistent with how the Lark/Slack integrations
// already talk to their respective APIs over plain net/http.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient builds a client authenticated with a Personal/Bot Access Token
// against a Gitea instance at baseURL (e.g. "https://gitea.internal.example.com").
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, httpClient: httpClient}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gitea: encode request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v1"+path, reader)
	if err != nil {
		return fmt.Errorf("gitea: build request: %w", err)
	}
	// Gitea's classic PAT auth scheme: `Authorization: token <PAT>`.
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gitea: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("gitea: %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("gitea: decode response: %w", err)
	}
	return nil
}

// AuthenticatedUser is the subset of GET /user we care about.
type AuthenticatedUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

// GetAuthenticatedUser validates the token live and returns the account it
// belongs to — used both as a liveness check before persisting the token and
// to populate the connection's display info.
func (c *Client) GetAuthenticatedUser(ctx context.Context) (AuthenticatedUser, error) {
	var out AuthenticatedUser
	if err := c.do(ctx, http.MethodGet, "/user", nil, &out); err != nil {
		return AuthenticatedUser{}, err
	}
	if out.Login == "" {
		return AuthenticatedUser{}, fmt.Errorf("gitea: /user response missing login")
	}
	return out, nil
}

type webhookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Secret      string `json:"secret"`
}

type createWebhookRequest struct {
	Type   string        `json:"type"`
	Config webhookConfig `json:"config"`
	Events []string      `json:"events"`
	Active bool          `json:"active"`
}

type webhookResponse struct {
	ID int64 `json:"id"`
}

// CreateWebhook registers a repo-scoped webhook subscribed to pull_request
// events, pointed at targetURL and signed with secret using Gitea's
// X-Gitea-Signature scheme (raw hex HMAC-SHA256 of the body, no "sha256="
// prefix — unlike GitHub's X-Hub-Signature-256).
func (c *Client) CreateWebhook(ctx context.Context, owner, repo, targetURL, secret string) (int64, error) {
	var out webhookResponse
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/hooks", url.PathEscape(owner), url.PathEscape(repo)),
		createWebhookRequest{
			Type:   "gitea",
			Config: webhookConfig{URL: targetURL, ContentType: "json", Secret: secret},
			Events: []string{"pull_request"},
			Active: true,
		},
		&out,
	)
	if err != nil {
		return 0, err
	}
	return out.ID, nil
}

// DeleteWebhook removes a previously created webhook. A 404 (already gone on
// the Gitea side) is treated as success — the caller's intent ("this hook
// should not exist") is already satisfied.
func (c *Client) DeleteWebhook(ctx context.Context, owner, repo string, hookID int64) error {
	err := c.do(ctx, http.MethodDelete,
		fmt.Sprintf("/repos/%s/%s/hooks/%d", url.PathEscape(owner), url.PathEscape(repo), hookID),
		nil, nil,
	)
	if err != nil && strings.Contains(err.Error(), "returned 404:") {
		return nil
	}
	return err
}
