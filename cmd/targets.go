package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// environmentNamePattern is the platform's CHECK on environments.name.
var environmentNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// hostTargetNamePattern is the platform's CHECK on host_targets.name.
var hostTargetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// maximumJoinTokenLifetime is the platform's ceiling on a join token.
const maximumJoinTokenLifetime = 24 * time.Hour

// routeAbsentDetail is what the platform's router answers for a path it does
// not serve, as opposed to a handler's own not-found sentence.
const routeAbsentDetail = "Not Found"

// deployLaneError wraps an error from the deploy targets API for the
// terminal. A platform that predates host deploy targets answers the router's
// bare 404; that is not "the target does not exist", so it is said plainly and
// exits 1 rather than the not-found code a script would treat as idempotent.
func deployLaneError(operation string, apiError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(apiError, &unexpected) && unexpected.StatusCode == http.StatusNotFound &&
		(unexpected.Detail == "" || unexpected.Detail == routeAbsentDetail) {
		return withExitCode(exitError, fmt.Errorf(
			"%s: this Ankra platform does not serve host deploy targets yet (the route answered 404). "+
				"Host deploy targets are rolling out; check the selected organisation with 'ankra org current' "+
				"and try again once the platform has them", operation))
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

// validateEnvironmentName refuses a name the platform would refuse, before
// any request is made.
func validateEnvironmentName(environmentName string) error {
	if !environmentNamePattern.MatchString(environmentName) {
		return withExitCode(exitUsage, fmt.Errorf(
			"--environment %q is not a valid environment name: use lower-case letters, digits and '-', "+
				"starting with a letter or digit, at most 63 characters", environmentName))
	}
	return nil
}

func newTargetsCommand() *cobra.Command {
	targetsCommand := &cobra.Command{
		Use:     "targets",
		Aliases: []string{"target"},
		Short:   "Manage host deploy targets",
		Long: `Manage host deploy targets: machines outside Kubernetes that run the
ankra-host-agent and receive releases from 'kind: deploy' pipeline stages.

A host joins an environment with a single-use join token:

  # as an organisation admin
  ankra targets join-token create --environment production -o json

  # as root on the host
  ankra targets register --environment production --name web-1 --token-stdin

The agent only ever connects outbound over HTTPS; the host needs no inbound
port.`,
	}
	joinTokenCommand := &cobra.Command{
		Use:   "join-token",
		Short: "Mint join tokens that let a host register into an environment",
	}
	joinTokenCommand.AddCommand(newTargetsJoinTokenCreateCommand())
	targetsCommand.AddCommand(
		joinTokenCommand,
		newTargetsRegisterCommand(),
		newTargetsListCommand(),
		newTargetsGetCommand(),
		newTargetsRevokeCommand(),
	)
	return targetsCommand
}

func newTargetsJoinTokenCreateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "create",
		Short: "Mint a single-use join token for an environment",
		Long: `Mint a single-use join token a host registers into the environment with.
The environment is created when it does not exist yet.

The token is shown exactly once: the platform keeps only its hash. Without -o
it is the only thing written to stdout (the expiry and the next step go to
stderr), so it can be piped straight into 'ankra targets register
--token-stdin' on the host.`,
		Example: `  ankra targets join-token create --environment production
  ankra targets join-token create --environment production --ttl 4h -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			environmentName, _ := command.Flags().GetString("environment")
			timeToLive, _ := command.Flags().GetDuration("ttl")
			if validationError := validateEnvironmentName(environmentName); validationError != nil {
				return validationError
			}
			request := client.CreateHostJoinTokenRequest{}
			if command.Flags().Changed("ttl") {
				if timeToLive < time.Second || timeToLive > maximumJoinTokenLifetime {
					return withExitCode(exitUsage, fmt.Errorf(
						"--ttl %s is out of range: a join token lives between 1s and 24h", timeToLive))
				}
				request.TTLSeconds = int(timeToLive / time.Second)
			}
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			joinToken, createError := apiClient.CreateHostJoinToken(command.Context(), environmentName, request)
			if createError != nil {
				return deployLaneError("creating a join token for environment "+environmentName, createError)
			}
			if rendered, renderError := renderStructured(command, joinToken); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintln(command.OutOrStdout(), joinToken.JoinToken)
			_, _ = fmt.Fprintf(command.ErrOrStderr(),
				"Join token for environment %s, single use, expires %s. It is not shown again.\n"+
					"On the host, as root: ankra targets register --environment %s --name <host-name> --token-stdin\n",
				joinToken.Environment, joinToken.ExpiresAt, joinToken.Environment)
			return nil
		},
	}
	command.Flags().String("environment", "", "Environment the host registers into (required)")
	command.Flags().Duration("ttl", time.Hour, "How long the token stays valid (at most 24h)")
	_ = command.MarkFlagRequired("environment")
	registerStructuredOutputFlags(command)
	return command
}

func newTargetsListCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List host deploy targets",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			environmentName, _ := command.Flags().GetString("environment")
			if environmentName != "" {
				if validationError := validateEnvironmentName(environmentName); validationError != nil {
					return validationError
				}
			}
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			listing, listError := apiClient.ListHostTargets(command.Context(), environmentName)
			if listError != nil {
				return deployLaneError("listing host targets", listError)
			}
			if listing.HostTargets == nil {
				listing.HostTargets = []client.HostTarget{}
			}
			if rendered, renderError := renderStructured(command, listing); rendered || renderError != nil {
				return renderError
			}
			if len(listing.HostTargets) == 0 {
				_, _ = fmt.Fprintln(command.OutOrStdout(), emptyHostTargetsMessage(command, environmentName))
				return nil
			}
			writeHostTargetTable(command.OutOrStdout(), listing.HostTargets)
			return nil
		},
	}
	command.Flags().String("environment", "", "Only list targets in this environment")
	registerStructuredOutputFlags(command)
	return command
}

// emptyHostTargetsMessage says why a listing is empty. When an environment
// was named, the environments listing tells a typo from an environment that
// simply has no hosts yet.
func emptyHostTargetsMessage(command *cobra.Command, environmentName string) string {
	registerHint := "Register a host with 'ankra targets join-token create --environment <env>' " +
		"and 'ankra targets register' on the host."
	if environmentName == "" {
		return "No host targets found. " + registerHint
	}
	environments, listError := apiClient.ListEnvironments(command.Context())
	if listError == nil {
		exists := false
		for _, environment := range environments.Environments {
			if environment.Name == environmentName {
				exists = true
				break
			}
		}
		if !exists {
			return fmt.Sprintf("Environment %q does not exist. A join token creates it: "+
				"'ankra targets join-token create --environment %s'.", environmentName, environmentName)
		}
	}
	return fmt.Sprintf("No host targets in environment %q. %s", environmentName, registerHint)
}

func writeHostTargetTable(out io.Writer, targets []client.HostTarget) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Name", "Environment", "Status", "Labels", "Agent", "Last Heartbeat", "ID"})
	for _, target := range targets {
		writer.AppendRow(table.Row{
			target.Name,
			target.Environment,
			hostTargetDisplayStatus(target),
			formatHostTargetLabels(target.Labels),
			optionalText(target.AgentVersion),
			formatOptionalTimestamp(target.LastHeartbeatAt),
			target.ID,
		})
	}
	writer.Render()
}

// hostTargetDisplayStatus shows a revoked target as revoked whatever its
// last heartbeat says.
func hostTargetDisplayStatus(target client.HostTarget) string {
	if target.IsRevoked() {
		return "revoked"
	}
	if target.Status == "" {
		return "unknown"
	}
	return target.Status
}

func formatHostTargetLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+labels[key])
	}
	return strings.Join(pairs, ",")
}

// resolveHostTarget accepts a host target id or name. A name is looked up in
// the targets listing (narrowed to environmentName when given); active
// targets win over revoked ones, and when includeRevoked is false a revoked
// target never matches.
func resolveHostTarget(command *cobra.Command, reference string, environmentName string,
	includeRevoked bool) (string, error) {
	if looksLikeUUID(reference) {
		return reference, nil
	}
	listing, listError := apiClient.ListHostTargets(command.Context(), environmentName)
	if listError != nil {
		return "", deployLaneError("looking up host target "+reference, listError)
	}
	var active, revoked []client.HostTarget
	for _, target := range listing.HostTargets {
		if !strings.EqualFold(target.Name, reference) {
			continue
		}
		if target.IsRevoked() {
			revoked = append(revoked, target)
		} else {
			active = append(active, target)
		}
	}
	matched := active
	if len(matched) == 0 && includeRevoked {
		matched = revoked
	}
	switch len(matched) {
	case 1:
		return matched[0].ID, nil
	case 0:
		scope := ""
		if environmentName != "" {
			scope = " in environment " + environmentName
		}
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"no host target named %q%s - run 'ankra targets list' to see the registered targets", reference, scope))
	default:
		descriptions := make([]string, 0, len(matched))
		for _, target := range matched {
			descriptions = append(descriptions, fmt.Sprintf("%s in %s", target.ID, target.Environment))
		}
		return "", withExitCode(exitUsage, fmt.Errorf(
			"%d host targets are named %q - pass --environment or the id instead (%s)",
			len(matched), reference, strings.Join(descriptions, ", ")))
	}
}

func newTargetsGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show a host deploy target",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			environmentName, _ := command.Flags().GetString("environment")
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			hostTargetID, resolveError := resolveHostTarget(command, arguments[0], environmentName, true)
			if resolveError != nil {
				return resolveError
			}
			target, getError := apiClient.GetHostTarget(command.Context(), hostTargetID)
			if getError != nil {
				return deployLaneError("getting host target "+arguments[0], getError)
			}
			if rendered, renderError := renderStructured(command, target); rendered || renderError != nil {
				return renderError
			}
			printHostTarget(command.OutOrStdout(), target)
			return nil
		},
	}
	command.Flags().String("environment", "", "Environment to look the name up in, when several environments use it")
	registerStructuredOutputFlags(command)
	return command
}

func printHostTarget(out io.Writer, target *client.HostTarget) {
	_, _ = fmt.Fprintln(out, "Host Target:")
	_, _ = fmt.Fprintf(out, "  Name:           %s\n", target.Name)
	_, _ = fmt.Fprintf(out, "  ID:             %s\n", target.ID)
	_, _ = fmt.Fprintf(out, "  Environment:    %s\n", target.Environment)
	_, _ = fmt.Fprintf(out, "  Status:         %s\n", hostTargetDisplayStatus(*target))
	_, _ = fmt.Fprintf(out, "  Labels:         %s\n", formatHostTargetLabels(target.Labels))
	_, _ = fmt.Fprintf(out, "  Hostname:       %s\n", optionalText(target.Hostname))
	platform := "-"
	if target.OS != nil && target.Arch != nil {
		platform = *target.OS + "/" + *target.Arch
	}
	_, _ = fmt.Fprintf(out, "  Platform:       %s\n", platform)
	_, _ = fmt.Fprintf(out, "  Agent version:  %s\n", optionalText(target.AgentVersion))
	jobTypes := "-"
	if len(target.SupportedJobTypes) > 0 {
		jobTypes = strings.Join(target.SupportedJobTypes, ", ")
	}
	_, _ = fmt.Fprintf(out, "  Job types:      %s\n", jobTypes)
	_, _ = fmt.Fprintf(out, "  Last heartbeat: %s\n", formatOptionalTimestamp(target.LastHeartbeatAt))
	_, _ = fmt.Fprintf(out, "  Registered:     %s\n", formatOptionalTimestamp(target.RegisteredAt))
	if target.IsRevoked() {
		_, _ = fmt.Fprintf(out, "  Revoked:        %s by %s\n", formatOptionalTimestamp(target.RevokedAt), optionalText(target.RevokedBy))
	}
	if len(target.Running) == 0 {
		_, _ = fmt.Fprintln(out, "  Running:        -")
		return
	}
	_, _ = fmt.Fprintln(out, "  Running:")
	for _, release := range target.Running {
		activated := "-"
		if release.ActivatedAt != "" {
			activated = formatTimeAgo(release.ActivatedAt)
		}
		_, _ = fmt.Fprintf(out, "    %s  %s  (activated %s)\n", release.Release, release.Digest, activated)
	}
}

func newTargetsRevokeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "revoke <name|id>",
		Short: "Revoke a host deploy target",
		Long: `Revoke a host deploy target. Its identity token and every session stop
working at once and it receives no further deploy jobs; the releases already
on the host keep running. A revoked host registers again only with a new
join token.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			environmentName, _ := command.Flags().GetString("environment")
			yes, _ := command.Flags().GetBool("yes")
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			hostTargetID, resolveError := resolveHostTarget(command, arguments[0], environmentName, false)
			if resolveError != nil {
				return resolveError
			}
			prompt := fmt.Sprintf("Revoke host target %q (%s)? It stops receiving deploys at once. [y/N]: ",
				arguments[0], hostTargetID)
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), prompt, yes); confirmError != nil {
				return confirmError
			}
			target, revokeError := apiClient.RevokeHostTarget(command.Context(), hostTargetID)
			if revokeError != nil {
				return deployLaneError("revoking host target "+arguments[0], revokeError)
			}
			if rendered, renderError := renderStructured(command, target); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Revoked host target %s (%s).\n", target.Name, target.ID)
			return nil
		},
	}
	command.Flags().String("environment", "", "Environment to look the name up in, when several environments use it")
	command.Flags().Bool("yes", false, "Revoke without asking for confirmation")
	registerStructuredOutputFlags(command)
	return command
}

func init() {
	rootCmd.AddCommand(newTargetsCommand())
}
