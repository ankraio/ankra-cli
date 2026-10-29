package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The project a command acts in (ADR 0022) is decided in this order: the
// global --project flag, ANKRA_PROJECT, the selection `ankra project switch`
// saved beside the organisation selection, and otherwise the member's
// server-side current project, which the platform applies on its own. The
// first three are sent as X-Ankra-Project-Id on every request of the
// invocation; the platform ignores the header while the organisation has
// projects off, so a saved selection never breaks an organisation that
// turned them off again.

// projectPinnedExplicitly is true when this invocation's project came from
// --project or ANKRA_PROJECT. An explicit pin is a statement about which
// project to use, so the gateway commands never retry a cluster in another
// project behind its back.
var projectPinnedExplicitly bool

// envAnkraProject is the environment twin of the global --project flag.
const envAnkraProject = "ANKRA_PROJECT"

// annotationIgnoresLocalProject marks a command that must reach the API
// without the saved project selection: the commands that list, show or
// change the selection itself, so a selection that no longer exists can
// always be inspected and replaced.
const annotationIgnoresLocalProject = "ankra.ignores_local_project"

// Where a command's project came from, as `ankra project current` reports it.
const (
	projectSourceOverride = "override"
	projectSourceLocal    = "local"
	projectSourceServer   = "server"
)

// SelectedProject is the selection `ankra project switch` saves. The
// organisation id pins it to the organisation it was made in, so switching
// organisation never carries a project of the old one along.
type SelectedProject struct {
	ProjectID      string `json:"project_id"`
	Name           string `json:"name,omitempty"`
	Slug           string `json:"slug,omitempty"`
	OrganisationID string `json:"organisation_id,omitempty"`
}

// errProjectNotFound marks a reference that matches no project of the
// organisation, so the commands exit exitNotFound (3); an ambiguous reference
// stays a fixable usage error (2).
var errProjectNotFound = errors.New("project not found")

// uuidPattern recognises a project id, which the override header takes as
// is; anything else is a slug or a name and is resolved against the listing.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isLikelyUUID(value string) bool {
	return uuidPattern.MatchString(strings.TrimSpace(value))
}

