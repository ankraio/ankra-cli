package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The organisation registry commands. Every organisation has a private
// project on the Ankra registry; Ankra mints the robots its own lanes log in
// with (the organisation's ci and pull logins, one push robot per
// application). These commands are for the robots you need yourself: a login
// for CI you run outside Ankra, a laptop pushing an image by hand, or a
// cluster Ankra does not manage pulling from the project. The listing shows
// Ankra's own robots beside them, read-only, so it answers "which logins
// reach my images" in full.

func newRegistryCommand() *cobra.Command {
	registryCommand := &cobra.Command{
		Use:   "registry",
		Short: "Manage the organisation's Ankra registry",
		Long: `Manage the organisation's Ankra registry.

Every organisation publishes to a private project on the Ankra registry. Ankra
mints the logins its own lanes use - the organisation's ci and pull robots, and
one push robot per application - and these commands cover the robot accounts
you need on top of that: a login for CI you run outside Ankra, a laptop
pushing an image by hand, or a cluster Ankra does not manage pulling from the
project. 'ankra registry robots list' shows both: yours, and the ones Ankra
manages.

A robot account is bound to one registry project. 'ankra registry projects'
creates extra projects beside the organisation's own, so a robot can be given
access to only what is kept in one of them.`,
	}
	registryCommand.AddCommand(newRegistryRobotsCommand())
	registryCommand.AddCommand(newRegistryProjectsCommand())
	return registryCommand
}

func newRegistryProjectsCommand() *cobra.Command {
	projectsCommand := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "Create, list and delete the organisation's registry projects",
		Long: `Create, list and delete the organisation's registry projects.

Every organisation has one project on the Ankra registry, named default here,
and everything it publishes lands there. A robot account is bound to one
project, so a robot on the default project reaches all of it. An extra project
is how you give a login less: push what should be kept apart into the extra
project and bind a robot to it with 'ankra registry robots create <name>
--project <project>'.

An extra project is private and has the same scan policy, retention policy and
storage quota as the default one. Deleting one never deletes anything else on
the way: a project that still holds repositories, or that robot accounts are
still bound to, is refused.`,
	}
	projectsCommand.AddCommand(newRegistryProjectsListCommand())
	projectsCommand.AddCommand(newRegistryProjectsCreateCommand())
	projectsCommand.AddCommand(newRegistryProjectsDeleteCommand())
	return projectsCommand
}

func newRegistryProjectsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the organisation's registry projects",
		Long: `List the organisation's registry projects: its own (default) first, then the
extra ones, each with the path images are pushed under, how many repositories
it holds and how many of your robot accounts are bound to it.

With --registry and --admin-credential it lists the projects of a Harbor your
organisation runs itself instead, as that credential sees them - the names to
bind a robot to with 'ankra registry robots create <name> --registry ...
--project <project>'. That listing acts as the admin credential, so it needs
credentials.reveal.`,
		Example: "  ankra registry projects list\n  ankra registry projects list -o json\n" +
			"  ankra registry projects list --registry smartoptics-harbor --admin-credential smartoptics-harbor-robot-admin",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			registryName, adminCredentialName, flagError := integratedRegistryFlags(command)
			if flagError != nil {
				return flagError
			}
			var list *client.RegistryProjectList
			var listError error
			if registryName != "" {
				list, listError = listIntegratedRegistryProjects(command, registryName, adminCredentialName)
				if listError != nil {
					return listError
				}
			} else {
				list, listError = apiClient.ListRegistryProjects(command.Context())
				if listError != nil {
					return registryProjectsRouteError(listError)
				}
			}
			if rendered, renderError := renderStructured(command, list); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			projectTable := table.NewWriter()
			projectTable.SetOutputMirror(out)
			projectTable.SetStyle(table.StyleRounded)
			projectTable.AppendHeader(table.Row{"Name", "Push to", "Repositories", "Robots", "Created"})
			for _, project := range list.Projects {
				created := project.CreatedAt
				if created == "" {
					created = "-"
				}
				projectTable.AppendRow(table.Row{project.Name, project.Host + "/" + project.Project,
					project.RepositoryCount, project.RobotCount, created})
			}
			projectTable.Render()
			if list.ExtraProjectLimit > 0 {
				_, _ = fmt.Fprintf(out, "%d of %d extra projects used.\n", max(len(list.Projects)-1, 0), list.ExtraProjectLimit)
			}
			return nil
		},
	}
	registerIntegratedRegistryFlags(listCommand)
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

// registerIntegratedRegistryFlags adds --registry and --admin-credential,
// which point a command at a Harbor the organisation runs itself.
func registerIntegratedRegistryFlags(command *cobra.Command) {
	command.Flags().String("registry", "", "An integrated OCI registry entry - a Harbor your organisation runs - "+
		"instead of the Ankra registry ('ankra helm registries list' shows them)")
	command.Flags().String("admin-credential", "", "With --registry: the organisation's registry credential that may "+
		"manage robot accounts on that registry")
}

