package client

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// AppliedProjectHeader is the response header the platform answers a request
// with: the id of the project the request was scoped to, or
// appliedProjectNone on a read that took no project scope. Absent says
// nothing: a platform older than the header, an organisation with projects off
// (which ignores the override by design), or a write that recorded no project.
const AppliedProjectHeader = "X-Ankra-Applied-Project"

const appliedProjectNone = "none"

// envProjectScopeWarning set to off silences the warning, the way
// ANKRA_TELEMETRY=off silences attribution.
const envProjectScopeWarning = "ANKRA_PROJECT_SCOPE_WARNING"

// organisationLevelPathPrefixes are the routes that belong to the organisation
// by design (ADR 0022: people, money, the Harbor project, AI providers, support
// tickets), which a project-scoped command can still read in passing, such as
// resolving the current organisation. A route answering for the organisation
// here is not news.
var organisationLevelPathPrefixes = []string{
	"/api/v1/org/account",
	"/api/v1/org/ai-settings",
	"/api/v1/org/billing",
	"/api/v1/org/organisation",
	"/api/v1/org/projects",
	"/api/v1/org/registry-projects",
	"/api/v1/org/registry-robots",
	"/api/v1/org/registry-storage",
	"/api/v1/org/secret-slots",
	"/api/v1/org/support",
}

// projectScopeWarning prints, at most once per invocation, a one-line stderr
// warning that a read ignored the project the user selected. Without it a
// command run with --project answers with the whole organisation's rows and
// nothing says so. It writes to stderr only, so `-o json` stays parseable.
type projectScopeWarning struct {
	once              sync.Once
	output            io.Writer
	lookupEnvironment func(string) string
}

func newProjectScopeWarning() *projectScopeWarning {
	return &projectScopeWarning{output: os.Stderr, lookupEnvironment: os.Getenv}
}

// observe warns when a successful read sent with a project override says the
// project was not applied. A write is never warned about: the platform only
// answers none on a read.
func (warning *projectScopeWarning) observe(request *http.Request, response *http.Response, projectID string) {
	if warning == nil || request == nil || response == nil || projectID == "" {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(response.Header.Get(AppliedProjectHeader)), appliedProjectNone) {
		return
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return
	}
	if strings.EqualFold(strings.TrimSpace(warning.lookupEnvironment(envProjectScopeWarning)), "off") {
		return
	}
	if invocationIsOrganisationLevel() || pathIsOrganisationLevel(request.URL.Path) {
		return
	}
	if request.URL.Query().Get("project_id") == CostProjectScopeAll {
		return
	}
	warning.once.Do(func() {
		_, _ = fmt.Fprintln(warning.output, projectScopeWarningLine(request.Method, request.URL.Path, projectID))
	})
}

func projectScopeWarningLine(method string, path string, projectID string) string {
	return fmt.Sprintf("Warning: project %s was not applied to %s %s, so this answer may cover the whole organisation. "+
		"Set %s=off to silence this.", projectID, method, path, envProjectScopeWarning)
}

func pathIsOrganisationLevel(path string) bool {
	for _, prefix := range organisationLevelPathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