// selectedProjectFile is the selection's path: beside the organisation
// selection under ~/.ankra, or beside an explicit --config file, following the
// selectedClusterFile rule so a custom config fully isolates CLI state.
func selectedProjectFile() (string, error) {
	if cfgFile != "" {
		absConfig, err := filepath.Abs(cfgFile)
		if err != nil {
			absConfig = cfgFile
		}
		if err := os.MkdirAll(filepath.Dir(absConfig), 0700); err != nil {
			return "", err
		}
		return absConfig + ".project.json", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ankra")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "project.json"), nil
}

func saveSelectedProject(selected SelectedProject) error {
	path, err := selectedProjectFile()
	if err != nil {
		return err
	}
	data, err := json.Marshal(selected)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func loadSelectedProject() (SelectedProject, error) {
	var selected SelectedProject
	path, err := selectedProjectFile()
	if err != nil {
		return selected, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return selected, err
	}
	err = json.Unmarshal(data, &selected)
	return selected, err
}

// clearSelectedProject forgets the saved project selection. A selection that
// was never made is not an error.
func clearSelectedProject() error {
	path, err := selectedProjectFile()
	if err != nil {
		return err
	}
	if removeError := os.Remove(path); removeError != nil && !errors.Is(removeError, os.ErrNotExist) {
		return removeError
	}
	return nil
}

func setIgnoresLocalProject(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[annotationIgnoresLocalProject] = "true"
}

// projectOverrideValue returns the project requested for this invocation via
// the global --project flag, falling back to ANKRA_PROJECT. An empty string
// means no override was supplied.
func projectOverrideValue(cmd *cobra.Command) string {
	if flag, changed := flagValue(cmd.Root().PersistentFlags().Lookup("project")); changed && strings.TrimSpace(flag) != "" {
		return strings.TrimSpace(flag)
	}
	return strings.TrimSpace(os.Getenv(envAnkraProject))
}

// applyProjectOverride scopes every API request of this invocation to a
// project, without moving the member's server-side current project. The
// explicit --project / ANKRA_PROJECT wins; a slug or name is resolved once
// against the organisation's projects and an id is sent as is. Without an
// override, the selection `ankra project switch` saved applies, but only
// alongside the saved `ankra login` token and only when it was made in the
// organisation this invocation runs in.
func applyProjectOverride(cmd *cobra.Command, tokenSource credentialSource) error {
	projectPinnedExplicitly = false
	if override := projectOverrideValue(cmd); override != "" {
		projectID, err := resolveProjectReferenceToID(override)
		if err != nil {
			return err
		}
		apiClient.SetProjectOverride(projectID)
		projectPinnedExplicitly = true
		return nil
	}
	if tokenSource != sourceConfigFile || cmd.Annotations[annotationIgnoresLocalProject] == "true" {
		return nil
	}
	selected, err := loadSelectedProject()
	if err != nil || strings.TrimSpace(selected.ProjectID) == "" {
		return nil
	}
	if !selectedProjectMatchesOrganisation(selected) {
		return nil
	}
	apiClient.SetProjectOverride(strings.TrimSpace(selected.ProjectID))
	return nil
}

// selectedProjectMatchesOrganisation says whether the saved project selection
// was made in the organisation this invocation runs in: the organisation
// override when one is set (--org, ANKRA_ORG or the saved organisation
// selection), else the saved organisation selection itself. When neither
// names an organisation (the platform's current one applies) the selection
// is trusted, as is a selection saved without an organisation: the platform
// refuses a project of another organisation with a 403, never a silent
// wrong scope, so no request is spent finding out here.
func selectedProjectMatchesOrganisation(selected SelectedProject) bool {
	savedOrganisation := strings.TrimSpace(selected.OrganisationID)
	if savedOrganisation == "" {
		return true
	}
	currentOrganisation := strings.TrimSpace(apiClient.OrganisationOverride())
	if currentOrganisation == "" {
		if organisation, loadError := loadSelectedOrganisation(); loadError == nil {
			currentOrganisation = strings.TrimSpace(organisation.OrganisationID)
		}
	}
	if currentOrganisation == "" {
		return true
	}
	return savedOrganisation == currentOrganisation
}

// resolveProjectReferenceToID maps an id, slug or name to a project id. An id
// is taken as is without a request, so a token pinned to a project (which the
// listing may refuse) can still name its own project.
func resolveProjectReferenceToID(reference string) (string, error) {
	trimmed := strings.TrimSpace(reference)
	if isLikelyUUID(trimmed) {
		return strings.ToLower(trimmed), nil
	}
	projects, err := apiClient.ListProjects()
	if err != nil {
		return "", projectsCommandError(fmt.Errorf("resolving project %q: %w", reference, err))
	}
	project, resolveError := resolveProjectReference(projects, trimmed)
	if resolveError != nil {
		if errors.Is(resolveError, errProjectNotFound) {
			return "", withExitCode(exitNotFound, resolveError)
		}
		return "", withExitCode(exitUsage, resolveError)
	}
	return project.ID, nil
}

// resolveProjectReference maps a user-supplied reference (an id, a slug, or
// a name) to the matching project. Resolution order is exact id, then slug,
// then name; slug and name comparisons are case-insensitive. Unknown and
// ambiguous references return an actionable error listing the projects.
func resolveProjectReference(projects []client.ProjectSummary, reference string) (*client.ProjectSummary, error) {
	trimmed := strings.TrimSpace(reference)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: an empty reference names no project. %s", errProjectNotFound, availableProjectsHint(projects))
	}
	for index := range projects {
		if strings.EqualFold(projects[index].ID, trimmed) {
			return &projects[index], nil
		}
	}
	for index := range projects {
		if strings.EqualFold(strings.TrimSpace(projects[index].Slug), trimmed) {
			return &projects[index], nil
		}
	}
	var nameMatches []*client.ProjectSummary
	for index := range projects {
		if strings.EqualFold(strings.TrimSpace(projects[index].Name), trimmed) {
			nameMatches = append(nameMatches, &projects[index])
		}
	}
	switch len(nameMatches) {
	case 1:
		return nameMatches[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %q. %s", errProjectNotFound, reference, availableProjectsHint(projects))
	default:
		return nil, fmt.Errorf("multiple projects are named %q; pass the project slug or id instead", reference)
	}
}

func availableProjectsHint(projects []client.ProjectSummary) string {
	if len(projects) == 0 {
		return "This organisation has no projects you can see."
	}
	labels := make([]string, 0, len(projects))
	for _, project := range projects {
		labels = append(labels, projectLabel(project))
	}
	return "Available projects: " + strings.Join(labels, ", ")
}

// projectLabel renders a project as name and slug, the two things a person
// types; the id is what scripts pass.
func projectLabel(project client.ProjectSummary) string {
	switch {
	case project.Name != "" && project.Slug != "":
		return fmt.Sprintf("%s (%s)", project.Name, project.Slug)
	case project.Slug != "":
		return project.Slug
	case project.Name != "":
		return project.Name
	default:
		return project.ID
	}
}

// projectsCommandError classifies the project routes' answers for the exit
// code contract: an organisation with projects off is a missing target (3)
// with a sentence saying so, a platform that predates projects is a plain
// failure (1) naming the route, and a project the organisation does not
// have is a missing target (3). Everything else passes through with the
// standard classification (401 -> 6, RBAC 403 -> 7).
func projectsCommandError(err error) error {
	switch {
	case errors.Is(err, client.ErrProjectsDisabled):
		return withExitCode(exitNotFound, fmt.Errorf("%w. Projects are turned on per organisation by Ankra; "+
			"until then every resource lives in the organisation's default project and this command has nothing to act on", err))
	case errors.Is(err, client.ErrProjectsRouteMissing):
		return withExitCode(exitError, err)
	case errors.Is(err, client.ErrProjectNotFound):
		return withExitCode(exitNotFound, err)
	}
	return err
}

// resolveTargetProject resolves which project a command acts in and the
// source of that decision, in the precedence applyProjectOverride applies:
// the explicit override, then the saved selection (when it still names a
// project of this organisation), then the server-side current project.
func resolveTargetProject(cmd *cobra.Command) (client.ProjectSummary, string, error) {
	projects, err := apiClient.ListProjects()
	if err != nil {
		return client.ProjectSummary{}, "", projectsCommandError(fmt.Errorf("listing projects: %w", err))
	}
	if override := projectOverrideValue(cmd); override != "" {
		project, resolveError := resolveProjectReference(projects, override)
		if resolveError != nil {
			if errors.Is(resolveError, errProjectNotFound) {
				return client.ProjectSummary{}, "", withExitCode(exitNotFound, resolveError)
			}
			return client.ProjectSummary{}, "", withExitCode(exitUsage, resolveError)
		}
		return *project, projectSourceOverride, nil
	}
	if selected, loadError := loadSelectedProject(); loadError == nil && selected.ProjectID != "" && selectedProjectMatchesOrganisation(selected) {
		for _, project := range projects {
			if strings.EqualFold(project.ID, strings.TrimSpace(selected.ProjectID)) {
				return project, projectSourceLocal, nil
			}
		}
	}
	for _, project := range projects {
		if project.IsCurrent {
			return project, projectSourceServer, nil
		}
	}
	current, currentError := apiClient.GetCurrentProject()
	if currentError != nil {
		return client.ProjectSummary{}, "", projectsCommandError(fmt.Errorf("reading the current project: %w", currentError))
	}
	return *current, projectSourceServer, nil
}

// selectedProjectIDForLinks is the project a portal link should open in: the
// one this invocation is scoped to, or none.
func selectedProjectIDForLinks() string {
	if apiClient == nil {
		return ""
	}
	return strings.TrimSpace(apiClient.ProjectOverride())
}
