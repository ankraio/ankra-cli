package cmd

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// projectSlugMaximum is the longest slug the platform accepts.
const projectSlugMaximum = 63

// projectOrganisationWideDestination is the --to value that makes a shared
// resource visible in every project of the organisation.
const projectOrganisationWideDestination = "org-wide"

// projectMoveKinds lists the resource kinds a move accepts, as the platform
// names them. The strict kinds belong to exactly one project; the shared
// kinds may also be organisation-wide.
var (
	projectMoveStrictKinds = []string{"cluster", "application", "environment", "stack_profile", "pipeline_repository", "ai_automation", "ai_workspace"}
	projectMoveSharedKinds = []string{"credential", "registry", "backup_vault", "object_storage_bucket", "organisation_variable", "notification_route", "dns_zone"}
)

var projectCmd = &cobra.Command{
	Use:     "project",
	Aliases: []string{"projects"},
	Short:   "Manage the organisation's projects: a team-sized scope for clusters, applications and cost",
	Long: `Manage the organisation's projects.

A project is a scope inside an organisation. Clusters, applications,
environments, stack profiles, pipeline repositories and AI automations each
belong to exactly one project; credentials, registries, backup vaults,
buckets, organisation variables, notification routes and DNS zones belong to
one project or are organisation-wide (visible in every project). Members,
roles, billing and settings stay organisation-wide. Every organisation has a
default project, and until an administrator creates another one nothing
changes.

Which project a command acts in: the global --project <id|slug> flag (or
ANKRA_PROJECT) for one invocation, else the selection 'ankra project switch'
saves on this machine, else the current project the platform remembers for
you (the one the portal shows). 'ankra project current' says which applies.

Not to be confused with 'ankra registry projects', which are the projects of
the organisation's container registry (a Harbor concept).

Projects are turned on per organisation. While they are off, every project
command exits 3 saying so.`,
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's projects, the current one marked",
	Args:  cobra.NoArgs,
	Example: `  ankra project list
  ankra project list -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projects, err := apiClient.ListProjects()
		if err != nil {
			return projectsCommandError(fmt.Errorf("listing projects: %w", err))
		}
		if rendered, err := renderStructured(cmd, projects); rendered || err != nil {
			return err
		}
		renderProjectTable(cmd.OutOrStdout(), projects)
		return nil
	},
}

type currentProjectOutput struct {
	Project client.ProjectSummary `json:"project" yaml:"project"`
	Source  string                `json:"source" yaml:"source"`
}

var projectCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the project this CLI acts in, and why (flag, saved selection or the platform's current project)",
	Args:  cobra.NoArgs,
	Example: `  ankra project current
  ankra project current -o json
  ankra --project platform-team project current`,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, source, err := resolveTargetProject(cmd)
		if err != nil {
			return err
		}
		if rendered, err := renderStructured(cmd, currentProjectOutput{Project: project, Source: source}); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Current project (%s):\n", source)
		renderProjectDetail(out, project)
		return nil
	},
}

var projectSwitchCmd = &cobra.Command{
	Use:   "switch <project>",
	Short: "Switch to another project by slug, name or id",
	Long: `Switch to another project of the organisation. The platform's current project
moves with you (the portal follows), and the choice is saved on this machine
beside the organisation selection, so later commands act in it until you
switch again or run 'ankra org switch'. A token pinned to a project cannot
switch; pass --project for a single command instead.`,
	Args: cobra.ExactArgs(1),
	Example: `  ankra project switch platform-team
  ankra project switch 071d45b1-f8fe-4f47-b13a-4f13a01689f1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projects, err := apiClient.ListProjects()
		if err != nil {
			return projectsCommandError(fmt.Errorf("listing projects: %w", err))
		}
		target, resolveError := resolveProjectReference(projects, args[0])
		if resolveError != nil {
			if errors.Is(resolveError, errProjectNotFound) {
				return withExitCode(exitNotFound, resolveError)
			}
			return withExitCode(exitUsage, resolveError)
		}
		response, switchError := apiClient.SwitchProject(target.ID)
		if switchError != nil {
			return projectsCommandError(fmt.Errorf("switching project: %w", switchError))
		}
		selected := SelectedProject{
			ProjectID:      target.ID,
			Name:           target.Name,
			Slug:           target.Slug,
			OrganisationID: effectiveOrganisationID(),
		}
		if saveError := saveSelectedProject(selected); saveError != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: switched server-side but failed to save locally: %v\n", saveError)
		}
		if rendered, err := renderStructured(cmd, response); rendered || err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Switched to project: %s (%s)\n", projectLabel(*target), target.ID)
		if response.Message != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Message: %s\n", response.Message)
		}
		return nil
	},
}

var projectCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a project (needs projects.manage)",
	Long: `Create a project. The slug is derived from the name unless --slug names one:
lowercase letters, digits and hyphens, at most 63 characters. It never
changes afterwards - it ends up in kubeconfig entries, CLI selections and
deep links - so pick it deliberately.`,
	Args: cobra.ExactArgs(1),
	Example: `  ankra project create "Platform team"
  ankra project create "Platform team" --slug platform --description "Shared infrastructure clusters"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return withExitCode(exitUsage, errors.New("the project needs a name"))
		}
		slug, _ := cmd.Flags().GetString("slug")
		slug = strings.ToLower(strings.TrimSpace(slug))
		if slug == "" {
			slug = projectSlugFromName(name)
		}
		if !isValidProjectSlug(slug) {
			return withExitCode(exitUsage, fmt.Errorf("slug %q must be lowercase letters, digits and hyphens, at most %d characters; pass --slug", slug, projectSlugMaximum))
		}
		description, _ := cmd.Flags().GetString("description")
		created, err := apiClient.CreateProject(client.CreateProjectRequest{
			Name:        name,
			Slug:        slug,
			Description: strings.TrimSpace(description),
		})
		if err != nil {
			return projectsCommandError(fmt.Errorf("creating project: %w", err))
		}
		if rendered, err := renderStructured(cmd, created); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "Project created.")
		renderProjectDetail(out, *created)
		_, _ = fmt.Fprintf(out, "\nTo act in it: ankra project switch %s (or --project %s on one command)\n", created.Slug, created.Slug)
		return nil
	},
}

var projectGetCmd = &cobra.Command{
	Use:   "get <project>",
	Short: "Show one project by slug, name or id",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra project get platform-team
  ankra project get platform-team -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := lookUpProject(args[0])
		if err != nil {
			return err
		}
		if rendered, err := renderStructured(cmd, project); rendered || err != nil {
			return err
		}
		renderProjectDetail(cmd.OutOrStdout(), *project)
		return nil
	},
}

var projectRenameCmd = &cobra.Command{
	Use:   "rename <project> <new-name>",
	Short: "Rename a project, and optionally change its description (needs projects.manage)",
	Long: `Rename a project. The slug never changes; pass --description to change the
description in the same call.`,
	Args: cobra.ExactArgs(2),
	Example: `  ankra project rename platform-team "Platform engineering"
  ankra project rename platform-team "Platform engineering" --description "Shared clusters"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := lookUpProject(args[0])
		if err != nil {
			return err
		}
		newName := strings.TrimSpace(args[1])
		if newName == "" {
			return withExitCode(exitUsage, errors.New("the new name must not be empty"))
		}
		request := client.UpdateProjectRequest{Name: &newName}
		if cmd.Flags().Changed("description") {
			description, _ := cmd.Flags().GetString("description")
			description = strings.TrimSpace(description)
			request.Description = &description
		}
		updated, updateError := apiClient.UpdateProject(project.ID, request)
		if updateError != nil {
			return projectsCommandError(fmt.Errorf("renaming project: %w", updateError))
		}
		if rendered, err := renderStructured(cmd, updated); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "Project updated.")
		renderProjectDetail(out, *updated)
		return nil
	},
}

