package gitea

import "testing"

func TestParseRepoURL(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		wantOwner string
		wantName  string
		wantOK    bool
	}{
		{"https", "https://gitea.internal.example.com/myorg/myrepo.git", "myorg", "myrepo", true},
		{"https_no_git_suffix", "https://gitea.internal.example.com/myorg/myrepo", "myorg", "myrepo", true},
		{"https_trailing_slash", "https://gitea.internal.example.com/myorg/myrepo/", "myorg", "myrepo", true},
		{"scp_shorthand", "git@gitea.internal.example.com:myorg/myrepo.git", "myorg", "myrepo", true},
		{"ssh_url_with_port", "ssh://git@gitea.internal.example.com:2222/myorg/myrepo.git", "myorg", "myrepo", true},
		{"too_few_segments", "https://gitea.internal.example.com/myrepo", "", "", false},
		{"bare_name", "myrepo", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, name, ok := ParseRepoURL(tc.url)
			if ok != tc.wantOK {
				t.Fatalf("ParseRepoURL(%q) ok = %v, want %v", tc.url, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Errorf("ParseRepoURL(%q) = (%q, %q), want (%q, %q)", tc.url, owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

func TestHostMatches(t *testing.T) {
	base := "https://gitea.internal.example.com"
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"https_same_host", "https://gitea.internal.example.com/org/repo.git", true},
		{"scp_same_host", "git@gitea.internal.example.com:org/repo.git", true},
		{"ssh_same_host_different_port", "ssh://git@gitea.internal.example.com:2222/org/repo.git", true},
		{"different_host", "https://github.com/org/repo.git", false},
		{"case_insensitive", "https://GITEA.internal.example.com/org/repo.git", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HostMatches(tc.url, base); got != tc.want {
				t.Errorf("HostMatches(%q, %q) = %v, want %v", tc.url, base, got, tc.want)
			}
		})
	}
}
