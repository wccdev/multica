// Package giteavcsbackfill migrates this fork's retired, per-workspace Gitea
// integration (server/migrations/9000_gitea_integration) into upstream's
// native multi-provider VCS integration (server/migrations/216_vcs_integration)
// BEFORE migration 9001 relabels project_resource rows. cmd/migrate registers
// this as a preMigrationHook keyed to "9001_gitea_retirement_backfill", so it
// runs exactly once per database, before that migration's SQL applies.
//
// Why a Go hook and not plain SQL: gitea_connection.token_encrypted is
// secretbox ciphertext under MULTICA_GITEA_SECRET_KEY, but vcs_connection
// requires access_token_encrypted (and webhook_secret_encrypted) sealed under
// the different MULTICA_VCS_SECRET_KEY. Moving a token between the two schemes
// means decrypt-then-reencrypt, which SQL cannot do — it has to happen in Go,
// with both keys loaded.
//
// Webhook secrets do NOT carry over like-for-like: the Gitea integration used
// one shared static secret from the GITEA_WEBHOOK_SECRET env var for every
// repo webhook, while VCS generates and stores one random secret per
// connection. This hook mints a fresh random secret per migrated connection
// and logs a warning, because the webhook already registered on the Gitea
// side will keep sending the OLD shared secret — an operator must open
// Settings -> Integrations -> Git providers after upgrading and re-point each
// migrated repo's webhook at the new URL and secret shown there. PR mirrors
// and issue links already recorded keep working immediately; only NEW webhook
// deliveries need the manual re-point.
//
// Idempotent and safe to retry: every insert uses ON CONFLICT DO NOTHING
// against the same unique constraints VCS's own connect flow relies on, so a
// partial run (or a workspace that already reconnected manually through the
// new VCS tab before this hook ran) is picked up by the follow-up SELECT
// rather than duplicated or clobbered. The source gitea_* tables are never
// modified or dropped — they stay as a historical safety net.
package giteavcsbackfill

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

// webhookSecretBytes matches the byte length VCS's own connect flow uses when
// minting a webhook secret for a new connection.
const webhookSecretBytes = 32

// Result reports what a single Hook run did. Exposed so cmd/migrate and tests
// can log or assert on it.
type Result struct {
	// Skipped is non-empty when the hook did no work (no legacy gitea_connection
	// rows found), and explains why.
	Skipped              string
	ConnectionsMigrated  int
	PullRequestsMigrated int
	IssueLinksMigrated   int
}

// HookOptions controls Hook behaviour. Zero values are correct for
// production; tests override fields as needed.
type HookOptions struct {
	// Logger receives slog records about the migration. nil = slog.Default().
	Logger *slog.Logger
}

type giteaConnRow struct {
	id            pgtype.UUID
	workspaceID   pgtype.UUID
	accountLogin  string
	tokenEnc      string
	connectedByID pgtype.UUID
}

type giteaPRRow struct {
	id, workspaceID                          pgtype.UUID
	repoOwner, repoName, title, state, html  string
	prNumber                                 int32
	branch, authorLogin, authorAvatarURL     pgtype.Text
	mergedAt, closedAt, createdAt, updatedAt pgtype.Timestamptz
}

type giteaLinkRow struct {
	issueID, pullRequestID     pgtype.UUID
	closeIntent, referenceOnly bool
	linkedByType               pgtype.Text
	linkedByID                 pgtype.UUID
	linkedAt                   pgtype.Timestamptz
}

// Hook is the migration-time entrypoint invoked before migration
// 9001_gitea_retirement_backfill's SQL. It returns an error only when it
// cannot safely proceed (a SQL failure, or legacy data exists but a required
// secret key is unavailable) — that aborts the migrate run before
// schema_migrations records 9001, so the same version retries cleanly on the
// next invocation once the operator fixes the environment.
func Hook(ctx context.Context, pool *pgxpool.Pool, opts HookOptions) (Result, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	var res Result

	var giteaCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM gitea_connection").Scan(&giteaCount); err != nil {
		return res, fmt.Errorf("count gitea_connection: %w", err)
	}
	if giteaCount == 0 {
		res.Skipped = "no gitea_connection rows"
		log.Info("gitea->vcs backfill: no legacy Gitea connections found, nothing to migrate")
		return res, nil
	}

	giteaKey, err := secretbox.LoadKey("MULTICA_GITEA_SECRET_KEY")
	if err != nil {
		return res, fmt.Errorf("gitea->vcs backfill: %d legacy gitea_connection row(s) exist but MULTICA_GITEA_SECRET_KEY is unavailable to decrypt them (%w); keep it set until this one-time migration completes", giteaCount, err)
	}
	giteaBox, err := secretbox.New(giteaKey)
	if err != nil {
		return res, fmt.Errorf("gitea->vcs backfill: MULTICA_GITEA_SECRET_KEY: %w", err)
	}
	vcsKey, err := secretbox.LoadKey("MULTICA_VCS_SECRET_KEY")
	if err != nil {
		return res, fmt.Errorf("gitea->vcs backfill: %d legacy gitea_connection row(s) exist but MULTICA_VCS_SECRET_KEY is unavailable to re-encrypt them (%w); set it (and MULTICA_VCS_INTEGRATION_ENABLED=true) before upgrading", giteaCount, err)
	}
	vcsBox, err := secretbox.New(vcsKey)
	if err != nil {
		return res, fmt.Errorf("gitea->vcs backfill: MULTICA_VCS_SECRET_KEY: %w", err)
	}
	instanceURL := strings.TrimSpace(os.Getenv("GITEA_BASE_URL"))
	if instanceURL == "" {
		return res, fmt.Errorf("gitea->vcs backfill: %d legacy gitea_connection row(s) exist but GITEA_BASE_URL is unset; it is required to populate vcs_connection.instance_url", giteaCount)
	}

	connByWorkspace, err := migrateConnections(ctx, pool, log, giteaBox, vcsBox, instanceURL, &res)
	if err != nil {
		return res, err
	}
	prIDMap, err := migratePullRequests(ctx, pool, log, connByWorkspace, &res)
	if err != nil {
		return res, err
	}
	if err := migrateIssueLinks(ctx, pool, log, prIDMap, &res); err != nil {
		return res, err
	}

	log.Info("gitea->vcs backfill: complete",
		"connections_migrated", res.ConnectionsMigrated,
		"pull_requests_migrated", res.PullRequestsMigrated,
		"issue_links_migrated", res.IssueLinksMigrated)
	return res, nil
}

