package gitea

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// InstallService owns the encrypted-at-rest PAT lifecycle for the Gitea
// integration: validating a pasted token, persisting the connection, and
// decrypting it back for API calls made on the workspace's behalf. Mirrors
// slack.InstallService's ownership of *secretbox.Box (see
// server/internal/integrations/slack/byo_install.go) — a Gitea PAT is a
// static secret that must be stored durably, unlike GitHub's ephemeral App
// JWTs, so the Slack BYO shape is the closer template here.
type InstallService struct {
	queries    *db.Queries
	box        *secretbox.Box
	baseURL    string
	httpClient *http.Client
}

// NewInstallService constructs the service bound to a single configured
// Gitea instance (GITEA_BASE_URL) and the secretbox used to seal tokens at
// rest (MULTICA_GITEA_SECRET_KEY).
func NewInstallService(queries *db.Queries, box *secretbox.Box, baseURL string, httpClient *http.Client) *InstallService {
	return &InstallService{queries: queries, box: box, baseURL: baseURL, httpClient: httpClient}
}

// BaseURL exposes the configured Gitea instance origin.
func (s *InstallService) BaseURL() string { return s.baseURL }

func (s *InstallService) newClient(token string) *Client {
	return NewClient(s.baseURL, token, s.httpClient)
}

// Connect validates the pasted PAT live against the configured Gitea
// instance and upserts the workspace's connection row with the token sealed
// at rest. One connection per workspace (UNIQUE workspace_id) — reconnecting
// replaces the stored token.
func (s *InstallService) Connect(ctx context.Context, workspaceID pgtype.UUID, token string, initiatorID pgtype.UUID) (db.GiteaConnection, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return db.GiteaConnection{}, fmt.Errorf("gitea: token is required")
	}
	user, err := s.newClient(token).GetAuthenticatedUser(ctx)
	if err != nil {
		return db.GiteaConnection{}, fmt.Errorf("gitea: token validation failed: %w", err)
	}
	sealed, err := s.box.Seal([]byte(token))
	if err != nil {
		return db.GiteaConnection{}, fmt.Errorf("gitea: encrypt token: %w", err)
	}
	var avatar pgtype.Text
	if user.AvatarURL != "" {
		avatar = pgtype.Text{String: user.AvatarURL, Valid: true}
	}
	return s.queries.UpsertGiteaConnection(ctx, db.UpsertGiteaConnectionParams{
		WorkspaceID:      workspaceID,
		AccountLogin:     user.Login,
		TokenEncrypted:   base64.StdEncoding.EncodeToString(sealed),
		AccountAvatarUrl: avatar,
		ConnectedByID:    initiatorID,
	})
}

// DecryptToken reverses the encryption Connect applied, tolerating PG's
// MIME-wrapped base64 output the same way the Slack integration's
// decryptToken does (server/internal/integrations/slack/config.go).
func (s *InstallService) DecryptToken(encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(stripWhitespace(encoded))
	if err != nil {
		return "", fmt.Errorf("gitea: base64 decode token: %w", err)
	}
	plaintext, err := s.box.Open(ciphertext)
	if err != nil {
		return "", fmt.Errorf("gitea: decrypt token: %w", err)
	}
	return string(plaintext), nil
}

// ClientFor builds an API client authenticated with the workspace's stored,
// decrypted token.
func (s *InstallService) ClientFor(conn db.GiteaConnection) (*Client, error) {
	token, err := s.DecryptToken(conn.TokenEncrypted)
	if err != nil {
		return nil, err
	}
	return s.newClient(token), nil
}

// Disconnect removes every tracked webhook (best-effort — one failing repo
// does not block the rest) and deletes the workspace's connection + webhook
// rows.
func (s *InstallService) Disconnect(ctx context.Context, workspaceID pgtype.UUID, conn db.GiteaConnection) error {
	if client, err := s.ClientFor(conn); err == nil {
		if rows, listErr := s.queries.ListGiteaWebhooksByWorkspace(ctx, workspaceID); listErr == nil {
			for _, row := range rows {
				_ = client.DeleteWebhook(ctx, row.RepoOwner, row.RepoName, row.HookID)
			}
		}
	}
	if _, err := s.queries.DeleteGiteaWebhooksByWorkspace(ctx, workspaceID); err != nil {
		return fmt.Errorf("gitea: delete webhook rows: %w", err)
	}
	if err := s.queries.DeleteGiteaConnection(ctx, workspaceID); err != nil {
		return fmt.Errorf("gitea: delete connection: %w", err)
	}
	return nil
}

// stripWhitespace removes ASCII whitespace so a MIME-wrapped base64 string
// (newlines every 64 chars, as PostgreSQL's encode(...,'base64') emits) and
// an unwrapped one decode identically.
func stripWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
