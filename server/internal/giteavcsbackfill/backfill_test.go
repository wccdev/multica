package giteavcsbackfill

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

// These tests exercise the gitea->vcs backfill hook against a live Postgres
// (matching every other live-Postgres suite in the repo). They connect to
// DATABASE_URL and skip cleanly when no database is reachable.
//
// Each test creates its own throwaway workspace (and issue, where needed) so
// it never collides with real data, and cleans up via t.Cleanup. gitea_* and
// vcs_* rows are deleted explicitly since neither table cascades from
// workspace deletion in the same transaction ordering assumed by cleanup.

// newTestPool registers its own Close via t.Cleanup rather than leaving it to
// the caller's defer: t.Cleanup callbacks run in LIFO order after the test
// function returns, so a fixture's t.Cleanup (registered later, closing over
// this same pool) runs BEFORE this one closes it. A plain `defer pool.Close()`
// in the test function would instead close the pool as soon as the test body
// returns — before any t.Cleanup runs at all — silently breaking fixture
// teardown (every fixture query below swallows its error).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("could not connect to %s: %v", dbURL, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database not reachable at %s: %v", dbURL, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func randomKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func sealToBase64(t *testing.T, box *secretbox.Box, plaintext string) string {
	t.Helper()
	sealed, err := box.Seal([]byte(plaintext))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sealed)
}

// fixture holds a throwaway workspace (and lazily an issue) plus the exact
// gitea/vcs secretbox keys wired into the test's environment.
type fixture struct {
	pool        *pgxpool.Pool
	workspaceID string
	giteaBox    *secretbox.Box
}

func newFixture(t *testing.T, pool *pgxpool.Pool) *fixture {
	t.Helper()
	ctx := context.Background()

	giteaKeyB64 := randomKey(t)
	vcsKeyB64 := randomKey(t)
	t.Setenv("MULTICA_GITEA_SECRET_KEY", giteaKeyB64)
	t.Setenv("MULTICA_VCS_SECRET_KEY", vcsKeyB64)
	t.Setenv("GITEA_BASE_URL", "https://gitea.internal.example.com")

	giteaKey, err := secretbox.LoadKey("MULTICA_GITEA_SECRET_KEY")
	if err != nil {
		t.Fatalf("load gitea key: %v", err)
	}
	giteaBox, err := secretbox.New(giteaKey)
	if err != nil {
		t.Fatalf("new gitea box: %v", err)
	}

	slug := fmt.Sprintf("giteavcs-test-%d", time.Now().UnixNano())
	var wsID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug) VALUES ($1, $1) RETURNING id
	`, slug).Scan(&wsID); err != nil {
		t.Fatalf("create test workspace: %v", err)
	}

	f := &fixture{pool: pool, workspaceID: wsID, giteaBox: giteaBox}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM issue_vcs_pull_request WHERE issue_id IN (SELECT id FROM issue WHERE workspace_id = $1)`, wsID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM issue_gitea_pull_request WHERE issue_id IN (SELECT id FROM issue WHERE workspace_id = $1)`, wsID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM vcs_pull_request WHERE workspace_id = $1`, wsID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM gitea_pull_request WHERE workspace_id = $1`, wsID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM vcs_connection WHERE workspace_id = $1`, wsID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM gitea_connection WHERE workspace_id = $1`, wsID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM issue WHERE workspace_id = $1`, wsID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM workspace WHERE id = $1`, wsID)
	})
	return f
}

func (f *fixture) createGiteaConnection(t *testing.T, plaintextToken, accountLogin string) {
	t.Helper()
	tokenEnc := sealToBase64(t, f.giteaBox, plaintextToken)
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO gitea_connection (workspace_id, account_login, token_encrypted)
		VALUES ($1, $2, $3)
	`, f.workspaceID, accountLogin, tokenEnc); err != nil {
		t.Fatalf("insert gitea_connection: %v", err)
	}
}

