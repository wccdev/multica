-- =====================
-- Gitea Connection
-- =====================

-- name: GetGiteaConnectionByWorkspace :one
SELECT * FROM gitea_connection WHERE workspace_id = $1;

-- name: UpsertGiteaConnection :one
INSERT INTO gitea_connection (
    workspace_id, account_login, account_avatar_url, token_encrypted, connected_by_id
) VALUES (
    $1, $2, sqlc.narg('account_avatar_url'), $3, sqlc.narg('connected_by_id')
)
ON CONFLICT (workspace_id) DO UPDATE SET
    account_login = EXCLUDED.account_login,
    account_avatar_url = EXCLUDED.account_avatar_url,
    token_encrypted = EXCLUDED.token_encrypted,
    connected_by_id = EXCLUDED.connected_by_id,
    updated_at = now()
RETURNING *;

-- name: DeleteGiteaConnection :exec
DELETE FROM gitea_connection WHERE workspace_id = $1;

-- =====================
-- Gitea Webhook (tracked so we can clean them up on disconnect/repo removal)
-- =====================

-- name: ListGiteaWebhooksByWorkspace :many
SELECT * FROM gitea_webhook WHERE workspace_id = $1 ORDER BY created_at ASC;

-- name: UpsertGiteaWebhook :one
INSERT INTO gitea_webhook (
    workspace_id, repo_owner, repo_name, hook_id
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (workspace_id, repo_owner, repo_name) DO UPDATE SET
    hook_id = EXCLUDED.hook_id
RETURNING *;

-- name: DeleteGiteaWebhook :exec
DELETE FROM gitea_webhook WHERE workspace_id = $1 AND repo_owner = $2 AND repo_name = $3;

-- name: DeleteGiteaWebhooksByWorkspace :many
-- Returns the deleted rows so the caller can best-effort remove each hook on
-- the Gitea side (via its hook_id) before the local record is gone.
DELETE FROM gitea_webhook WHERE workspace_id = $1
RETURNING *;

-- =====================
-- Gitea Pull Request
-- =====================

-- name: UpsertGiteaPullRequest :one
INSERT INTO gitea_pull_request (
    workspace_id, repo_owner, repo_name, pr_number,
    title, state, html_url, branch, author_login, author_avatar_url,
    merged_at, closed_at, pr_created_at, pr_updated_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, sqlc.narg('branch'), sqlc.narg('author_login'), sqlc.narg('author_avatar_url'),
    sqlc.narg('merged_at'), sqlc.narg('closed_at'), $8, $9
)
ON CONFLICT (workspace_id, repo_owner, repo_name, pr_number) DO UPDATE SET
    title = EXCLUDED.title,
    state = EXCLUDED.state,
    html_url = EXCLUDED.html_url,
    branch = EXCLUDED.branch,
    author_login = EXCLUDED.author_login,
    author_avatar_url = EXCLUDED.author_avatar_url,
    merged_at = EXCLUDED.merged_at,
    closed_at = EXCLUDED.closed_at,
    pr_updated_at = EXCLUDED.pr_updated_at,
    updated_at = now()
RETURNING *;

-- name: GetGiteaPullRequest :one
SELECT * FROM gitea_pull_request
WHERE workspace_id = $1 AND repo_owner = $2 AND repo_name = $3 AND pr_number = $4;

-- name: ListPullRequestsByIssueGitea :many
-- reference_only links (a PR that merely mentions the issue identifier in its
-- body, with no closing keyword and no title/branch reference) are filtered
-- out, mirroring ListPullRequestsByIssue's github equivalent.
SELECT pr.*
FROM gitea_pull_request pr
JOIN issue_gitea_pull_request ipr ON ipr.pull_request_id = pr.id
WHERE ipr.issue_id = sqlc.arg('issue_id') AND NOT ipr.reference_only
ORDER BY pr.pr_created_at DESC;

-- name: ListIssueIDsForGiteaPullRequest :many
SELECT issue_id FROM issue_gitea_pull_request
WHERE pull_request_id = $1;

-- name: GetIssueGiteaPullRequestCloseAggregate :one
-- Mirrors GetIssuePullRequestCloseAggregate: gates the webhook's
-- auto-advance-to-done decision on "no linked PR still open/draft" AND "at
-- least one merged PR declared explicit closing intent".
SELECT
    COALESCE(SUM(CASE WHEN pr.state IN ('open', 'draft') THEN 1 ELSE 0 END), 0)::bigint AS open_count,
    COALESCE(SUM(CASE WHEN pr.state = 'merged' AND ipr.close_intent THEN 1 ELSE 0 END), 0)::bigint AS merged_with_close_intent_count
FROM gitea_pull_request pr
JOIN issue_gitea_pull_request ipr ON ipr.pull_request_id = pr.id
WHERE ipr.issue_id = $1 AND NOT ipr.reference_only;

-- =====================
-- Issue ↔ Gitea Pull Request link
-- =====================

-- name: LinkIssueToGiteaPullRequest :exec
-- Mirrors LinkIssueToPullRequest's preserve-on-conflict semantics.
INSERT INTO issue_gitea_pull_request (
    issue_id, pull_request_id, linked_by_type, linked_by_id, close_intent, reference_only
) VALUES (
    $1, $2, sqlc.narg('linked_by_type'), sqlc.narg('linked_by_id'), $3, sqlc.arg('reference_only')
)
ON CONFLICT (issue_id, pull_request_id) DO UPDATE SET
    close_intent = CASE
        WHEN sqlc.arg('preserve_close_intent') THEN issue_gitea_pull_request.close_intent
        ELSE EXCLUDED.close_intent
    END,
    reference_only = CASE
        WHEN sqlc.arg('preserve_close_intent') THEN issue_gitea_pull_request.reference_only
        ELSE EXCLUDED.reference_only
    END;
