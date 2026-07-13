package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/gitea"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This file is the Gitea counterpart to github.go, for teams that run a
// private/self-hosted Gitea instance instead of GitHub. It is deliberately
// independent of github.go and the github_* tables (separate handler file,
// separate schema, separate frontend package) so this fork's Gitea support
// never conflicts with upstream GitHub changes. Where the underlying logic
// is genuinely provider-agnostic (issue-identifier extraction, timestamp
// parsing, PR state derivation, small helpers like uuidToString/writeJSON),
// this file calls the existing package-level functions in github.go /
// handler.go rather than duplicating them.
//
// Unlike GitHub's App-marketplace install (an ephemeral installation token
// scoped by GitHub), Gitea has no such concept: the workspace admin pastes a
// Personal/Bot Access Token (PAT), which is validated live and stored
// encrypted at rest (see server/internal/integrations/gitea). Because a bare
// Gitea webhook payload carries no tenant identifier, the workspace id is
// embedded directly in the webhook URL: POST /api/webhooks/gitea/{workspaceId}.

// ── Response shapes ─────────────────────────────────────────────────────────

type GiteaConnectionResponse struct {
	ID               string  `json:"id"`
	WorkspaceID      string  `json:"workspace_id"`
	AccountLogin     string  `json:"account_login"`
	AccountAvatarURL *string `json:"account_avatar_url"`
	ConnectedByID    *string `json:"connected_by_id,omitempty"`
	CreatedAt        string  `json:"created_at"`
}

type GetGiteaConnectionResponse struct {
	Connection *GiteaConnectionResponse `json:"connection"`
	Configured bool                     `json:"configured"`
	CanManage  bool                     `json:"can_manage"`
	BaseURL    string                   `json:"base_url"`
}

type GiteaPullRequestResponse struct {
	ID              string  `json:"id"`
	WorkspaceID     string  `json:"workspace_id"`
	RepoOwner       string  `json:"repo_owner"`
	RepoName        string  `json:"repo_name"`
	Number          int32   `json:"number"`
	Title           string  `json:"title"`
	State           string  `json:"state"`
	HtmlURL         string  `json:"html_url"`
	Branch          *string `json:"branch"`
	AuthorLogin     *string `json:"author_login"`
	AuthorAvatarURL *string `json:"author_avatar_url"`
	MergedAt        *string `json:"merged_at"`
	ClosedAt        *string `json:"closed_at"`
	PRCreatedAt     string  `json:"pr_created_at"`
	PRUpdatedAt     string  `json:"pr_updated_at"`
}

type GiteaSyncResponse struct {
	Created int `json:"created"`
	Removed int `json:"removed"`
}

func giteaConnectionToResponse(c db.GiteaConnection) GiteaConnectionResponse {
	resp := GiteaConnectionResponse{
		ID:               uuidToString(c.ID),
		WorkspaceID:      uuidToString(c.WorkspaceID),
		AccountLogin:     c.AccountLogin,
		AccountAvatarURL: textToPtr(c.AccountAvatarUrl),
		CreatedAt:        timestampToString(c.CreatedAt),
	}
	if c.ConnectedByID.Valid {
		id := uuidToString(c.ConnectedByID)
		resp.ConnectedByID = &id
	}
	return resp
}

func giteaPullRequestToResponse(p db.GiteaPullRequest) GiteaPullRequestResponse {
	return GiteaPullRequestResponse{
		ID:              uuidToString(p.ID),
		WorkspaceID:     uuidToString(p.WorkspaceID),
		RepoOwner:       p.RepoOwner,
		RepoName:        p.RepoName,
		Number:          p.PrNumber,
		Title:           p.Title,
		State:           p.State,
		HtmlURL:         p.HtmlUrl,
		Branch:          textToPtr(p.Branch),
		AuthorLogin:     textToPtr(p.AuthorLogin),
		AuthorAvatarURL: textToPtr(p.AuthorAvatarUrl),
		MergedAt:        timestampToPtr(p.MergedAt),
		ClosedAt:        timestampToPtr(p.ClosedAt),
		PRCreatedAt:     timestampToString(p.PrCreatedAt),
		PRUpdatedAt:     timestampToString(p.PrUpdatedAt),
	}
}