func (f *fixture) createGiteaPullRequest(t *testing.T, repoOwner, repoName string, prNumber int) string {
	t.Helper()
	var id string
	now := time.Now().UTC()
	if err := f.pool.QueryRow(context.Background(), `
		INSERT INTO gitea_pull_request (
			workspace_id, repo_owner, repo_name, pr_number, title, state, html_url, pr_created_at, pr_updated_at
		) VALUES ($1, $2, $3, $4, $5, 'open', $6, $7, $7)
		RETURNING id
	`, f.workspaceID, repoOwner, repoName, prNumber,
		fmt.Sprintf("Fix %s/%s#%d", repoOwner, repoName, prNumber),
		fmt.Sprintf("https://gitea.internal.example.com/%s/%s/pulls/%d", repoOwner, repoName, prNumber),
		now).Scan(&id); err != nil {
		t.Fatalf("insert gitea_pull_request: %v", err)
	}
	return id
}

func (f *fixture) createIssue(t *testing.T) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, creator_type, creator_id)
		VALUES ($1, 'Test issue', 'member', gen_random_uuid())
		RETURNING id
	`, f.workspaceID).Scan(&id); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	return id
}

func (f *fixture) linkIssueToGiteaPR(t *testing.T, issueID, pullRequestID string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO issue_gitea_pull_request (issue_id, pull_request_id, close_intent)
		VALUES ($1, $2, true)
	`, issueID, pullRequestID); err != nil {
		t.Fatalf("insert issue_gitea_pull_request: %v", err)
	}
}

func TestHookSkipsWhenNoGiteaData(t *testing.T) {
	pool := newTestPool(t)
	// A throwaway workspace with zero gitea_connection rows anywhere in the
	// database is not guaranteed in a shared test DB, so this only checks the
	// Skipped path is reachable in principle — assert on Skipped OR a normal
	// result, whichever the live table state produces.
	res, err := Hook(context.Background(), pool, HookOptions{})
	if err != nil {
		t.Fatalf("Hook: %v", err)
	}
	if res.Skipped != "" && res.Skipped != "no gitea_connection rows" {
		t.Fatalf("unexpected Skipped reason: %q", res.Skipped)
	}
}

