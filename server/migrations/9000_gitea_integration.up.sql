-- Gitea integration: a single per-workspace connection (PAT-based, no
-- App-marketplace install concept like GitHub), tracked webhooks (so we can
-- clean them up on disconnect/repo removal), mirrored pull request state, and
-- the link table joining issues ↔ Gitea PRs. Kept fully separate from the
-- github_* tables (own link table, not a shared issue_pull_request) so this
-- migration never touches GitHub's schema.
--
-- Numbered 9000 (not the next sequential number after upstream's highest)
-- because this repo is a fork that regularly merges multica-ai/multica's
-- upstream `main`, and upstream keeps adding migrations in the normal
-- 001-999 range. A fork-side migration living in that shared range WILL
-- eventually collide with an upstream migration claiming the same number
-- (this happened once already — this file was originally 167, then 175,
-- each time stomped by an upstream migration reusing that number a few
-- weeks later). 9000+ is a block reserved for this fork's own
-- migrations, far outside any range upstream will reach organically.
-- Any future fork-only migration should continue at 9001, 9002, etc. —
-- never renumber this file again, and never pick a number below 9000 for
-- a new fork-only migration.

CREATE TABLE gitea_connection (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    account_login      TEXT NOT NULL,
    account_avatar_url TEXT,
    token_encrypted    TEXT NOT NULL,
    connected_by_id    UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id)
);

CREATE TABLE gitea_webhook (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    repo_owner   TEXT NOT NULL,
    repo_name    TEXT NOT NULL,
    hook_id      BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, repo_owner, repo_name)
);

CREATE TABLE gitea_pull_request (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    repo_owner      TEXT NOT NULL,
    repo_name       TEXT NOT NULL,
    pr_number       INTEGER NOT NULL,
    title           TEXT NOT NULL,
    state           TEXT NOT NULL
        CHECK (state IN ('open', 'closed', 'merged', 'draft')),
    html_url        TEXT NOT NULL,
    branch          TEXT,
    author_login    TEXT,
    author_avatar_url TEXT,
    merged_at       TIMESTAMPTZ,
    closed_at       TIMESTAMPTZ,
    pr_created_at   TIMESTAMPTZ NOT NULL,
    pr_updated_at   TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, repo_owner, repo_name, pr_number)
);

CREATE INDEX idx_gitea_pull_request_workspace ON gitea_pull_request(workspace_id);

CREATE TABLE issue_gitea_pull_request (
    issue_id        UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    pull_request_id UUID NOT NULL REFERENCES gitea_pull_request(id) ON DELETE CASCADE,
    linked_by_type  TEXT,
    linked_by_id    UUID,
    close_intent    BOOLEAN NOT NULL DEFAULT FALSE,
    reference_only  BOOLEAN NOT NULL DEFAULT FALSE,
    linked_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, pull_request_id)
);

CREATE INDEX idx_issue_gitea_pull_request_pr ON issue_gitea_pull_request(pull_request_id);