// ── Config ───────────────────────────────────────────────────────────────

func giteaBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("GITEA_BASE_URL")), "/")
}

func giteaWebhookSecret() string { return strings.TrimSpace(os.Getenv("GITEA_WEBHOOK_SECRET")) }

// isGiteaConfigured requires the instance URL, the webhook secret, the
// secretbox-backed install service (MULTICA_GITEA_SECRET_KEY, wired in
// router.go), AND a public URL — without the last one we cannot mint an
// absolute webhook target URL for Gitea to call back into.
func (h *Handler) isGiteaConfigured() bool {
	return h.GiteaInstall != nil && giteaBaseURL() != "" && giteaWebhookSecret() != "" && h.cfg.PublicURL != ""
}

func (h *Handler) giteaWebhookTargetURL(workspaceID string) string {
	return strings.TrimRight(h.cfg.PublicURL, "/") + "/api/webhooks/gitea/" + workspaceID
}

// ── Connect / disconnect / sync ─────────────────────────────────────────────

type registerGiteaConnectionRequest struct {
	Token string `json:"token"`
}

// RegisterGiteaConnection (POST /api/workspaces/{id}/gitea/install,
// admin-only) validates the pasted PAT live and persists the workspace's
// connection, then immediately syncs webhooks against the current
// workspace.repos so a fresh connect doesn't require a separate manual step.
func (h *Handler) RegisterGiteaConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if !h.isGiteaConfigured() {
		writeError(w, http.StatusServiceUnavailable, "gitea integration is not configured")
		return
	}
	var req registerGiteaConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var connectedBy pgtype.UUID
	if userID := requestUserID(r); userID != "" {
		if u, err := parseStrictUUID(userID); err == nil {
			connectedBy = u
		}
	}
	conn, err := h.GiteaInstall.Connect(r.Context(), wsUUID, req.Token, connectedBy)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if result, err := h.syncGiteaWebhooks(r.Context(), wsUUID, conn); err != nil {
		slog.Warn("gitea: post-connect webhook sync failed", "err", err, "workspace_id", workspaceID)
	} else {
		slog.Info("gitea: post-connect webhook sync", "workspace_id", workspaceID, "created", result.Created, "removed", result.Removed)
	}
	h.publish(protocol.EventGiteaConnectionCreated, workspaceID, "system", "", map[string]any{
		"connection": giteaConnectionToResponse(conn),
	})
	writeJSON(w, http.StatusOK, map[string]any{"connection": giteaConnectionToResponse(conn)})
}

// GetGiteaConnection (GET /api/workspaces/{id}/gitea/connection,
// member-visible) mirrors ListGitHubInstallations' shape: every member sees
// whether Gitea is wired up and by whom; only admins get `can_manage`.
func (h *Handler) GetGiteaConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, _ := middleware.MemberFromContext(r.Context())
	canManage := roleAllowed(member.Role, "owner", "admin")

	resp := GetGiteaConnectionResponse{
		Configured: h.isGiteaConfigured(),
		CanManage:  canManage,
		BaseURL:    giteaBaseURL(),
	}
	conn, err := h.Queries.GetGiteaConnectionByWorkspace(r.Context(), wsUUID)
	if err == nil {
		c := giteaConnectionToResponse(conn)
		resp.Connection = &c
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load connection")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteGiteaConnection (DELETE /api/workspaces/{id}/gitea/connection,
// admin-only) best-effort removes every tracked webhook on the Gitea side,
// then deletes the connection + webhook rows.
func (h *Handler) DeleteGiteaConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if h.GiteaInstall == nil {
		writeError(w, http.StatusServiceUnavailable, "gitea integration is not configured")
		return
	}
	conn, err := h.Queries.GetGiteaConnectionByWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load connection")
		return
	}
	if err := h.GiteaInstall.Disconnect(r.Context(), wsUUID, conn); err != nil {
		slog.Error("gitea: disconnect failed", "err", err, "workspace_id", workspaceID)
		writeError(w, http.StatusInternalServerError, "failed to disconnect")
		return
	}
	h.publish(protocol.EventGiteaConnectionDeleted, workspaceID, "system", "", map[string]any{
		"id": uuidToString(conn.ID),
	})
	w.WriteHeader(http.StatusNoContent)
}