// integratedRegistryFlags reads --registry and --admin-credential, which
// only make sense together.
func integratedRegistryFlags(command *cobra.Command) (string, string, error) {
	registryName, _ := command.Flags().GetString("registry")
	registryName = strings.TrimSpace(registryName)
	adminCredentialName, _ := command.Flags().GetString("admin-credential")
	adminCredentialName = strings.TrimSpace(adminCredentialName)
	switch {
	case registryName != "" && adminCredentialName == "":
		return "", "", withExitCode(exitUsage, fmt.Errorf("--registry needs --admin-credential: the organisation's registry "+
			"credential that may manage robot accounts on that registry"))
	case registryName == "" && adminCredentialName != "":
		return "", "", withExitCode(exitUsage, fmt.Errorf("--admin-credential is only used with --registry, "+
			"for a registry your organisation runs itself"))
	}
	return registryName, adminCredentialName, nil
}

// listIntegratedRegistryProjects lists an integrated registry's projects. A
// platform that predates the listing ignores the query and answers the
// Ankra registry's projects, which that registry does not have, so an answer
// that does not name the registry asked about is refused.
func listIntegratedRegistryProjects(command *cobra.Command, registryName string,
	adminCredentialName string) (*client.RegistryProjectList, error) {
	list, listError := apiClient.ListIntegratedRegistryProjects(command.Context(), registryName, adminCredentialName)
	if listError != nil {
		return nil, registryProjectsRouteError(listError)
	}
	if list.Registry != registryName {
		return nil, withExitCode(exitError, fmt.Errorf("this platform does not list the projects of an integrated "+
			"registry yet: it answered the Ankra registry's projects instead of %q's", registryName))
	}
	return list, nil
}

func newRegistryProjectsCreateCommand() *cobra.Command {
	createCommand := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an extra registry project",
		Long: `Create an extra registry project for the organisation.

The name is 2 to 30 lower-case letters, digits and single hyphens; 'default' is the
organisation's own project and is reserved. The project is private and has the
same scan policy, retention policy and storage quota as the default one. Bind
a robot account to it with 'ankra registry robots create <name> --project
<project>'.`,
		Example: "  ankra registry projects create staging\n  ankra registry robots create staging-ci --project staging",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			created, createError := apiClient.CreateRegistryProject(command.Context(), strings.TrimSpace(arguments[0]))
			if createError != nil {
				return registryProjectsRouteError(createError)
			}
			if rendered, renderError := renderStructured(command, created); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Registry project %q created.\n", created.Name)
			_, _ = fmt.Fprintf(out, "  Push to: %s/%s\n", created.Host, created.Project)
			_, _ = fmt.Fprintf(out, "Bind a robot account to it with 'ankra registry robots create <name> --project %s'.\n", created.Name)
			return nil
		},
	}
	registerStructuredOutputFlags(createCommand)
	return createCommand
}

func newRegistryProjectsDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete an extra registry project",
		Long: `Delete an extra registry project.

Nothing else is deleted on the way. A project that still holds repositories is
refused until they are deleted from the registry, and one that robot accounts
are still bound to is refused until they are revoked. The organisation's own
project (default) cannot be deleted.`,
		Example: "  ankra registry projects delete staging --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			projectName := strings.TrimSpace(arguments[0])
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Delete registry project %q? [y/N]: ", projectName), yes); confirmError != nil {
				return confirmError
			}
			if deleteError := apiClient.DeleteRegistryProject(command.Context(), projectName); deleteError != nil {
				return registryProjectsRouteError(deleteError)
			}
			if rendered, renderError := renderStructured(command, map[string]any{"name": projectName, "deleted": true}); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Registry project %q deleted.\n", projectName)
			return nil
		},
	}
	deleteCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(deleteCommand)
	return deleteCommand
}

func newRegistryRobotsCommand() *cobra.Command {
	robotsCommand := &cobra.Command{
		Use:     "robots",
		Aliases: []string{"robot"},
		Short:   "Create, list, rotate and revoke robot accounts on the organisation's registry project",
		Long: `Create, list, rotate and revoke robot accounts on the organisation's registry
project.

A robot is a named login bound to one registry project: it reaches the
organisation's project and nothing else on the registry. It holds a preset -
push (push and pull) or pull - or its own list of permissions, and can be set
to expire. Its secret is shown once, when it is created or rotated; the login
is also stored as the managed registry credential ankra-harbor-robot-<name>,
so it can be referenced from clusters and applications like any other registry
credential. Revoking a robot deletes it from the registry and drops that
credential in one step.

The robots Ankra mints for its own lanes (ci, pull, one per application) are
listed too. They are managed: Ankra rotates them and hands the new secret to
the builds and clusters that use it, so 'rotate' and 'delete' refuse them.`,
	}
	robotsCommand.AddCommand(newRegistryRobotsCreateCommand())
	robotsCommand.AddCommand(newRegistryRobotsListCommand())
	robotsCommand.AddCommand(newRegistryRobotsPermissionsCommand())
	robotsCommand.AddCommand(newRegistryRobotsGetCommand())
	robotsCommand.AddCommand(newRegistryRobotsRotateCommand())
	robotsCommand.AddCommand(newRegistryRobotsDeleteCommand())
	return robotsCommand
}

