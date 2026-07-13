package gitea

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SyncResult summarizes a webhook reconciliation pass.
type SyncResult struct {
	Created int
	Removed int
}

type repoKey struct {
	owner string
	name  string
}

// SyncWebhooks reconciles gitea_webhook rows against the workspace's current
// repo list, restricted to repoURLs whose host matches the connected Gitea
// instance (see HostMatches) — repos.repos is a generic "code the agent
// clones" list that may also contain non-Gitea entries, which are silently
// ignored here. Repos newly present get a webhook created on the Gitea side;
// webhook rows whose repo is no longer listed get their Gitea-side hook
// deleted. Both directions are best-effort per-repo: one failing repo does
// not abort the rest of the sync.
func (s *InstallService) SyncWebhooks(ctx context.Context, workspaceID pgtype.UUID, conn db.GiteaConnection, webhookTargetURL, webhookSecret string, repoURLs []string) (SyncResult, error) {
	client, err := s.ClientFor(conn)
	if err != nil {
		return SyncResult{}, err
	}

	desired := map[repoKey]struct{}{}
	for _, raw := range repoURLs {
		if !HostMatches(raw, s.baseURL) {
			continue
		}
		owner, name, ok := ParseRepoURL(raw)
		if !ok {
			continue
		}
		desired[repoKey{owner, name}] = struct{}{}
	}

	existing, err := s.queries.ListGiteaWebhooksByWorkspace(ctx, workspaceID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("gitea: list existing webhooks: %w", err)
	}
	have := map[repoKey]db.GiteaWebhook{}
	for _, row := range existing {
		have[repoKey{row.RepoOwner, row.RepoName}] = row
	}

	var result SyncResult
	for key := range desired {
		if _, ok := have[key]; ok {
			continue
		}
		hookID, err := client.CreateWebhook(ctx, key.owner, key.name, webhookTargetURL, webhookSecret)
		if err != nil {
			continue
		}
		if _, err := s.queries.UpsertGiteaWebhook(ctx, db.UpsertGiteaWebhookParams{
			WorkspaceID: workspaceID,
			RepoOwner:   key.owner,
			RepoName:    key.name,
			HookID:      hookID,
		}); err != nil {
			continue
		}
		result.Created++
	}

	for key, row := range have {
		if _, ok := desired[key]; ok {
			continue
		}
		_ = client.DeleteWebhook(ctx, row.RepoOwner, row.RepoName, row.HookID)
		if err := s.queries.DeleteGiteaWebhook(ctx, db.DeleteGiteaWebhookParams{
			WorkspaceID: workspaceID,
			RepoOwner:   row.RepoOwner,
			RepoName:    row.RepoName,
		}); err != nil {
			continue
		}
		result.Removed++
	}

	return result, nil
}