// SyncGiteaRepositories (POST /api/workspaces/{id}/gitea/sync, admin-only)
// reconciles webhooks against the workspace's current repos list — the
// explicit action to re-run after editing Settings → Repositories.
func (h *Handler) SyncGiteaRepositories(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if !h.isGiteaConfigured() {
		writeError(w, http.StatusServiceUnavailable, "gitea integration is not configured")
		return
	}
	conn, err := h.Queries.GetGiteaConnectionByWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "workspace is not connected to gitea")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load connection")
		return
	}
	result, err := h.syncGiteaWebhooks(r.Context(), wsUUID, conn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sync repositories")
		return
	}
	writeJSON(w, http.StatusOK, GiteaSyncResponse{Created: result.Created, Removed: result.Removed})
}

// syncGiteaWebhooks re-reads the workspace's generic repos list (the same
// registry the daemon clones from — see workspace.repos) and reconciles
// Gitea-side webhooks against it, restricted to the repos hosted on this
// workspace's connected Gitea instance.
func (h *Handler) syncGiteaWebhooks(ctx context.Context, wsUUID pgtype.UUID, conn db.GiteaConnection) (gitea.SyncResult, error) {
	ws, err := h.Queries.GetWorkspace(ctx, wsUUID)
	if err != nil {
		return gitea.SyncResult{}, err
	}
	var repos []struct {
		URL string `json:"url"`
	}
	if ws.Repos != nil {
		_ = json.Unmarshal(ws.Repos, &repos)
	}
	urls := make([]string, 0, len(repos))
	for _, repo := range repos {
		if repo.URL != "" {
			urls = append(urls, repo.URL)
		}
	}
	target := h.giteaWebhookTargetURL(uuidToString(wsUUID))
	return h.GiteaInstall.SyncWebhooks(ctx, wsUUID, conn, target, giteaWebhookSecret(), urls)
}

// ── List PRs for an issue ───────────────────────────────────────────────────

// ListIssueGiteaPullRequests (GET /api/issues/{id}/gitea-pull-requests) is
// the Gitea counterpart to ListPullRequestsForIssue.
func (h *Handler) ListIssueGiteaPullRequests(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rows, err := h.Queries.ListPullRequestsByIssueGitea(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pull requests")
		return
	}
	out := make([]GiteaPullRequestResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, giteaPullRequestToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pull_requests": out})
}

// ── Webhook ─────────────────────────────────────────────────────────────────

// verifyGiteaWebhookSignature checks Gitea's X-Gitea-Signature header: a raw
// hex-encoded HMAC-SHA256 of the body, with NO "sha256=" prefix — this is
// the one meaningful wire-format difference from GitHub's
// X-Hub-Signature-256 (see verifyWebhookSignature in github.go).
func verifyGiteaWebhookSignature(secret, header string, body []byte) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	want, err := hex.DecodeString(header)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// HandleGiteaWebhook (POST /api/webhooks/gitea/{workspaceId}) is the
// destination Gitea calls for every event on a repo we registered a webhook
// for. Unlike GitHub's installation-scoped payloads, a bare Gitea webhook
// carries no tenant identifier, so the workspace id is embedded in the URL
// itself and resolved before signature verification even runs.
func (h *Handler) HandleGiteaWebhook(w http.ResponseWriter, r *http.Request) {
	workspaceIDStr := chi.URLParam(r, "workspaceId")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDStr, "workspace id")
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MiB cap
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body failed")
		return
	}
	secret := giteaWebhookSecret()
	if secret == "" {
		writeError(w, http.StatusServiceUnavailable, "gitea webhooks not configured")
		return
	}
	if !verifyGiteaWebhookSignature(secret, r.Header.Get("X-Gitea-Signature"), body) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	event := r.Header.Get("X-Gitea-Event")
	switch event {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]string{"ok": "pong"})
		return
	case "pull_request":
		h.handleGiteaPullRequestEvent(r.Context(), wsUUID, body)
	default:
		// Acknowledge every event so Gitea doesn't mark the webhook
		// failing, but ignore types we don't model (issues, push, etc.).
	}
	w.WriteHeader(http.StatusAccepted)
}