var projectDeleteCmd = &cobra.Command{
	Use:   "delete <project>",
	Short: "Delete an empty project (needs projects.manage)",
	Long: `Delete a project. The default project cannot be deleted, and a project that
still holds resources is refused: move them with 'ankra project move' (or make
the shared ones organisation-wide) first. Members whose current project it was
are pointed back at the default project; tokens pinned to it stop working.`,
	Args: cobra.ExactArgs(1),
	Example: `  ankra project delete old-team
  ankra project delete old-team --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := lookUpProject(args[0])
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete project %s (%s)? Tokens pinned to it stop working. [y/N]: ", projectLabel(*project), project.ID), yes); confirmError != nil {
			return confirmError
		}
		response, deleteError := apiClient.DeleteProject(project.ID)
		if deleteError != nil {
			return projectsCommandError(fmt.Errorf("deleting project: %w", deleteError))
		}
		if selected, loadError := loadSelectedProject(); loadError == nil && strings.EqualFold(selected.ProjectID, project.ID) {
			_ = clearSelectedProject()
		}
		if rendered, err := renderStructured(cmd, response); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Project %s deleted.\n", projectLabel(*project))
		if response.RetiredAssignments > 0 || response.RepointedMembers > 0 || response.PinnedTokens > 0 {
			_, _ = fmt.Fprintf(out, "  %d project-scoped role assignment(s) retired, %d member(s) pointed back at the default project, %d pinned token(s) no longer work.\n",
				response.RetiredAssignments, response.RepointedMembers, response.PinnedTokens)
		}
		return nil
	},
}

var projectMembersCmd = &cobra.Command{
	Use:   "members <project>",
	Short: "List who reaches a project, and how (organisation role or project-scoped assignment)",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra project members platform-team
  ankra project members platform-team -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := lookUpProject(args[0])
		if err != nil {
			return err
		}
		members, listError := apiClient.ListProjectMembers(project.ID)
		if listError != nil {
			return projectsCommandError(fmt.Errorf("listing project members: %w", listError))
		}
		if rendered, err := renderStructured(cmd, members); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Members of project %s (%s):\n", projectLabel(*project), project.ID)
		if len(members) == 0 {
			_, _ = fmt.Fprintln(out, "No members reach this project.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Email", "Role", "Via", "User ID"})
		for _, member := range members {
			writer.AppendRow(table.Row{member.Email, member.RoleSlug, member.Via, member.AnkraUserID})
		}
		writer.Render()
		return nil
	},
}

var projectMoveCmd = &cobra.Command{
	Use:   "move --kind <kind> --id <id> --to <project|org-wide>",
	Short: "Move a resource to another project, or make a shared one organisation-wide",
	Long: `Move one resource to another project of the organisation (needs
projects.manage).

--kind names what moves: ` + strings.Join(projectMoveStrictKinds, ", ") + `
(each belongs to exactly one project), or ` + strings.Join(projectMoveSharedKinds, ", ") + `
(which may also be organisation-wide). --id is the resource's id; for a
cluster or an application its name works too. --to is the destination
project by slug, name or id, or '` + projectOrganisationWideDestination + `' to make a shared resource
visible in every project.

A cluster carries its DNS zones, incidents and tickets along; an application
carries its connected repositories; a stack profile carries its drafts. A
cluster with operations still running is refused until they finish, and a DNS
zone bound to a cluster follows that cluster instead of moving on its own.
Role assignments scoped to a project keep their project: a cluster that
leaves it is no longer reached by them.`,
	Args: cobra.NoArgs,
	Example: `  ankra project move --kind cluster --id prod-eu --to platform-team
  ankra project move --kind application --id 7d2a1c6e-... --to default
  ankra project move --kind credential --id 0b7c4d1e-... --to org-wide`,
	RunE: func(cmd *cobra.Command, args []string) error {
		kind, _ := cmd.Flags().GetString("kind")
		kind = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(kind, "-", "_")))
		resourceReference, _ := cmd.Flags().GetString("id")
		resourceReference = strings.TrimSpace(resourceReference)
		destination, _ := cmd.Flags().GetString("to")
		destination = strings.TrimSpace(destination)
		isShared := containsString(projectMoveSharedKinds, kind)
		if !isShared && !containsString(projectMoveStrictKinds, kind) {
			return withExitCode(exitUsage, fmt.Errorf("--kind must be one of %s", strings.Join(append(append([]string{}, projectMoveStrictKinds...), projectMoveSharedKinds...), ", ")))
		}
		if resourceReference == "" {
			return withExitCode(exitUsage, errors.New("--id names the resource to move"))
		}
		if destination == "" {
			return withExitCode(exitUsage, fmt.Errorf("--to names the destination project (slug, name or id), or %s for a shared resource", projectOrganisationWideDestination))
		}
		resourceID, resolveError := resolveProjectMoveResourceID(cmd, kind, resourceReference)
		if resolveError != nil {
			return resolveError
		}
		request := client.ProjectMoveRequest{ResourceKind: kind, ResourceID: resourceID}
		switch strings.ToLower(destination) {
		case projectOrganisationWideDestination, "organisation-wide", "organization-wide", "shared":
			if !isShared {
				return withExitCode(exitUsage, fmt.Errorf("a %s always belongs to one project; name the destination project with --to", strings.ReplaceAll(kind, "_", " ")))
			}
			request.OrganisationWide = true
		default:
			projectID, projectError := resolveProjectReferenceToID(destination)
			if projectError != nil {
				return projectError
			}
			request.ProjectID = projectID
		}
		moved, moveError := apiClient.MoveResourceToProject(request)
		if moveError != nil {
			var refused *client.ProjectMoveRefusedError
			if errors.As(moveError, &refused) {
				return withExitCode(exitError, fmt.Errorf("the platform refused the move (%s): %w", refused.Code, refused))
			}
			return projectsCommandError(fmt.Errorf("moving %s: %w", strings.ReplaceAll(kind, "_", " "), moveError))
		}
		if rendered, err := renderStructured(cmd, moved); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Moved %s %q (%s) to %s.\n", strings.ReplaceAll(moved.ResourceKind, "_", " "), moved.ResourceName, moved.ResourceID, projectDestinationLabel(moved.ToProjectID))
		for followedTable, count := range moved.Followed {
			if count > 0 {
				_, _ = fmt.Fprintf(out, "  %d row(s) of %s followed it.\n", count, followedTable)
			}
		}
		if moved.FollowedNotificationRoutes > 0 {
			_, _ = fmt.Fprintf(out, "  %d notification route(s) pinned to another project were made organisation-wide.\n", moved.FollowedNotificationRoutes)
		}
		return nil
	},
}

// resolveProjectMoveResourceID turns --id into the resource's id: a cluster
// or application name is looked up, anything else must already be an id.
func resolveProjectMoveResourceID(cmd *cobra.Command, kind string, reference string) (string, error) {
	if isLikelyUUID(reference) {
		return strings.ToLower(reference), nil
	}
	switch kind {
	case "cluster":
		return resolveClusterID(reference)
	case "application":
		return resolveApplicationID(cmd.Context(), apiClient, reference)
	default:
		return "", withExitCode(exitUsage, fmt.Errorf("--id must be the %s's id (a UUID), not %q", strings.ReplaceAll(kind, "_", " "), reference))
	}
}

func projectDestinationLabel(projectID *string) string {
	if projectID == nil || *projectID == "" {
		return "organisation-wide"
	}
	return "project " + *projectID
}

// lookUpProject resolves a reference against the organisation's projects and
// classifies the miss for the exit code contract.
func lookUpProject(reference string) (*client.ProjectSummary, error) {
	projects, err := apiClient.ListProjects()
	if err != nil {
		return nil, projectsCommandError(fmt.Errorf("listing projects: %w", err))
	}
	project, resolveError := resolveProjectReference(projects, reference)
	if resolveError != nil {
		if errors.Is(resolveError, errProjectNotFound) {
			return nil, withExitCode(exitNotFound, resolveError)
		}
		return nil, withExitCode(exitUsage, resolveError)
	}
	return project, nil
}

var (
	projectSlugUnwanted = regexp.MustCompile(`[^a-z0-9]+`)
	projectSlugPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// projectSlugFromName derives a slug the platform accepts from a display
// name: lowercase, runs of anything but letters and digits become one hyphen,
// trimmed to the maximum.
func projectSlugFromName(name string) string {
	slug := projectSlugUnwanted.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > projectSlugMaximum {
		slug = strings.Trim(slug[:projectSlugMaximum], "-")
	}
	return slug
}

func isValidProjectSlug(slug string) bool {
	return slug != "" && len(slug) <= projectSlugMaximum && projectSlugPattern.MatchString(slug) && !isLikelyUUID(slug)
}

func renderProjectTable(out io.Writer, projects []client.ProjectSummary) {
	if len(projects) == 0 {
		_, _ = fmt.Fprintln(out, "No projects found.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "Name", "Slug", "Default", "Current", "Description"})
	for _, project := range projects {
		isDefault := ""
		if project.IsDefault {
			isDefault = "yes"
		}
		isCurrent := ""
		if project.IsCurrent {
			isCurrent = text.FgGreen.Sprint("✓")
		}
		writer.AppendRow(table.Row{project.ID, project.Name, project.Slug, isDefault, isCurrent, project.Description})
	}
	writer.Render()
}

func renderProjectDetail(out io.Writer, project client.ProjectSummary) {
	_, _ = fmt.Fprintf(out, "  ID:          %s\n", project.ID)
	_, _ = fmt.Fprintf(out, "  Name:        %s\n", project.Name)
	_, _ = fmt.Fprintf(out, "  Slug:        %s\n", project.Slug)
	if project.Description != "" {
		_, _ = fmt.Fprintf(out, "  Description: %s\n", project.Description)
	}
	if project.IsDefault {
		_, _ = fmt.Fprintln(out, "  Default:     yes")
	}
	if project.CreatedAt != "" {
		_, _ = fmt.Fprintf(out, "  Created:     %s\n", project.CreatedAt)
	}
}

func init() {
	projectCreateCmd.Flags().String("slug", "", "The project's slug (default: derived from the name); lowercase letters, digits and hyphens, immutable")
	projectCreateCmd.Flags().String("description", "", "What the project is for")
	projectRenameCmd.Flags().String("description", "", "Replace the description as well")
	projectDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	projectMoveCmd.Flags().String("kind", "", "What moves: "+strings.Join(projectMoveStrictKinds, ", ")+", "+strings.Join(projectMoveSharedKinds, ", "))
	projectMoveCmd.Flags().String("id", "", "The resource's id (a cluster or application name works too)")
	projectMoveCmd.Flags().String("to", "", "Destination project (slug, name or id), or "+projectOrganisationWideDestination+" for a shared resource")

	registerStructuredOutputFlags(projectListCmd, projectCurrentCmd, projectSwitchCmd, projectCreateCmd, projectGetCmd,
		projectRenameCmd, projectDeleteCmd, projectMembersCmd, projectMoveCmd)

	setIgnoresLocalProject(projectListCmd)
	setIgnoresLocalProject(projectSwitchCmd)
	setIgnoresLocalProject(projectCurrentCmd)

	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectCurrentCmd)
	projectCmd.AddCommand(projectSwitchCmd)
	projectCmd.AddCommand(projectCreateCmd)
	projectCmd.AddCommand(projectGetCmd)
	projectCmd.AddCommand(projectRenameCmd)
	projectCmd.AddCommand(projectDeleteCmd)
	projectCmd.AddCommand(projectMembersCmd)
	projectCmd.AddCommand(projectMoveCmd)

	rootCmd.AddCommand(projectCmd)
}
