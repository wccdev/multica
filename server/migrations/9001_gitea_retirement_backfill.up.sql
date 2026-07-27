-- Gitea integration retirement: this fork's custom per-workspace Gitea
-- vertical (migration 9000) is being replaced by upstream's native
-- multi-provider VCS integration (migration 216_vcs_integration, supporting
-- Forgejo/Gitea/GitLab). This migration's Go pre-migration hook
-- (server/internal/giteavcsbackfill) runs BEFORE this SQL and migrates
-- gitea_connection / gitea_pull_request / issue_gitea_pull_request rows into
-- vcs_connection / vcs_pull_request / issue_vcs_pull_request, re-encrypting
-- the stored access token from MULTICA_GITEA_SECRET_KEY to
-- MULTICA_VCS_SECRET_KEY. See that package's doc comment for the full story,
-- including why the webhook configured on the Gitea side must be re-pointed
-- at the new URL/secret afterward (Gitea used one shared static webhook
-- secret; VCS generates one random secret per connection).
--
-- This SQL step reclassifies existing project resources: gitea_repo and
-- github_repo have always been byte-identical in ref shape and daemon
-- checkout semantics (server/internal/handler/project_resource.go never
-- branched on the distinction) — the separate resource_type existed only so
-- the UI could pick the right provider icon. Relabeling is lossless.
--
-- The legacy gitea_connection / gitea_pull_request / issue_gitea_pull_request
-- / gitea_webhook tables and migration 9000 are deliberately NOT dropped —
-- they remain as a historical safety net after this one-time migration.
--
-- Numbered 9001 per the 9000+ fork-reserved block (see 9000's header): never
-- pick a number below 9000 for a new fork-only migration.
UPDATE project_resource
SET resource_type = 'github_repo'
WHERE resource_type = 'gitea_repo';
