import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const giteaKeys = {
  all: (wsId: string) => ["gitea", wsId] as const,
  connection: (wsId: string) => [...giteaKeys.all(wsId), "connection"] as const,
  pullRequests: (issueId: string) => ["gitea", "pull-requests", issueId] as const,
};

export const giteaConnectionOptions = (wsId: string) =>
  queryOptions({
    queryKey: giteaKeys.connection(wsId),
    queryFn: () => api.getGiteaConnection(wsId),
    enabled: !!wsId,
  });

export const issueGiteaPullRequestsOptions = (issueId: string) =>
  queryOptions({
    queryKey: giteaKeys.pullRequests(issueId),
    queryFn: () => api.listIssueGiteaPullRequests(issueId),
    enabled: !!issueId,
  });