func newRegistryRobotsCreateCommand() *cobra.Command {
	createCommand := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a robot account and show its secret once",
		Long: `Create a robot account on the organisation's registry project and show its
secret once.

The name is 2 to 32 lower-case letters, digits and hyphens. The registry login
becomes robot$<project>+user-<name>. --scope push (the default) grants push and
pull; --scope pull grants pull only. For anything else - a cleanup job that
deletes artifacts, a release job that moves tags, a scanner - grant exactly
what it needs with --permission, repeated or comma-separated, instead of a
scope; 'ankra registry robots permissions' lists what can be granted. Whatever
it holds, the robot reaches the organisation's project and no other.
--expires-in-days makes the registry stop honouring the robot after that many
days; without it the robot never expires. --project binds the robot to one of
the organisation's extra registry projects instead of its own ('ankra registry
projects list' shows them); it then reaches that project and nothing else.

--registry creates the robot on a Harbor your organisation runs itself instead:
name the OCI registry entry it is integrated as ('ankra helm registries list'
shows them) and, with --admin-credential, a registry credential of the organisation
that may manage robot accounts there (project administrator on the project).
Ankra acts on that registry only with that credential, never its own; a robot
login Ankra manages cannot be used. --project is required there: the one
project of that registry the robot is bound to, such as collectorcharts, and
the only one it reaches ('ankra registry projects list --registry ...
--admin-credential ...' shows them). Rotate, delete, get and list work the
same afterwards.

The secret is printed exactly once, with
a docker login command that reads it from stdin - copy it now, it is not stored
anywhere you can read it back from. The secret is never put on a command line,
where the shell history and 'ps' would keep it. Rotate it with 'ankra registry robots rotate' if it
is lost or leaked.

Creating a robot needs the credentials.write permission. A --permission list
that reaches past repository:pull and repository:push (deleting artifacts,
moving tags, and the like) also needs credentials.reveal, and so does a robot on
an integrated registry (--registry), which is minted by acting as the admin
credential. With --permission or
--expires-in-days the CLI first checks that the platform offers them by
reading the robot listing, and with --project that the organisation has the
project; a token without credentials.read skips those checks, and if the
robot then comes back broader, longer-lived or on another project than asked
it is deleted again before its secret is shown.`,
		Example: `  ankra registry robots create jenkins --description "Jenkins on the office server"
  ankra registry robots create edge-cluster --scope pull
  ankra registry robots create contractor --scope pull --expires-in-days 30
  ankra registry robots create cleanup --permission repository:pull,artifact:list,artifact:delete
  ankra registry robots create staging-ci --project staging
  ankra registry robots create jenkins --registry smartoptics-harbor \
    --admin-credential smartoptics-harbor-robot-admin --project smart-hub-images
  ankra registry robots create jenkins -o json | jq -r .secret`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			scope, _ := command.Flags().GetString("scope")
			scope = strings.ToLower(strings.TrimSpace(scope))
			rawPermissions, _ := command.Flags().GetStringSlice("permission")
			permissions := registryRobotPermissionFlags(rawPermissions)
			if command.Flags().Changed("permission") && len(permissions) == 0 {
				return withExitCode(exitUsage, fmt.Errorf("--permission names no permission; "+
					"give one or more resource:action values ('ankra registry robots permissions' lists them), or leave it out for --scope"))
			}
			switch {
			case len(permissions) > 0 && command.Flags().Changed("scope"):
				return withExitCode(exitUsage, fmt.Errorf("--scope and --permission are alternatives: "+
					"state a preset scope, or the robot's own permissions, not both"))
			case len(permissions) > 0:
				// The platform derives the scope from the permissions; sending
				// the flag's default beside them would read as both.
				scope = ""
			case scope != client.RegistryRobotScopePush && scope != client.RegistryRobotScopePull:
				return withExitCode(exitUsage, fmt.Errorf("--scope must be %q (push and pull) or %q, got %q; "+
					"for anything else grant permissions with --permission",
					client.RegistryRobotScopePush, client.RegistryRobotScopePull, scope))
			}
			expiresInDays, _ := command.Flags().GetInt("expires-in-days")
			if expiresInDays < 0 || expiresInDays > registryRobotMaximumExpiryDays {
				return withExitCode(exitUsage, fmt.Errorf("--expires-in-days must be 1 to %d days, or 0 (the default) "+
					"for a robot that never expires; got %d", registryRobotMaximumExpiryDays, expiresInDays))
			}
			supportChecked := true
			if len(permissions) > 0 || expiresInDays > 0 {
				supportError := requireRegistryRobotPermissionSupport(command, permissions)
				switch {
				case supportError == nil:
				case exitCodeFor(supportError) == exitForbidden:
					// The support check reads the robot listing, which needs
					// credentials.read; a token that may only create
					// (credentials.write) is not refused for it. The platform
					// validates the permissions itself, and the answer to the
					// create is checked below instead.
					supportChecked = false
				default:
					return supportError
				}
			}
			registryName, adminCredentialName, flagError := integratedRegistryFlags(command)
			if flagError != nil {
				return flagError
			}
			projectName, _ := command.Flags().GetString("project")
			projectName = strings.ToLower(strings.TrimSpace(projectName))
			if registryName != "" && projectName == "" {
				return withExitCode(exitUsage, fmt.Errorf("--registry needs --project: the one project of that registry "+
					"the robot is bound to ('ankra registry projects list --registry %s --admin-credential %s' shows them)",
					registryName, adminCredentialName))
			}
			if projectName == client.RegistryDefaultProjectName && registryName == "" {
				projectName = ""
			}
			projectChecked := true
			if projectName != "" && registryName == "" {
				projectError := requireRegistryProject(command, projectName)
				switch {
				case projectError == nil:
				case exitCodeFor(projectError) == exitForbidden:
					// The project check reads the project listing, which
					// needs credentials.read, like the support check above:
					// a create-only token goes ahead and the answer is
					// checked below instead.
					projectChecked = false
				default:
					return projectError
				}
			}
			description, _ := command.Flags().GetString("description")
			created, createError := apiClient.CreateRegistryRobot(command.Context(), client.CreateRegistryRobotRequest{
				Name:                strings.TrimSpace(arguments[0]),
				Scope:               scope,
				Permissions:         permissions,
				Description:         description,
				ExpiresInDays:       expiresInDays,
				Project:             projectName,
				Registry:            registryName,
				AdminCredentialName: adminCredentialName,
			})
			if createError != nil {
				return createError
			}
			var ignored []string
			if !supportChecked {
				if ignoredFlags := registryRobotIgnoredFlags(created, permissions, expiresInDays, time.Now()); ignoredFlags != "" {
					ignored = append(ignored, ignoredFlags)
				}
			}
			if !projectChecked && created.ProjectName != projectName {
				// A platform without registry projects ignores --project and
				// binds the robot to the organisation's own project, which
				// reaches more than was asked for.
				ignored = append(ignored, "--project")
			}
			if registryName != "" && created.Registry != registryName {
				// A platform without integrated-registry robots ignores
				// --registry and mints the robot on the Ankra registry instead:
				// a login for somewhere other than asked.
				ignored = append(ignored, "--registry")
			}
			if len(ignored) > 0 {
				return revokeUnhonouredRegistryRobot(command, created, strings.Join(ignored, " and "))
			}
			return renderRegistryRobotSecret(command, created, "Robot account created.")
		},
	}
	createCommand.Flags().String("scope", client.RegistryRobotScopePush, "Rights on the project: push (push and pull) or pull")
	createCommand.Flags().StringSlice("permission", nil, "A permission to grant instead of a preset scope, written resource:action; "+
		"repeat the flag or separate with commas ('ankra registry robots permissions' lists them)")
	createCommand.Flags().Int("expires-in-days", 0, "Days until the registry stops honouring the robot (default: it never expires)")
	createCommand.Flags().String("project", "", "The registry project to bind the robot to (default: the organisation's own; "+
		"'ankra registry projects list' shows the others)")
	registerIntegratedRegistryFlags(createCommand)
	createCommand.Flags().String("description", "", "What this robot is for (shown in the listing)")
	registerStructuredOutputFlags(createCommand)
	return createCommand
}

