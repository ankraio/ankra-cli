package cmd

import (
	"errors"
	"net/url"
	"strings"
)

// The source-control providers a pipeline repository is stored under
// (cluster enginekit/pipelinerun Provider*).
const (
	repositoryProviderGitHub    = "github"
	repositoryProviderGitLab    = "gitlab"
	repositoryProviderBitbucket = "bitbucket_cloud"
)

// repositoryRemote is the repository a git remote names, in the vocabulary
// the platform stores pipeline repositories under.
type repositoryRemote struct {
	Provider string
	Host     string
	Owner    string
	Name     string
}

// FullName is "owner/name".
func (remote repositoryRemote) FullName() string {
	return remote.Owner + "/" + remote.Name
}

// repositoryProviderForHost maps a remote's host onto its provider:
// github.com, gitlab.com (and a self-managed GitLab whose host names it, such
// as gitlab.example.com) and bitbucket.org. Any other host answers "".
func repositoryProviderForHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case host == "github.com" || host == "www.github.com" || host == "ssh.github.com":
		return repositoryProviderGitHub
	case host == "gitlab.com" || host == "www.gitlab.com" || host == "altssh.gitlab.com" ||
		strings.HasPrefix(host, "gitlab."):
		return repositoryProviderGitLab
	case host == "bitbucket.org" || host == "www.bitbucket.org" || host == "altssh.bitbucket.org":
		return repositoryProviderBitbucket
	}
	return ""
}

// The ways a remote URL is refused before its path is read.
var (
	errRemoteURLEmpty          = errors.New("remote URL is empty")
	errRemoteSchemeUnsupported = errors.New("remote uses an unsupported URL scheme")
	errRemoteHostUnsupported   = errors.New("remote is not a github.com, gitlab.com or bitbucket.org repository")
	errRemoteURLUnrecognised   = errors.New("remote URL is not a host:path or scheme URL")
)

// splitRemoteURL answers the host and repository path of a git remote URL:
// a scheme URL (https://, ssh://, git://) or an scp-like one (git@host:path).
func splitRemoteURL(remoteURL string) (string, string, error) {
	trimmedRemoteURL := strings.TrimSpace(remoteURL)
	if trimmedRemoteURL == "" {
		return "", "", errRemoteURLEmpty
	}
	if strings.Contains(trimmedRemoteURL, "://") {
		parsedURL, parseError := url.Parse(trimmedRemoteURL)
		if parseError != nil {
			return "", "", errRemoteURLUnrecognised
		}
		switch strings.ToLower(parsedURL.Scheme) {
		case "git", "http", "https", "ssh", "git+ssh", "ssh+git":
		default:
			return "", "", errRemoteSchemeUnsupported
		}
		return parsedURL.Hostname(), parsedURL.EscapedPath(), nil
	}
	separatorIndex := strings.Index(trimmedRemoteURL, ":")
	if separatorIndex <= 0 {
		return "", "", errRemoteURLUnrecognised
	}
	hostPart := trimmedRemoteURL[:separatorIndex]
	if userSeparatorIndex := strings.LastIndex(hostPart, "@"); userSeparatorIndex >= 0 {
		hostPart = hostPart[userSeparatorIndex+1:]
	}
	return hostPart, trimmedRemoteURL[separatorIndex+1:], nil
}

// parseRepositoryRemote reads the provider, owner and name off a git remote
// URL. GitHub and Bitbucket repositories are exactly owner/name; a GitLab
// project may sit in nested groups, and its owner is the whole namespace
// ("group/subgroup"), as the platform stores it.
func parseRepositoryRemote(remoteURL string) (repositoryRemote, error) {
	host, repositoryPath, splitError := splitRemoteURL(remoteURL)
	if splitError != nil {
		return repositoryRemote{}, splitError
	}
	provider := repositoryProviderForHost(host)
	if provider == "" {
		return repositoryRemote{}, errRemoteHostUnsupported
	}
	decodedPath, decodeError := url.PathUnescape(repositoryPath)
	if decodeError != nil {
		return repositoryRemote{}, errors.New("remote repository path is invalid")
	}
	pathParts := strings.Split(strings.Trim(decodedPath, "/"), "/")
	for index := range pathParts {
		pathParts[index] = strings.TrimSpace(pathParts[index])
	}
	if len(pathParts) > 0 {
		pathParts[len(pathParts)-1] = strings.TrimSuffix(pathParts[len(pathParts)-1], ".git")
	}
	if provider == repositoryProviderGitLab {
		// A GitLab URL may carry /-/... after the project; the project path
		// is what precedes it.
		for index, part := range pathParts {
			if part == "-" {
				pathParts = pathParts[:index]
				break
			}
		}
	}
	if len(pathParts) < 2 || (provider != repositoryProviderGitLab && len(pathParts) != 2) {
		return repositoryRemote{}, errors.New("remote must identify a repository as owner/name")
	}
	for _, part := range pathParts {
		if part == "" || part == "." || part == ".." {
			return repositoryRemote{}, errors.New("remote must identify a repository as owner/name")
		}
	}
	return repositoryRemote{
		Provider: provider,
		Host:     strings.ToLower(host),
		Owner:    strings.Join(pathParts[:len(pathParts)-1], "/"),
		Name:     pathParts[len(pathParts)-1],
	}, nil
}
