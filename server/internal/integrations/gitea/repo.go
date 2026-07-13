// Package gitea is the Gitea integration: a bring-your-own Personal/Bot
// Access Token (PAT) connection per workspace, mirrored pull request state,
// and issue auto-link/auto-close from pull_request webhooks. Unlike GitHub
// (an App-marketplace install with ephemeral installation tokens), Gitea has
// no App-install concept, so this mirrors the Slack BYO pattern instead
// (server/internal/integrations/slack/byo_install.go): the workspace admin
// pastes a PAT, we validate it live and store it encrypted at rest.
//
// This package is fully independent of server/internal/handler/github.go and
// the github_* tables — kept that way deliberately so this fork's Gitea
// support never conflicts with upstream GitHub changes.
package gitea

import (
	"net/url"
	"strings"
)

// splitHostAndPath extracts the host and path-with-namespace from a git
// remote URL. Mirrors repocache.splitHostAndPath's logic (kept local rather
// than exported from that package, to avoid coupling this integration to the
// daemon tree) — handles URL form (https://host/path,
// ssh://user@host:port/path) and scp-style shorthand ([user@]host:path).
func splitHostAndPath(rawURL string) (host, path string) {
	if u, err := url.Parse(rawURL); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Host, strings.TrimPrefix(u.Path, "/")
	}
	s := rawURL
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

// ParseRepoURL extracts (owner, name) from a git remote URL, accepting both
// full URL form and scp-style shorthand. Returns ok=false when the URL
// doesn't carry at least two path segments (owner + repo name).
func ParseRepoURL(rawURL string) (owner, name string, ok bool) {
	_, path := splitHostAndPath(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	path = strings.TrimSuffix(path, ".git")
	segs := make([]string, 0, 2)
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	if len(segs) < 2 {
		return "", "", false
	}
	return segs[len(segs)-2], segs[len(segs)-1], true
}

// HostMatches reports whether rawURL's host (hostname only, port ignored —
// SSH clone URLs and the HTTPS base URL commonly differ in port) matches the
// configured Gitea instance's hostname. Used to filter workspace.repos down
// to the ones that live on this workspace's connected Gitea instance.
func HostMatches(rawURL, baseURL string) bool {
	host, _ := splitHostAndPath(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	host = stripPort(host)
	base, err := url.Parse(baseURL)
	if err != nil || base.Hostname() == "" || host == "" {
		return false
	}
	return strings.EqualFold(host, base.Hostname())
}