// registryProjectsRouteError turns the answer of a platform that does not
// serve registry projects - a 404 that names nothing, from a route that is
// not registered - into a sentence saying so. Every other error passes
// through unchanged, a 404 that names a project included.
func registryProjectsRouteError(routeError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(routeError, &unexpected) && unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == "" {
		return withExitCode(exitError, errors.New("this platform does not serve registry projects yet: "+
			"/api/v1/org/registry-projects is not registered, so every robot account is bound to the organisation's own project"))
	}
	return routeError
}

// requireRegistryProject refuses a robot create that names a project unless
// the organisation has it. A platform that predates registry projects
// ignores the field and would mint the robot on the organisation's own
// project - a login reaching more than was asked for - so the platform is
// asked first, and a name it does not list is refused here with the ones it
// does.
func requireRegistryProject(command *cobra.Command, projectName string) error {
	list, listError := apiClient.ListRegistryProjects(command.Context())
	if listError != nil {
		return registryProjectsRouteError(listError)
	}
	var projectNames []string
	for _, project := range list.Projects {
		if project.Name == projectName {
			return nil
		}
		projectNames = append(projectNames, project.Name)
	}
	return withExitCode(exitUsage, fmt.Errorf("--project %q is not one of this organisation's registry projects (%s); "+
		"create it with 'ankra registry projects create %s'", projectName, strings.Join(projectNames, ", "), projectName))
}

// registryRobotMaximumExpiryDays is the platform's ceiling on a robot's
// expiry; it refuses anything longer.
const registryRobotMaximumExpiryDays = 3650

