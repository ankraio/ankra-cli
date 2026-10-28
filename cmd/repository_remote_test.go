package cmd

import "testing"

func TestParseRepositoryRemote(t *testing.T) {
	testCases := []struct {
		name      string
		remoteURL string
		want      repositoryRemote
		wantError bool
	}{
		{name: "github_https", remoteURL: "https://github.com/ankraio/ankra-cli.git",
			want: repositoryRemote{Provider: "github", Host: "github.com", Owner: "ankraio", Name: "ankra-cli"}},
		{name: "github_scp", remoteURL: "git@github.com:ankraio/ankra-cli.git",
			want: repositoryRemote{Provider: "github", Host: "github.com", Owner: "ankraio", Name: "ankra-cli"}},
		{name: "github_ssh_url", remoteURL: "ssh://git@github.com/ankraio/ankra-cli",
			want: repositoryRemote{Provider: "github", Host: "github.com", Owner: "ankraio", Name: "ankra-cli"}},
		{name: "github_case_insensitive_host", remoteURL: "https://GitHub.com/Acme/Shop-API",
			want: repositoryRemote{Provider: "github", Host: "github.com", Owner: "Acme", Name: "Shop-API"}},
		{name: "github_nested_refused", remoteURL: "https://github.com/acme/shop/extra", wantError: true},
		{name: "gitlab_https", remoteURL: "https://gitlab.com/acme/shop.git",
			want: repositoryRemote{Provider: "gitlab", Host: "gitlab.com", Owner: "acme", Name: "shop"}},
		{name: "gitlab_nested_groups", remoteURL: "git@gitlab.com:acme/platform/backend/shop.git",
			want: repositoryRemote{Provider: "gitlab", Host: "gitlab.com", Owner: "acme/platform/backend", Name: "shop"}},
		{name: "gitlab_web_url_suffix", remoteURL: "https://gitlab.com/acme/group/shop/-/tree/main",
			want: repositoryRemote{Provider: "gitlab", Host: "gitlab.com", Owner: "acme/group", Name: "shop"}},
		{name: "gitlab_self_managed", remoteURL: "https://gitlab.example.com/acme/shop.git",
			want: repositoryRemote{Provider: "gitlab", Host: "gitlab.example.com", Owner: "acme", Name: "shop"}},
		{name: "gitlab_single_segment_refused", remoteURL: "https://gitlab.com/shop.git", wantError: true},
		{name: "bitbucket_https_with_user", remoteURL: "https://jane@bitbucket.org/acme/shop.git",
			want: repositoryRemote{Provider: "bitbucket_cloud", Host: "bitbucket.org", Owner: "acme", Name: "shop"}},
		{name: "bitbucket_scp", remoteURL: "git@bitbucket.org:acme/shop.git",
			want: repositoryRemote{Provider: "bitbucket_cloud", Host: "bitbucket.org", Owner: "acme", Name: "shop"}},
		{name: "unknown_host", remoteURL: "https://git.example.com/acme/shop.git", wantError: true},
		{name: "unsupported_scheme", remoteURL: "ftp://github.com/acme/shop", wantError: true},
		{name: "empty", remoteURL: "  ", wantError: true},
		{name: "local_path", remoteURL: "/srv/git/shop.git", wantError: true},
		{name: "dot_segments", remoteURL: "https://github.com/../shop", wantError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			remote, parseError := parseRepositoryRemote(testCase.remoteURL)
			if testCase.wantError {
				if parseError == nil {
					t.Fatalf("parseRepositoryRemote(%q) = %+v, want an error", testCase.remoteURL, remote)
				}
				return
			}
			if parseError != nil {
				t.Fatalf("parseRepositoryRemote(%q) error = %v", testCase.remoteURL, parseError)
			}
			if remote != testCase.want {
				t.Fatalf("parseRepositoryRemote(%q) = %+v, want %+v", testCase.remoteURL, remote, testCase.want)
			}
		})
	}
}

// TestParseGitHubRepositoryRemoteStillGitHubOnly pins that applications keep
// reading only github.com remotes now that the parser underneath knows more.
func TestParseGitHubRepositoryRemoteStillGitHubOnly(t *testing.T) {
	for _, remoteURL := range []string{
		"git@gitlab.com:acme/shop.git",
		"https://bitbucket.org/acme/shop.git",
		"ftp://github.com/acme/shop",
		"",
	} {
		if owner, name, parseError := parseGitHubRepositoryRemote(remoteURL); parseError == nil {
			t.Errorf("parseGitHubRepositoryRemote(%q) = %s/%s, want an error", remoteURL, owner, name)
		}
	}
	owner, name, parseError := parseGitHubRepositoryRemote("git@github.com:acme/shop.git")
	if parseError != nil || owner != "acme" || name != "shop" {
		t.Fatalf("parseGitHubRepositoryRemote() = %s/%s, %v", owner, name, parseError)
	}
}
