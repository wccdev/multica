"use client";

import { giteaHostMatches } from "@multica/core/gitea";
import { GiteaMark } from "../settings/components/gitea-mark";
import { GitHubMark } from "../settings/components/github-mark";

/**
 * Renders the Gitea teapot mark when repoUrl's host matches the workspace's
 * connected Gitea instance, GitHub's octocat otherwise. Shared by the
 * create-project modal and the project resources section — the two must
 * agree on which icon a URL gets, since a repo tagged github_repo but shown
 * with the Gitea icon (or vice versa) would be a confusing UI/data mismatch.
 */
export function RepoProviderIcon({
  url,
  giteaBaseUrl,
  className,
}: {
  url: string;
  giteaBaseUrl: string | undefined;
  className?: string;
}) {
  if (giteaBaseUrl && giteaHostMatches(url, giteaBaseUrl)) {
    return <GiteaMark className={className} />;
  }
  return <GitHubMark className={className} />;
}

/** Pure helper mirroring RepoProviderIcon's provider decision, for callers
 * that need the resource_type string rather than the icon (e.g. deciding
 * which resource_type to attach a picked repo URL as). */
export function repoProviderResourceType(
  url: string,
  giteaBaseUrl: string | undefined,
): "github_repo" | "gitea_repo" {
  return giteaBaseUrl && giteaHostMatches(url, giteaBaseUrl) ? "gitea_repo" : "github_repo";
}