// requireRegistryRobotPermissionSupport refuses a create that states its own
// permissions or an expiry unless the platform can honour them. A platform
// that predates both ignores the fields it does not know and would mint a
// push-and-pull robot that never expires - the opposite of what was asked,
// with the secret already handed out - so the platform is asked first, and a
// permission it does not offer is refused here with the ones it does.
func requireRegistryRobotPermissionSupport(command *cobra.Command, permissions []string) error {
	list, listError := apiClient.ListRegistryRobots(command.Context())
	if listError != nil {
		return listError
	}
	if len(list.AvailablePermissions) == 0 {
		return fmt.Errorf("this platform does not support robot permissions or an expiry yet; " +
			"create the robot with --scope push or --scope pull")
	}
	offered := make([]string, 0, len(list.AvailablePermissions))
	for _, permission := range list.AvailablePermissions {
		offered = append(offered, permission.Permission)
	}
	for _, permission := range permissions {
		if !slices.Contains(offered, permission) {
			return withExitCode(exitUsage, fmt.Errorf("--permission %q is not a permission a robot can hold; choose from: %s",
				permission, strings.Join(offered, ", ")))
		}
	}
	return nil
}

// registryRobotIgnoredFlags names the flags a create answer shows the
// platform did not honour. A platform that predates robot permissions mints
// a push robot that never expires and answers with no permissions and no
// expiry; any answer broader or longer-lived than asked counts the same.
func registryRobotIgnoredFlags(created *client.RegistryRobotWithSecret, permissions []string, expiresInDays int, now time.Time) string {
	var ignored []string
	if len(permissions) > 0 && !registryRobotPermissionsWithin(created.Permissions, permissions) {
		ignored = append(ignored, "--permission")
	}
	if expiresInDays > 0 && !registryRobotExpiryWithin(created.ExpiresAt, expiresInDays, now) {
		ignored = append(ignored, "--expires-in-days")
	}
	return strings.Join(ignored, " and ")
}

// registryRobotPermissionsWithin reports whether the robot holds grants and
// none beyond the requested ones. The platform adds repository:pull to a
// selection holding repository:push, so that one is not counted as extra.
func registryRobotPermissionsWithin(granted []string, requested []string) bool {
	if len(granted) == 0 {
		return false
	}
	for _, grant := range granted {
		grant = strings.ToLower(strings.TrimSpace(grant))
		if slices.Contains(requested, grant) {
			continue
		}
		if grant == registryRobotPermissionPull && slices.Contains(requested, registryRobotPermissionPush) {
			continue
		}
		return false
	}
	return true
}

// registryRobotExpiryWithin reports whether the robot expires no later than
// the requested number of days from now, with a day's slack for the
// platform rounding to a whole day.
func registryRobotExpiryWithin(expiresAt *string, expiresInDays int, now time.Time) bool {
	if expiresAt == nil {
		return false
	}
	expiry, parseError := time.Parse(time.RFC3339, strings.TrimSpace(*expiresAt))
	if parseError != nil {
		return false
	}
	return !expiry.After(now.Add(time.Duration(expiresInDays+1) * 24 * time.Hour))
}

// The two grants the presets are made of (cluster organisation
// registry_robot_permissions.go).
const (
	registryRobotPermissionPull = "repository:pull"
	registryRobotPermissionPush = "repository:push"
)

// revokeUnhonouredRegistryRobot deletes a robot the platform minted without
// the permissions or expiry that were asked for, and never shows its secret:
// a robot broader or longer-lived than requested must not reach anyone.
func revokeUnhonouredRegistryRobot(command *cobra.Command, created *client.RegistryRobotWithSecret, ignored string) error {
	refusal := fmt.Sprintf("the platform did not honour %s: robot %q came back with scope %q, permissions [%s], expiry %s and project %q",
		ignored, created.Name, created.Scope, strings.Join(created.Permissions, ", "), registryRobotExpiryText(created.ExpiresAt),
		created.ProjectName)
	if created.Registry != "" || strings.Contains(ignored, "--registry") {
		refusal += fmt.Sprintf(" on %s", created.Host)
	}
	if deleteError := apiClient.DeleteRegistryRobot(command.Context(), created.Name); deleteError != nil {
		return fmt.Errorf("%s, and deleting it again failed (%v): delete it with 'ankra registry robots delete %s'; "+
			"its secret was not shown", refusal, deleteError, created.Name)
	}
	if strings.Contains(ignored, "--registry") {
		return fmt.Errorf("%s, so it was deleted again and its secret was not shown. This platform does not create robots "+
			"on an integrated registry yet: create the robot in that registry itself and store it with "+
			"'ankra helm credentials create'", refusal)
	}
	return fmt.Errorf("%s, so it was deleted again and its secret was not shown. A platform without robot "+
		"permissions, an expiry or registry projects ignores them: create the robot with --scope push or --scope pull, "+
		"on the organisation's own project, there", refusal)
}

func registryRobotExpiryText(expiresAt *string) string {
	if expiresAt == nil || strings.TrimSpace(*expiresAt) == "" {
		return "never"
	}
	return *expiresAt
}

// registryRobotPermissionFlags normalises what --permission collected: each
// value trimmed and lower-cased, blanks and repeats dropped, order kept.
func registryRobotPermissionFlags(values []string) []string {
	var permissions []string
	for _, value := range values {
		permission := strings.ToLower(strings.TrimSpace(value))
		if permission == "" || slices.Contains(permissions, permission) {
			continue
		}
		permissions = append(permissions, permission)
	}
	return permissions
}

