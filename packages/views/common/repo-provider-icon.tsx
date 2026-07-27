"use client";

import { GitHubMark } from "../settings/components/github-mark";

/**
 * Renders the provider mark for a repo URL. GitHub is the only git-checkout
 * resource type today; self-hosted providers (Forgejo/Gitea/GitLab) are
 * handled by the separate VCS integration, not by project resource
 * attachment, so this always renders the GitHub mark.
 */
export function RepoProviderIcon({
  className,
}: {
  className?: string;
}) {
  return <GitHubMark className={className} />;
}

/** Pure helper mirroring RepoProviderIcon's provider decision, for callers
 * that need the resource_type string rather than the icon (e.g. deciding
 * which resource_type to attach a picked repo URL as). */
export function repoProviderResourceType(): "github_repo" {
  return "github_repo";
}