// giteaPullRequestPayload captures the fields Gitea's pull_request webhook
// shares with GitHub's (Gitea deliberately mirrors GitHub's webhook JSON
// shape for these fields), which lets us reuse github.go's
// extractIdentifiers/extractClosingIdentifiers/lookupIssueByIdentifier/
// parseGHTime helpers unchanged instead of duplicating them.
type giteaPullRequestPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number    int32  `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		State     string `json:"state"`
		Draft     bool   `json:"draft"`
		Merged    bool   `json:"merged"`
		MergedAt  string `json:"merged_at"`
		ClosedAt  string `json:"closed_at"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		HTMLURL   string `json:"html_url"`
		Head      struct {
			Ref string `json:"ref"`
		} `json:"head"`
		User struct {
			Login     string `json:"login"`
			AvatarURL string `json:"avatar_url"`
		} `json:"user"`
	} `json:"pull_request"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

// handleGiteaPullRequestEvent mirrors mirrorPullRequestForWorkspace in
// github.go: upsert the PR row, auto-link issues referenced by identifier
// (gated by the workspace's gitea toggles), advance linked issues to `done`
// on a terminal event with declared closing intent, and broadcast. There is
// no check_suite/CI mirroring in this pass (Gitea's CI sources vary —
// Actions/Drone/Jenkins — with no unified webhook shape), so this is
// simpler than its GitHub counterpart.
func (h *Handler) handleGiteaPullRequestEvent(ctx context.Context, wsID pgtype.UUID, body []byte) {
	var p giteaPullRequestPayload
	if err := json.Unmarshal(body, &p); err != nil {
		slog.Warn("gitea: bad pull_request payload", "err", err)
		return
	}
	if p.Repository.Name == "" || p.Repository.Owner.Login == "" {
		return
	}
	state := derivePRState(p.PullRequest.State, p.PullRequest.Draft, p.PullRequest.Merged)
	pr, err := h.Queries.UpsertGiteaPullRequest(ctx, db.UpsertGiteaPullRequestParams{
		WorkspaceID:     wsID,
		RepoOwner:       p.Repository.Owner.Login,
		RepoName:        p.Repository.Name,
		PrNumber:        p.PullRequest.Number,
		Title:           p.PullRequest.Title,
		State:           state,
		HtmlUrl:         p.PullRequest.HTMLURL,
		Branch:          ptrToText(strPtrOrNil(p.PullRequest.Head.Ref)),
		AuthorLogin:     ptrToText(strPtrOrNil(p.PullRequest.User.Login)),
		AuthorAvatarUrl: ptrToText(strPtrOrNil(p.PullRequest.User.AvatarURL)),
		MergedAt:        parseGHTime(p.PullRequest.MergedAt),
		ClosedAt:        parseGHTime(p.PullRequest.ClosedAt),
		PrCreatedAt:     parseGHTimeRequired(p.PullRequest.CreatedAt),
		PrUpdatedAt:     parseGHTimeRequired(p.PullRequest.UpdatedAt),
	})
	if err != nil {
		slog.Warn("gitea: upsert pr failed", "err", err)
		return
	}

	workspaceID := uuidToString(wsID)
	resp := giteaPullRequestToResponse(pr)

	linkedIssueIDs := make([]string, 0)
	if h.workspaceGiteaAutoLinkPRsEnabled(ctx, wsID) {
		idents := extractIdentifiers(p.PullRequest.Title, p.PullRequest.Body, p.PullRequest.Head.Ref)
		closingIdents := map[string]struct{}{}
		for _, c := range extractClosingIdentifiers(p.PullRequest.Title, p.PullRequest.Body) {
			closingIdents[c] = struct{}{}
		}
		qualifyingIdents := map[string]struct{}{}
		for _, id := range extractIdentifiers(p.PullRequest.Title, p.PullRequest.Head.Ref) {
			qualifyingIdents[id] = struct{}{}
		}
		for c := range closingIdents {
			qualifyingIdents[c] = struct{}{}
		}
		preserveCloseIntent := p.Action != "closed" && (state == "merged" || state == "closed")
		prefix := h.getIssuePrefix(ctx, wsID)
		reevalIssues := make([]db.Issue, 0, len(idents))
		for _, id := range idents {
			issue, ok := h.lookupIssueByIdentifier(ctx, wsID, prefix, id)
			if !ok {
				continue
			}
			_, declared := closingIdents[id]
			closeIntent := declared && !preserveCloseIntent
			_, qualifies := qualifyingIdents[id]
			referenceOnly := !qualifies
			if err := h.Queries.LinkIssueToGiteaPullRequest(ctx, db.LinkIssueToGiteaPullRequestParams{
				IssueID:             issue.ID,
				PullRequestID:       pr.ID,
				CloseIntent:         closeIntent,
				ReferenceOnly:       referenceOnly,
				PreserveCloseIntent: preserveCloseIntent,
				LinkedByType:        strToText("system"),
				LinkedByID:          pgtype.UUID{},
			}); err != nil {
				slog.Warn("gitea: link failed", "err", err)
				continue
			}
			linkedIssueIDs = append(linkedIssueIDs, uuidToString(issue.ID))
			reevalIssues = append(reevalIssues, issue)
		}

		if state == "merged" || state == "closed" {
			for _, issue := range reevalIssues {
				if issue.Status == "done" || issue.Status == "cancelled" {
					continue
				}
				counts, err := h.Queries.GetIssueGiteaPullRequestCloseAggregate(ctx, issue.ID)
				if err != nil {
					slog.Warn("gitea: count linked pr states failed", "err", err, "issue_id", uuidToString(issue.ID))
					continue
				}
				if counts.OpenCount == 0 && counts.MergedWithCloseIntentCount > 0 {
					h.advanceGiteaIssueToDone(ctx, issue, workspaceID)
				}
			}
		}
	}

	h.publish(protocol.EventGiteaPullRequestUpdated, workspaceID, "system", "", map[string]any{
		"pull_request":     resp,
		"linked_issue_ids": linkedIssueIDs,
	})
}

// workspaceGiteaAutoLinkPRsEnabled mirrors workspaceAutoLinkPRsEnabled
// (github.go) against the gitea_enabled/gitea_auto_link_prs_enabled keys.
func (h *Handler) workspaceGiteaAutoLinkPRsEnabled(ctx context.Context, workspaceID pgtype.UUID) bool {
	ws, err := h.Queries.GetWorkspace(ctx, workspaceID)
	if err != nil || len(ws.Settings) == 0 {
		return true
	}
	var s struct {
		GiteaEnabled            *bool `json:"gitea_enabled"`
		GiteaAutoLinkPRsEnabled *bool `json:"gitea_auto_link_prs_enabled"`
	}
	if err := json.Unmarshal(ws.Settings, &s); err != nil {
		return true
	}
	if s.GiteaEnabled != nil && !*s.GiteaEnabled {
		return false
	}
	if s.GiteaAutoLinkPRsEnabled == nil {
		return true
	}
	return *s.GiteaAutoLinkPRsEnabled
}

// advanceGiteaIssueToDone mirrors advanceIssueToDone (github.go) with a
// distinct `source` tag ("gitea_pr_merged" vs "github_pr_merged") so
// downstream consumers of the issue:updated event can tell which
// integration drove the transition.
func (h *Handler) advanceGiteaIssueToDone(ctx context.Context, issue db.Issue, workspaceID string) {
	updated, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID:          issue.ID,
		Status:      "done",
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		slog.Warn("gitea: advance issue to done failed", "err", err)
		return
	}
	h.notifyParentOfChildDone(ctx, issue, updated)
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	resp := issueToResponse(updated, prefix)
	h.publish(protocol.EventIssueUpdated, workspaceID, "system", "", map[string]any{
		"issue":          resp,
		"status_changed": true,
		"prev_status":    issue.Status,
		"creator_type":   issue.CreatorType,
		"creator_id":     uuidToString(issue.CreatorID),
		"source":         "gitea_pr_merged",
	})
}