// The values --kind accepts on the listing. managed is every robot Ankra
// minted, whichever lane minted it.
const (
	registryRobotKindFilterAll     = "all"
	registryRobotKindFilterManaged = "managed"
)

// registryRobotMatchesKind reports whether a robot passes the listing's
// --kind filter.
func registryRobotMatchesKind(robot client.RegistryRobot, kindFilter string) bool {
	switch kindFilter {
	case registryRobotKindFilterAll:
		return true
	case registryRobotKindFilterManaged:
		return robot.Managed
	}
	return robot.KindOrUser() == kindFilter
}

// registryRobotAccessSummary says what a robot may do in one cell: the
// preset in words, or the permissions themselves when they are neither
// preset.
func registryRobotAccessSummary(robot client.RegistryRobot) string {
	switch robot.Scope {
	case client.RegistryRobotScopePush:
		return "push and pull"
	case client.RegistryRobotScopePull:
		return "pull"
	}
	if len(robot.Permissions) > 0 {
		return strings.Join(robot.Permissions, ", ")
	}
	if robot.Scope == "" {
		return "-"
	}
	return robot.Scope
}

// registryRobotProjectsSummary names the projects a robot reaches, falling
// back to the single project a platform that predates the list states. An
// extra project is named the way a member addresses it, since that is what
// --project and 'registry projects' take.
func registryRobotProjectsSummary(robot client.RegistryRobot) string {
	if robot.ProjectName != "" && robot.ProjectName != client.RegistryDefaultProjectName && robot.Project != "" {
		return robot.ProjectName + " (" + robot.Project + ")"
	}
	if len(robot.Projects) > 0 {
		return strings.Join(robot.Projects, ", ")
	}
	if robot.Project != "" {
		return robot.Project
	}
	return "-"
}

// registryRobotExpirySummary says when a robot stops working, and says so
// plainly once it has.
func registryRobotExpirySummary(robot client.RegistryRobot, now time.Time) string {
	if robot.ExpiresAt == nil || *robot.ExpiresAt == "" {
		return "never"
	}
	expiresAt, parseError := time.Parse(time.RFC3339, *robot.ExpiresAt)
	if parseError == nil && !now.Before(expiresAt) {
		return *robot.ExpiresAt + " (expired)"
	}
	return *robot.ExpiresAt
}

func newRegistryRobotsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every robot account the organisation holds, yours and the ones Ankra manages",
		Long: `List every robot account the organisation holds on its registry project.

Your own robots come first, then the ones Ankra manages: the organisation's ci
and pull robots and one push robot per application. Each row shows the robot's
kind, registry login, what it may do, the project it is bound to, when it
expires, and when it was created and last rotated. Secrets are never listed.

--kind narrows the listing: user (the ones you created), managed (everything
Ankra minted), organisation (ci and pull) or application. Managed robots are
shown so you can see every login that reaches the organisation's images; they
are rotated by the lane that owns them, not from here.`,
		Example: `  ankra registry robots list
  ankra registry robots list --kind user
  ankra registry robots list --kind managed -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			kindFilter, _ := command.Flags().GetString("kind")
			kindFilter = strings.ToLower(strings.TrimSpace(kindFilter))
			if !slices.Contains([]string{registryRobotKindFilterAll, registryRobotKindFilterManaged,
				client.RegistryRobotKindUser, client.RegistryRobotKindOrganisation, client.RegistryRobotKindApplication}, kindFilter) {
				return withExitCode(exitUsage, fmt.Errorf("--kind must be all, user, managed, organisation or application, got %q", kindFilter))
			}
			list, listError := apiClient.ListRegistryRobots(command.Context())
			if listError != nil {
				return listError
			}
			robots := []client.RegistryRobot{}
			for _, robot := range list.Robots {
				if registryRobotMatchesKind(robot, kindFilter) {
					robots = append(robots, robot)
				}
			}
			list.Robots = robots
			list.TotalCount = len(robots)
			if rendered, renderError := renderStructured(command, list); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(robots) == 0 {
				if kindFilter == registryRobotKindFilterAll || kindFilter == client.RegistryRobotKindUser {
					_, _ = fmt.Fprintln(out, "No robot accounts yet. Create one with 'ankra registry robots create <name>'.")
				} else {
					_, _ = fmt.Fprintf(out, "No %s robot accounts.\n", kindFilter)
				}
				return nil
			}
			if list.Registry != nil {
				_, _ = fmt.Fprintf(out, "Registry project: %s/%s\n", list.Registry.Host, list.Registry.Project)
			}
			robotTable := table.NewWriter()
			robotTable.SetOutputMirror(out)
			robotTable.SetStyle(table.StyleRounded)
			robotTable.AppendHeader(table.Row{"Name", "Kind", "Login", "Access", "Project", "Expires", "Created", "Rotated", "Description"})
			now := time.Now()
			hasManaged := false
			for _, robot := range robots {
				hasManaged = hasManaged || robot.Managed
				rotated := "-"
				if robot.RotatedAt != nil && *robot.RotatedAt != "" {
					rotated = *robot.RotatedAt
				}
				robotTable.AppendRow(table.Row{robot.Name, robot.KindOrUser(), robot.RobotName,
					registryRobotAccessSummary(robot), registryRobotProjectsSummary(robot),
					registryRobotExpirySummary(robot, now), robot.CreatedAt, rotated, robot.Description})
			}
			robotTable.Render()
			if hasManaged {
				_, _ = fmt.Fprintln(out, "organisation and application robots are managed by Ankra: listed here, rotated by the lane that owns them.")
			}
			return nil
		},
	}
	listCommand.Flags().String("kind", registryRobotKindFilterAll, "Which robots to list: all, user (yours), managed (Ankra's), organisation or application")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newRegistryRobotsPermissionsCommand() *cobra.Command {
	permissionsCommand := &cobra.Command{
		Use:   "permissions",
		Short: "List the permissions a robot account can be created with",
		Long: `List the permissions a robot account can be created with.

