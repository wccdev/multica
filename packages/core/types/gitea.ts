export type GiteaPullRequestState = "open" | "closed" | "merged" | "draft";

export interface GiteaConnection {
  id: string;
  workspace_id: string;
  account_login: string;
  account_avatar_url: string | null;
  /** Display name of the workspace member who connected this token. Optional
   * because the connecting user may not have been signed in when a webhook
   * refreshed the row, or minimum-visibility deployments may omit it. */
  connected_by_id?: string;
  created_at: string;
}

export interface GetGiteaConnectionResponse {
  connection: GiteaConnection | null;
  /** Whether the deployment has GITEA_BASE_URL / GITEA_WEBHOOK_SECRET /
   * MULTICA_GITEA_SECRET_KEY / MULTICA_PUBLIC_URL all configured. When false,
   * the Connect action is hidden / disabled. */
  configured: boolean;
  /** Whether the caller can connect / disconnect / sync. Non-admin members
   * get `false`. */
  can_manage: boolean;
  /** The configured Gitea instance origin, shown so admins know which
   * instance a pasted token needs to belong to. Empty when unconfigured. */
  base_url: string;
}

export interface GiteaPullRequest {
  id: string;
  workspace_id: string;
  repo_owner: string;
  repo_name: string;
  number: number;
  title: string;
  state: GiteaPullRequestState;
  html_url: string;
  branch: string | null;
  author_login: string | null;
  author_avatar_url: string | null;
  merged_at: string | null;
  closed_at: string | null;
  pr_created_at: string;
  pr_updated_at: string;
}

export interface GiteaSyncResponse {
  created: number;
  removed: number;
}