// migrateConnections re-keys every gitea_connection row into vcs_connection
// and returns a map from workspace UUID string to the resulting
// vcs_connection id, used by migratePullRequests to attach PRs to the right
// connection.
func migrateConnections(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, giteaBox, vcsBox *secretbox.Box, instanceURL string, res *Result) (map[string]pgtype.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT id, workspace_id, account_login, token_encrypted, connected_by_id FROM gitea_connection`)
	if err != nil {
		return nil, fmt.Errorf("list gitea_connection: %w", err)
	}
	var conns []giteaConnRow
	for rows.Next() {
		var c giteaConnRow
		if err := rows.Scan(&c.id, &c.workspaceID, &c.accountLogin, &c.tokenEnc, &c.connectedByID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan gitea_connection: %w", err)
		}
		conns = append(conns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate gitea_connection: %w", err)
	}

	connByWorkspace := make(map[string]pgtype.UUID, len(conns))
	for _, c := range conns {
		wsKey := util.UUIDToString(c.workspaceID)

		plaintext, err := decryptBase64(giteaBox, c.tokenEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt gitea token for workspace %s: %w", wsKey, err)
		}
		tokenEnc, err := encryptToBase64(vcsBox, plaintext)
		if err != nil {
			return nil, fmt.Errorf("re-encrypt token for workspace %s: %w", wsKey, err)
		}
		webhookPlain := make([]byte, webhookSecretBytes)
		if _, err := rand.Read(webhookPlain); err != nil {
			return nil, fmt.Errorf("generate webhook secret for workspace %s: %w", wsKey, err)
		}
		webhookEnc, err := encryptToBase64(vcsBox, webhookPlain)
		if err != nil {
			return nil, fmt.Errorf("encrypt webhook secret for workspace %s: %w", wsKey, err)
		}

		var vcsID pgtype.UUID
		insertErr := pool.QueryRow(ctx, `
			INSERT INTO vcs_connection (workspace_id, provider, instance_url, account_login, access_token_encrypted, webhook_secret_encrypted, connected_by_id)
			VALUES ($1, 'gitea', $2, $3, $4, $5, $6)
			ON CONFLICT (workspace_id, instance_url) DO NOTHING
			RETURNING id
		`, c.workspaceID, instanceURL, c.accountLogin, tokenEnc, webhookEnc, c.connectedByID).Scan(&vcsID)
		switch {
		case insertErr == pgx.ErrNoRows:
			// Already migrated on a prior run, or the workspace already
			// reconnected manually via the new VCS tab with the same
			// instance URL — attach to that row instead of clobbering it.
			if err := pool.QueryRow(ctx, `SELECT id FROM vcs_connection WHERE workspace_id = $1 AND instance_url = $2`, c.workspaceID, instanceURL).Scan(&vcsID); err != nil {
				return nil, fmt.Errorf("look up existing vcs_connection for workspace %s: %w", wsKey, err)
			}
		case insertErr != nil:
			return nil, fmt.Errorf("insert vcs_connection for workspace %s: %w", wsKey, insertErr)
		default:
			res.ConnectionsMigrated++
			log.Warn("gitea->vcs backfill: migrated a Gitea connection; re-point its webhook on the Gitea side at the new URL/secret in Settings -> Integrations -> Git providers (the old shared GITEA_WEBHOOK_SECRET no longer verifies new VCS deliveries)",
				"workspace_id", wsKey)
		}
		connByWorkspace[wsKey] = vcsID
	}
	return connByWorkspace, nil
}

// migratePullRequests re-keys every gitea_pull_request row into
// vcs_pull_request and returns a map from the OLD gitea_pull_request UUID
// string to the NEW vcs_pull_request id, used by migrateIssueLinks.
func migratePullRequests(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, connByWorkspace map[string]pgtype.UUID, res *Result) (map[string]pgtype.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, workspace_id, repo_owner, repo_name, pr_number, title, state, html_url,
		       branch, author_login, author_avatar_url, merged_at, closed_at, pr_created_at, pr_updated_at
		FROM gitea_pull_request
	`)
	if err != nil {
		return nil, fmt.Errorf("list gitea_pull_request: %w", err)
	}
	var prs []giteaPRRow
	for rows.Next() {
		var p giteaPRRow
		if err := rows.Scan(&p.id, &p.workspaceID, &p.repoOwner, &p.repoName, &p.prNumber, &p.title, &p.state, &p.html,
			&p.branch, &p.authorLogin, &p.authorAvatarURL, &p.mergedAt, &p.closedAt, &p.createdAt, &p.updatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan gitea_pull_request: %w", err)
		}
		prs = append(prs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate gitea_pull_request: %w", err)
	}

	prIDMap := make(map[string]pgtype.UUID, len(prs))
	for _, p := range prs {
		wsKey := util.UUIDToString(p.workspaceID)
		connID, ok := connByWorkspace[wsKey]
		if !ok {
			log.Warn("gitea->vcs backfill: skipping pull request with no migrated connection",
				"workspace_id", wsKey, "repo", p.repoOwner+"/"+p.repoName, "pr_number", p.prNumber)
			continue
		}

		var newID pgtype.UUID
		insertErr := pool.QueryRow(ctx, `
			INSERT INTO vcs_pull_request (
				workspace_id, connection_id, provider, repo_owner, repo_name, pr_number,
				title, state, html_url, branch, author_login, author_avatar_url,
				merged_at, closed_at, pr_created_at, pr_updated_at
			) VALUES ($1, $2, 'gitea', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (connection_id, repo_owner, repo_name, pr_number) DO NOTHING
			RETURNING id
		`, p.workspaceID, connID, p.repoOwner, p.repoName, p.prNumber, p.title, p.state, p.html,
			p.branch, p.authorLogin, p.authorAvatarURL, p.mergedAt, p.closedAt, p.createdAt, p.updatedAt).Scan(&newID)
		switch {
		case insertErr == pgx.ErrNoRows:
			if err := pool.QueryRow(ctx, `
				SELECT id FROM vcs_pull_request WHERE connection_id = $1 AND repo_owner = $2 AND repo_name = $3 AND pr_number = $4
			`, connID, p.repoOwner, p.repoName, p.prNumber).Scan(&newID); err != nil {
				return nil, fmt.Errorf("look up existing vcs_pull_request %s/%s#%d: %w", p.repoOwner, p.repoName, p.prNumber, err)
			}
		case insertErr != nil:
			return nil, fmt.Errorf("insert vcs_pull_request %s/%s#%d: %w", p.repoOwner, p.repoName, p.prNumber, insertErr)
		default:
			res.PullRequestsMigrated++
		}
		prIDMap[util.UUIDToString(p.id)] = newID
	}
	return prIDMap, nil
}