Each one is a value for 'ankra registry robots create --permission'. They are
all rights on the content of the organisation's own registry project - pulling,
pushing, listing and deleting repositories and artifacts, managing tags,
scanning - never on the project's settings or on any other project.`,
		Example: "  ankra registry robots permissions\n  ankra registry robots permissions -o json",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			list, listError := apiClient.ListRegistryRobots(command.Context())
			if listError != nil {
				return listError
			}
			permissions := list.AvailablePermissions
			if permissions == nil {
				permissions = []client.RegistryRobotPermission{}
			}
			if rendered, renderError := renderStructured(command, map[string]any{"permissions": permissions}); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(permissions) == 0 {
				_, _ = fmt.Fprintln(out, "This platform offers the two presets only: create a robot with --scope push or --scope pull.")
				return nil
			}
			permissionTable := table.NewWriter()
			permissionTable.SetOutputMirror(out)
			permissionTable.SetStyle(table.StyleRounded)
			permissionTable.AppendHeader(table.Row{"Permission", "What it allows"})
			for _, permission := range permissions {
				permissionTable.AppendRow(table.Row{permission.Permission, permission.Description})
			}
			permissionTable.Render()
			return nil
		},
	}
	registerStructuredOutputFlags(permissionsCommand)
	return permissionsCommand
}

func newRegistryRobotsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <name>",
		Short: "Show one robot account",
		Long: `Show one robot account: its registry login, what it may do, the project it is
bound to, the managed credential holding its login, and when it was created,
last rotated and expires. One of your own robots is read by its name; one
Ankra manages by the name the listing gives it (ci, pull, app-<application id>).
The secret is never shown here; rotate the robot to get a new one.`,
		Example: "  ankra registry robots get jenkins\n  ankra registry robots get ci",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			robot, getError := apiClient.GetRegistryRobot(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			if rendered, renderError := renderStructured(command, robot); rendered || renderError != nil {
				return renderError
			}
			printRegistryRobot(command, &robot.Name, robot)
			return nil
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newRegistryRobotsRotateCommand() *cobra.Command {
	rotateCommand := &cobra.Command{
		Use:   "rotate <name>",
		Short: "Mint a new secret for a robot account and show it once",
		Long: `Mint a new secret for a robot account and show it once.

The previous secret stops working the moment the registry answers, which makes
this the response to a leaked or lost secret. Update every place that logs in
with the robot afterwards. A robot the registry no longer has is minted again
under the same name, with the same permissions and the rest of its lifetime. A
rotation never extends a robot: one that has expired is refused, and so is a
robot Ankra manages. Asks first, because a mistyped name would take another
robot's consumers down; --yes skips the prompt for scripts.`,
		Example: "  ankra registry robots rotate jenkins\n  ankra registry robots rotate jenkins --yes -o json | jq -r .secret",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			robotName := strings.TrimSpace(arguments[0])
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Rotate the secret of robot account %q? Everything logging in with the current secret stops working. [y/N]: ", robotName),
				yes); confirmError != nil {
				return confirmError
			}
			rotated, rotateError := apiClient.RotateRegistryRobotSecret(command.Context(), robotName)
			if rotateError != nil {
				return rotateError
			}
			return renderRegistryRobotSecret(command, rotated, "Robot secret rotated. The previous secret no longer works.")
		},
	}
	rotateCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(rotateCommand)
	return rotateCommand
}

func newRegistryRobotsDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"revoke", "rm"},
		Short:   "Revoke a robot account: delete it from the registry and drop its credential",
		Long: `Revoke a robot account: delete it from the registry and drop the managed
credential holding its login.

Everything still logging in with the robot stops working, which is the point
of a revoke. Prefer 'rotate' when the robot should keep working under a new
secret. A robot Ankra manages is refused: it is not yours to revoke.`,
		Example: "  ankra registry robots delete jenkins --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			robotName := strings.TrimSpace(arguments[0])
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Revoke robot account %q? Everything logging in with it stops working. [y/N]: ", robotName),
				yes); confirmError != nil {
				return confirmError
			}
			if deleteError := apiClient.DeleteRegistryRobot(command.Context(), robotName); deleteError != nil {
				return deleteError
			}
			if rendered, renderError := renderStructured(command, map[string]any{"name": robotName, "deleted": true}); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Robot account %q revoked.\n", robotName)
			return nil
		},
	}
	deleteCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(deleteCommand)
	return deleteCommand
}

