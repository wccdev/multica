// Mirrors server/internal/integrations/gitea/repo.go's HostMatches — used by
// the UI to decide whether a repo URL belongs to the connected Gitea
// instance (render the Gitea icon / attach as a gitea_repo project resource)
// or should fall back to GitHub. Pure host matching only, no reachability
// check.

function splitHostAndPath(rawUrl: string): { host: string; path: string } {
  try {
    const u = new URL(rawUrl);
    if (u.protocol && u.host) {
      return { host: u.host, path: u.pathname.replace(/^\//, "") };
    }
  } catch {
    // Not a parseable absolute URL (e.g. scp-style shorthand) — fall through
    // to the manual split below.
  }
  let s = rawUrl;
  const at = s.indexOf("@");
  if (at >= 0) s = s.slice(at + 1);
  const colon = s.indexOf(":");
  if (colon >= 0) {
    return { host: s.slice(0, colon), path: s.slice(colon + 1) };
  }
  return { host: "", path: s };
}

function stripPort(host: string): string {
  const idx = host.lastIndexOf(":");
  return idx >= 0 ? host.slice(0, idx) : host;
}

/**
 * Reports whether repoUrl's host (hostname only, port ignored — SSH clone
 * URLs and the HTTPS base URL commonly differ in port) matches the
 * configured Gitea instance's hostname. Accepts the same URL forms as the
 * backend: https://host/path, ssh://user@host:port/path, and scp-style
 * user@host:path shorthand.
 */
export function giteaHostMatches(repoUrl: string, baseUrl: string): boolean {
  const trimmed = repoUrl.trim().replace(/\/+$/, "");
  if (!trimmed || !baseUrl) return false;
  const { host } = splitHostAndPath(trimmed);
  const repoHost = stripPort(host).toLowerCase();
  if (!repoHost) return false;
  try {
    const base = new URL(baseUrl);
    return !!base.hostname && repoHost === base.hostname.toLowerCase();
  } catch {
    return false;
  }
}
