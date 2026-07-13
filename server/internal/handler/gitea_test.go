package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestVerifyGiteaWebhookSignature guards the one meaningful wire-format
// difference from GitHub: Gitea's X-Gitea-Signature is a raw hex HMAC-SHA256
// with NO "sha256=" prefix (see verifyWebhookSignature in github_test.go for
// the GitHub-shaped counterpart).
func TestVerifyGiteaWebhookSignature(t *testing.T) {
	secret := "shared-secret"
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	if !verifyGiteaWebhookSignature(secret, good, body) {
		t.Error("expected valid signature to verify")
	}
	if verifyGiteaWebhookSignature(secret, "sha256="+good, body) {
		t.Error("expected a GitHub-style prefixed header to fail (Gitea has no prefix)")
	}
	if verifyGiteaWebhookSignature(secret, "deadbeef", body) {
		t.Error("expected bad hex to fail")
	}
	if verifyGiteaWebhookSignature(secret, "", body) {
		t.Error("expected empty header to fail")
	}
	if verifyGiteaWebhookSignature("other-secret", good, body) {
		t.Error("expected wrong secret to fail")
	}
}

func giteaWebhookRequest(t *testing.T, workspaceID string, body []byte, secret, event string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/webhooks/gitea/"+workspaceID, bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", event)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-Gitea-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("workspaceId", workspaceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestGiteaWebhook_MergedPR_AdvancesLinkedIssueToDone mirrors
// TestWebhook_MergedPR_AdvancesLinkedIssueToDone (github_test.go): fire a
// `pull_request` webhook carrying a closing keyword against a seeded issue
// and verify the PR row is upserted, linked, and the issue advances to
// `done`. Unlike GitHub, there is no installation row to seed first — the
// workspace is resolved directly from the webhook URL.
func TestGiteaWebhook_MergedPR_AdvancesLinkedIssueToDone(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	secret := "gitea-merge-sync-test-secret"
	t.Setenv("GITEA_WEBHOOK_SECRET", secret)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Gitea PR auto-merge test",
		"status": "in_progress",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue_gitea_pull_request WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM gitea_pull_request WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM activity_log WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	body := map[string]any{
		"action": "closed",
		"pull_request": map[string]any{
			"number":     4321,
			"html_url":   "https://gitea.internal.example.com/acme/widget/pulls/4321",
			"title":      "Fix login " + created.Identifier,
			"body":       "Closes " + created.Identifier,
			"state":      "closed",
			"draft":      false,
			"merged":     true,
			"merged_at":  "2026-04-29T00:00:00Z",
			"closed_at":  "2026-04-29T00:00:00Z",
			"created_at": "2026-04-28T00:00:00Z",
			"updated_at": "2026-04-29T00:00:00Z",
			"head":       map[string]any{"ref": "fix/login"},
			"user":       map[string]any{"login": "octocat", "avatar_url": ""},
		},
		"repository": map[string]any{
			"name":  "widget",
			"owner": map[string]any{"login": "acme"},
		},
	}
	raw, _ := json.Marshal(body)

	w = httptest.NewRecorder()
	testHandler.HandleGiteaWebhook(w, giteaWebhookRequest(t, testWorkspaceID, raw, secret, "pull_request"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("webhook: expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	pr, err := testHandler.Queries.GetGiteaPullRequest(ctx, db.GetGiteaPullRequestParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		RepoOwner:   "acme",
		RepoName:    "widget",
		PrNumber:    4321,
	})
	if err != nil {
		t.Fatalf("GetGiteaPullRequest: %v", err)
	}
	if pr.State != "merged" {
		t.Errorf("expected pr state merged, got %q", pr.State)
	}

	linked, err := testHandler.Queries.ListPullRequestsByIssueGitea(ctx, parseUUID(created.ID))
	if err != nil {
		t.Fatalf("ListPullRequestsByIssueGitea: %v", err)
	}
	if len(linked) != 1 {
		t.Fatalf("expected 1 linked PR, got %d", len(linked))
	}

	updated, err := testHandler.Queries.GetIssue(ctx, parseUUID(created.ID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.Status != "done" {
		t.Errorf("expected issue status 'done', got %q", updated.Status)
	}
}

// TestGiteaWebhook_BadSignatureRejected guards that an unsigned/incorrectly
// signed request never reaches the PR-mirroring logic.
func TestGiteaWebhook_BadSignatureRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", "right-secret")
	raw := []byte(`{"action":"opened"}`)

	w := httptest.NewRecorder()
	testHandler.HandleGiteaWebhook(w, giteaWebhookRequest(t, testWorkspaceID, raw, "wrong-secret", "pull_request"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad signature, got %d", w.Code)
	}
}

// TestGiteaWebhook_UnconfiguredReturns503 guards the "refuse rather than
// treat everything as valid" behavior when GITEA_WEBHOOK_SECRET is unset,
// mirroring the GitHub webhook's equivalent guard.
func TestGiteaWebhook_UnconfiguredReturns503(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", "")
	raw := []byte(`{"action":"opened"}`)

	w := httptest.NewRecorder()
	testHandler.HandleGiteaWebhook(w, giteaWebhookRequest(t, testWorkspaceID, raw, "", "pull_request"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when unconfigured, got %d", w.Code)
	}
}