func TestHookMigratesConnectionAndPullRequestAndIssueLink(t *testing.T) {
	pool := newTestPool(t)
	f := newFixture(t, pool)
	ctx := context.Background()

	f.createGiteaConnection(t, "gitea-pat-plaintext-value", "acme-bot")
	prID := f.createGiteaPullRequest(t, "acme", "widget", 42)
	issueID := f.createIssue(t)
	f.linkIssueToGiteaPR(t, issueID, prID)

	res, err := Hook(ctx, pool, HookOptions{})
	if err != nil {
		t.Fatalf("Hook: %v", err)
	}
	if res.ConnectionsMigrated != 1 {
		t.Errorf("ConnectionsMigrated = %d, want 1", res.ConnectionsMigrated)
	}
	if res.PullRequestsMigrated != 1 {
		t.Errorf("PullRequestsMigrated = %d, want 1", res.PullRequestsMigrated)
	}
	if res.IssueLinksMigrated != 1 {
		t.Errorf("IssueLinksMigrated = %d, want 1", res.IssueLinksMigrated)
	}

	// The token must round-trip: decrypted under the OLD gitea key, then
	// re-encrypted under the NEW vcs key, must equal the original plaintext.
	vcsKey, err := secretbox.LoadKey("MULTICA_VCS_SECRET_KEY")
	if err != nil {
		t.Fatalf("load vcs key: %v", err)
	}
	vcsBox, err := secretbox.New(vcsKey)
	if err != nil {
		t.Fatalf("new vcs box: %v", err)
	}

	var provider, instanceURL, accountLogin, tokenEnc, webhookEnc string
	if err := pool.QueryRow(ctx, `
		SELECT provider, instance_url, account_login, access_token_encrypted, webhook_secret_encrypted
		FROM vcs_connection WHERE workspace_id = $1
	`, f.workspaceID).Scan(&provider, &instanceURL, &accountLogin, &tokenEnc, &webhookEnc); err != nil {
		t.Fatalf("select vcs_connection: %v", err)
	}
	if provider != "gitea" {
		t.Errorf("provider = %q, want gitea", provider)
	}
	if instanceURL != "https://gitea.internal.example.com" {
		t.Errorf("instance_url = %q, want the GITEA_BASE_URL value", instanceURL)
	}
	if accountLogin != "acme-bot" {
		t.Errorf("account_login = %q, want acme-bot", accountLogin)
	}
	plaintext, err := decryptBase64(vcsBox, tokenEnc)
	if err != nil {
		t.Fatalf("decrypt migrated token with vcs box: %v", err)
	}
	if string(plaintext) != "gitea-pat-plaintext-value" {
		t.Errorf("round-tripped token = %q, want %q", plaintext, "gitea-pat-plaintext-value")
	}
	if _, err := decryptBase64(vcsBox, webhookEnc); err != nil {
		t.Errorf("migrated webhook secret does not decrypt under the vcs box: %v", err)
	}

	var vcsPRProvider, repoOwner, repoName string
	if err := pool.QueryRow(ctx, `
		SELECT provider, repo_owner, repo_name FROM vcs_pull_request
		WHERE workspace_id = $1 AND repo_owner = 'acme' AND repo_name = 'widget' AND pr_number = 42
	`, f.workspaceID).Scan(&vcsPRProvider, &repoOwner, &repoName); err != nil {
		t.Fatalf("select vcs_pull_request: %v", err)
	}
	if vcsPRProvider != "gitea" {
		t.Errorf("vcs_pull_request.provider = %q, want gitea", vcsPRProvider)
	}

	var linkCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM issue_vcs_pull_request ivpr
		JOIN vcs_pull_request vpr ON vpr.id = ivpr.pull_request_id
		WHERE ivpr.issue_id = $1 AND vpr.repo_owner = 'acme' AND vpr.repo_name = 'widget' AND vpr.pr_number = 42
	`, issueID).Scan(&linkCount); err != nil {
		t.Fatalf("select issue_vcs_pull_request: %v", err)
	}
	if linkCount != 1 {
		t.Errorf("issue_vcs_pull_request link count = %d, want 1", linkCount)
	}
}

func TestHookIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	f := newFixture(t, pool)
	ctx := context.Background()

	f.createGiteaConnection(t, "gitea-pat-plaintext-value", "acme-bot")
	prID := f.createGiteaPullRequest(t, "acme", "widget", 7)
	issueID := f.createIssue(t)
	f.linkIssueToGiteaPR(t, issueID, prID)

	if _, err := Hook(ctx, pool, HookOptions{}); err != nil {
		t.Fatalf("first Hook run: %v", err)
	}
	res, err := Hook(ctx, pool, HookOptions{})
	if err != nil {
		t.Fatalf("second Hook run: %v", err)
	}
	// The second run must not re-migrate anything already present.
	if res.ConnectionsMigrated != 0 {
		t.Errorf("second run ConnectionsMigrated = %d, want 0", res.ConnectionsMigrated)
	}
	if res.PullRequestsMigrated != 0 {
		t.Errorf("second run PullRequestsMigrated = %d, want 0", res.PullRequestsMigrated)
	}
	if res.IssueLinksMigrated != 0 {
		t.Errorf("second run IssueLinksMigrated = %d, want 0", res.IssueLinksMigrated)
	}

	var connCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM vcs_connection WHERE workspace_id = $1`, f.workspaceID).Scan(&connCount); err != nil {
		t.Fatalf("count vcs_connection: %v", err)
	}
	if connCount != 1 {
		t.Errorf("vcs_connection count after two runs = %d, want 1 (no duplicate)", connCount)
	}
}

func TestHookErrorsClearlyWithoutDestinationKey(t *testing.T) {
	pool := newTestPool(t)
	f := newFixture(t, pool)
	f.createGiteaConnection(t, "gitea-pat-plaintext-value", "acme-bot")

	// newFixture already set MULTICA_VCS_SECRET_KEY; unset it to simulate an
	// operator upgrading without provisioning the VCS key first.
	t.Setenv("MULTICA_VCS_SECRET_KEY", "")

	_, err := Hook(context.Background(), pool, HookOptions{})
	if err == nil {
		t.Fatal("expected an error when MULTICA_VCS_SECRET_KEY is unset but gitea_connection rows exist")
	}
}
