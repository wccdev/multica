import type { Workspace } from "../types";

export interface GiteaSettings {
  /** Master switch. When false, every UI affordance and side-effect is gated off. */
  enabled: boolean;
  /** Issue-detail PR sidebar visibility. Implies `enabled`. */
  prSidebar: boolean;
  /** Co-authored-by trailer in agent commits. Implies `enabled`.
   *
   * Reads the SAME `co_authored_by_enabled` key as deriveGitHubSettings —
   * the underlying prepare-commit-msg hook (repocache/cache.go) is a plain
   * git hook with no host awareness, applied to every cloned repo
   * regardless of provider, so there is one shared switch surfaced on both
   * the GitHub and Gitea settings tabs rather than two independent ones. */
  coAuthor: boolean;
  /** Auto-link issues ↔ PRs from webhook payloads. Implies `enabled`. */
  autoLinkPRs: boolean;
}

/**
 * Pure derivation from a workspace's settings JSONB, mirroring
 * deriveGitHubSettings. Defaults every flag to true so a freshly connected
 * workspace behaves like GitHub's historical "all on" default.
 */
export function deriveGiteaSettings(
  workspace: Pick<Workspace, "settings"> | null | undefined,
): GiteaSettings {
  const s = (workspace?.settings ?? {}) as Record<string, unknown>;
  const enabled = s.gitea_enabled !== false;
  return {
    enabled,
    prSidebar: enabled && s.gitea_pr_sidebar_enabled !== false,
    coAuthor: enabled && s.co_authored_by_enabled !== false,
    autoLinkPRs: enabled && s.gitea_auto_link_prs_enabled !== false,
  };
}