// migrateIssueLinks re-keys every issue_gitea_pull_request row into
// issue_vcs_pull_request, pointing at the new vcs_pull_request id resolved by
// migratePullRequests.
func migrateIssueLinks(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, prIDMap map[string]pgtype.UUID, res *Result) error {
	rows, err := pool.Query(ctx, `
		SELECT issue_id, pull_request_id, close_intent, reference_only, linked_by_type, linked_by_id, linked_at
		FROM issue_gitea_pull_request
	`)
	if err != nil {
		return fmt.Errorf("list issue_gitea_pull_request: %w", err)
	}
	var links []giteaLinkRow
	for rows.Next() {
		var l giteaLinkRow
		if err := rows.Scan(&l.issueID, &l.pullRequestID, &l.closeIntent, &l.referenceOnly, &l.linkedByType, &l.linkedByID, &l.linkedAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan issue_gitea_pull_request: %w", err)
		}
		links = append(links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate issue_gitea_pull_request: %w", err)
	}

	for _, l := range links {
		newPRID, ok := prIDMap[util.UUIDToString(l.pullRequestID)]
		if !ok {
			log.Warn("gitea->vcs backfill: skipping issue link with no migrated pull request", "issue_id", util.UUIDToString(l.issueID))
			continue
		}
		tag, err := pool.Exec(ctx, `
			INSERT INTO issue_vcs_pull_request (issue_id, pull_request_id, close_intent, reference_only, linked_by_type, linked_by_id, linked_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (issue_id, pull_request_id) DO NOTHING
		`, l.issueID, newPRID, l.closeIntent, l.referenceOnly, l.linkedByType, l.linkedByID, l.linkedAt)
		if err != nil {
			return fmt.Errorf("insert issue_vcs_pull_request: %w", err)
		}
		res.IssueLinksMigrated += int(tag.RowsAffected())
	}
	return nil
}

func decryptBase64(box *secretbox.Box, encoded string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return box.Open(ciphertext)
}

func encryptToBase64(box *secretbox.Box, plaintext []byte) (string, error) {
	sealed, err := box.Seal(plaintext)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}