// renderRegistryRobotSecret prints a create or rotate answer: structured
// output carries the secret and the platform's docker_login as fields, for
// scripts; the human form shows the secret exactly once, followed by a login
// command that reads it from stdin.
//
// The platform's docker_login embeds the secret as -p '<secret>'. Pasting that
// line puts the secret into the shell history and into the argv every user on
// the machine can read with ps, so the human form never prints it: the login
// it prints takes the password on stdin instead, and the secret appears in the
// output once, on its own line.
func renderRegistryRobotSecret(command *cobra.Command, robot *client.RegistryRobotWithSecret, headline string) error {
	if rendered, renderError := renderStructured(command, robot); rendered || renderError != nil {
		return renderError
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintln(out, headline)
	_, _ = fmt.Fprintln(out)
	printRegistryRobot(command, nil, &robot.RegistryRobot)
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Secret (save this, it will not be shown again):")
	_, _ = fmt.Fprintf(out, "  %s\n", robot.Secret)
	if robot.Warning != "" {
		_, _ = fmt.Fprintln(command.ErrOrStderr())
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: %s\n", robot.Warning)
	}
	if login := registryRobotLoginCommand(&robot.RegistryRobot); login != "" {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Log in with it (paste the secret when prompted, or pipe it on stdin):")
		_, _ = fmt.Fprintf(out, "  %s\n", login)
	}
	return nil
}

// registryRobotLoginCommand is the docker login for the robot with the
// password taken from stdin, so the secret never sits on a command line. It
// is built from the robot's own host and login; when either is missing there
// is no safe line to print and it answers "", never the platform's
// secret-bearing docker_login.
func registryRobotLoginCommand(robot *client.RegistryRobot) string {
	host := strings.TrimSpace(robot.Host)
	login := strings.TrimSpace(robot.RobotName)
	if host == "" || login == "" {
		return ""
	}
	// The login is robot$<project>+user-<name>: the $ must not reach the
	// shell unquoted, so it is single-quoted like the platform's own line.
	// The host is quoted the same way; both come from the server's answer.
	return fmt.Sprintf("docker login %s -u %s --password-stdin", shellSingleQuote(host), shellSingleQuote(login))
}

// shellSingleQuote wraps a value in single quotes for a line meant to be
// pasted into a shell, escaping any single quote inside it.
func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// printRegistryRobot prints the robot's record as labelled lines.
func printRegistryRobot(command *cobra.Command, title *string, robot *client.RegistryRobot) {
	out := command.OutOrStdout()
	if title != nil {
		_, _ = fmt.Fprintf(out, "Robot account %q\n", *title)
	}
	_, _ = fmt.Fprintf(out, "  Name:        %s\n", robot.Name)
	if robot.Managed {
		_, _ = fmt.Fprintf(out, "  Kind:        %s (managed by Ankra)\n", robot.KindOrUser())
	} else {
		_, _ = fmt.Fprintf(out, "  Kind:        %s\n", robot.KindOrUser())
	}
	if robot.Application != nil {
		_, _ = fmt.Fprintf(out, "  Application: %s (%s)\n", robot.Application.Name, robot.Application.ID)
	}
	_, _ = fmt.Fprintf(out, "  Login:       %s\n", robot.RobotName)
	if robot.Registry != "" {
		_, _ = fmt.Fprintf(out, "  Registry:    %s (integrated as %s, managed with %s)\n", robot.Host, robot.Registry, robot.AdminCredentialName)
	} else {
		_, _ = fmt.Fprintf(out, "  Registry:    %s\n", robot.Host)
	}
	_, _ = fmt.Fprintf(out, "  Project:     %s\n", registryRobotProjectsSummary(*robot))
	_, _ = fmt.Fprintf(out, "  Access:      %s\n", registryRobotAccessSummary(*robot))
	if len(robot.Permissions) > 0 {
		_, _ = fmt.Fprintf(out, "  Permissions: %s\n", strings.Join(robot.Permissions, ", "))
	}
	if robot.Description != "" {
		_, _ = fmt.Fprintf(out, "  Description: %s\n", robot.Description)
	}
	_, _ = fmt.Fprintf(out, "  Credential:  %s\n", robot.CredentialName)
	_, _ = fmt.Fprintf(out, "  Created:     %s\n", robot.CreatedAt)
	if robot.RotatedAt != nil && *robot.RotatedAt != "" {
		_, _ = fmt.Fprintf(out, "  Rotated:     %s\n", *robot.RotatedAt)
	}
	_, _ = fmt.Fprintf(out, "  Expires:     %s\n", registryRobotExpirySummary(*robot, time.Now()))
}

func init() {
	rootCmd.AddCommand(newRegistryCommand())
}
