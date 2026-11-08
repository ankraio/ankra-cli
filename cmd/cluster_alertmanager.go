package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The silences on the active cluster's in-cluster Alertmanager, the CLI side
// of the assistant's list_alertmanager_silences, create_alertmanager_silence
// and expire_alertmanager_silence. The platform finds the Alertmanager by its
// prometheus-operator or chart labels and reaches it through the cluster's
// Ankra agent; --namespace and --service name it when discovery cannot.

var silenceStates = []string{"active", "pending", "expired", "all"}

// newClusterAlertmanagerCommand builds `ankra cluster alertmanager`.
func newClusterAlertmanagerCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "alertmanager",
		Short: "Work with the active cluster's in-cluster Alertmanager",
		Long: `Work with the Alertmanager running in the active cluster (the one
kube-prometheus-stack or the Alertmanager chart installs), through the
cluster's Ankra agent.`,
	}
	command.AddCommand(newClusterAlertmanagerSilencesCommand())
	return command
}

func newClusterAlertmanagerSilencesCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "silences",
		Aliases: []string{"silence"},
		Short:   "List, create and expire Alertmanager silences",
		Long: `List, create and expire silences on the active cluster's Alertmanager.

Listing needs clusters.read on the cluster; creating and expiring need
alerts.write there. The Alertmanager is discovered by its labels; when the
cluster runs several, or a non-standard install, pass --namespace (and
--service, --port).

  ankra cluster alertmanager silences list
  ankra cluster alertmanager silences create --matcher alertname=KubeVersionMismatch --duration 2w --comment "pending upgrade"
  ankra cluster alertmanager silences expire <silence-id> --yes`,
	}
	command.AddCommand(newClusterAlertmanagerSilencesListCommand(), newClusterAlertmanagerSilencesCreateCommand(),
		newClusterAlertmanagerSilencesExpireCommand())
	return command
}

// registerAlertmanagerLocationFlags adds the flags that name the
// Alertmanager when discovery cannot.
func registerAlertmanagerLocationFlags(command *cobra.Command) {
	command.Flags().String("namespace", "", "Namespace of the Alertmanager Service (narrows discovery; required with --service)")
	command.Flags().String("service", "", "Name of the Alertmanager Service, for installs discovery does not find")
	command.Flags().Int("port", 0, "Port Alertmanager listens on (default 9093)")
}

func alertmanagerLocationFromFlags(cmd *cobra.Command) (client.AlertmanagerLocation, error) {
	namespace, _ := cmd.Flags().GetString("namespace")
	service, _ := cmd.Flags().GetString("service")
	port, _ := cmd.Flags().GetInt("port")
	if service != "" && namespace == "" {
		return client.AlertmanagerLocation{}, withExitCode(exitUsage, errors.New("--service needs --namespace"))
	}
	if port < 0 || port > 65535 {
		return client.AlertmanagerLocation{}, withExitCode(exitUsage, fmt.Errorf("--port %d is not a port", port))
	}
	return client.AlertmanagerLocation{Namespace: namespace, Service: service, Port: port}, nil
}

// silenceRequestError keeps the exit-code contract for a silence call: the
// platform answers a request its guardrails refuse with 400 and an
// Alertmanager it cannot choose between with 409, both fixed by changing the
// invocation, so both are usage errors. 401, 403 and 404 keep their codes.
func silenceRequestError(action string, requestError error) error {
	wrapped := fmt.Errorf("%s: %w", action, requestError)
	var unexpected *client.UnexpectedResponseError
	if errors.As(requestError, &unexpected) &&
		(unexpected.StatusCode == http.StatusBadRequest || unexpected.StatusCode == http.StatusConflict) {
		return withExitCode(exitUsage, wrapped)
	}
	return wrapped
}

func describeSilenceMatchers(matchers []client.AlertmanagerSilenceMatcher) string {
	parts := make([]string, 0, len(matchers))
	for _, matcher := range matchers {
		operator := "="
		if matcher.IsRegex {
			operator = "=~"
		}
		parts = append(parts, matcher.Name+operator+`"`+matcher.Value+`"`)
	}
	return strings.Join(parts, ", ")
}

func newClusterAlertmanagerSilencesListCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List the silences on the cluster's Alertmanager",
		Long: `List the silences on the active cluster's Alertmanager, newest ending
first. --state picks active (the default), pending, expired or all.

  ankra cluster alertmanager silences list
  ankra cluster alertmanager silences list --state all -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, _ := cmd.Flags().GetString("state")
			if !containsString(silenceStates, state) {
				return withExitCode(exitUsage, fmt.Errorf("--state must be one of %s", strings.Join(silenceStates, ", ")))
			}
			location, locationError := alertmanagerLocationFromFlags(cmd)
			if locationError != nil {
				return locationError
			}
			cluster, clusterError := resolveActiveCluster(cmd)
			if clusterError != nil {
				return clusterError
			}
			list, listError := apiClient.ListAlertmanagerSilences(cluster.ID, state, location)
			if listError != nil {
				return silenceRequestError("listing Alertmanager silences", listError)
			}
			if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
				return renderError
			}
			out := cmd.OutOrStdout()
			if len(list.Silences) == 0 {
				_, _ = fmt.Fprintf(out, "No %s silences on %s/%s in cluster %q.\n", list.State,
					list.Alertmanager.Namespace, list.Alertmanager.Service, cluster.Name)
				return nil
			}
			writer := table.NewWriter()
			writer.SetOutputMirror(out)
			writer.SetStyle(table.StyleRounded)
			writer.AppendHeader(table.Row{"ID", "State", "Matchers", "Ends", "Created by", "Comment"})
			for _, silence := range list.Silences {
				writer.AppendRow(table.Row{silence.ID, silence.State, describeSilenceMatchers(silence.Matchers),
					silence.EndsAt, silence.CreatedBy, silence.Comment})
			}
			writer.Render()
			_, _ = fmt.Fprintf(out, "\n%d silence(s) on %s/%s\n", list.Count,
				list.Alertmanager.Namespace, list.Alertmanager.Service)
			return nil
		},
	}
	command.Flags().String("state", "active", "Silences to list: active, pending, expired or all")
	registerAlertmanagerLocationFlags(command)
	registerStructuredOutputFlags(command)
	return command
}

// parseSilenceMatcher reads name=value (an exact match) or name=~regex.
func parseSilenceMatcher(text string) (client.AlertmanagerSilenceMatcher, error) {
	name, value, hasSeparator := strings.Cut(text, "=")
	name = strings.TrimSpace(name)
	if !hasSeparator || name == "" {
		return client.AlertmanagerSilenceMatcher{}, fmt.Errorf("--matcher %q must be name=value or name=~regex", text)
	}
	if regex, isRegex := strings.CutPrefix(value, "~"); isRegex {
		return client.AlertmanagerSilenceMatcher{Name: name, Value: regex, IsRegex: true}, nil
	}
	return client.AlertmanagerSilenceMatcher{Name: name, Value: value}, nil
}

func newClusterAlertmanagerSilencesCreateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "create --matcher <name=value> --comment <why>",
		Short: "Silence the alerts that match every matcher",
		Long: `Create a silence on the active cluster's Alertmanager. An alert is
silenced while every --matcher matches it: name=value is an exact match,
name=~regex a regular expression, and at least one must be exact so a
silence cannot mute everything. --duration takes a number and a unit (s, m,
h, d, w) from 5m to 30d, 2h by default. --comment says why; operators see it
in Alertmanager. The silence starts at once and is attributed to you.

  ankra cluster alertmanager silences create --matcher alertname=KubeVersionMismatch \
    --matcher cluster=ankra-production --duration 2w --comment "pending component version alignment"
  ankra cluster alertmanager silences create --matcher alertname=PodCrashLooping --matcher 'pod=~web-.*' \
    --duration 30m --comment "rolling deploy"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rawMatchers, _ := cmd.Flags().GetStringArray("matcher")
			if len(rawMatchers) == 0 {
				return withExitCode(exitUsage, errors.New("at least one --matcher is required"))
			}
			matchers := make([]client.AlertmanagerSilenceMatcher, 0, len(rawMatchers))
			for _, rawMatcher := range rawMatchers {
				matcher, matcherError := parseSilenceMatcher(rawMatcher)
				if matcherError != nil {
					return withExitCode(exitUsage, matcherError)
				}
				matchers = append(matchers, matcher)
			}
			comment, _ := cmd.Flags().GetString("comment")
			if strings.TrimSpace(comment) == "" {
				return withExitCode(exitUsage, errors.New("--comment is required: it is the reason operators see"))
			}
			duration, _ := cmd.Flags().GetString("duration")
			location, locationError := alertmanagerLocationFromFlags(cmd)
			if locationError != nil {
				return locationError
			}
			cluster, clusterError := resolveActiveCluster(cmd)
			if clusterError != nil {
				return clusterError
			}
			created, createError := apiClient.CreateAlertmanagerSilence(cluster.ID, client.CreateAlertmanagerSilenceRequest{
				Matchers:  matchers,
				Duration:  duration,
				Comment:   comment,
				Namespace: location.Namespace,
				Service:   location.Service,
				Port:      location.Port,
			})
			if createError != nil {
				return silenceRequestError("creating the Alertmanager silence", createError)
			}
			if rendered, renderError := renderStructured(cmd, created); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Silence %s created on cluster %q (%s/%s)\n  Matchers: %s\n  Until:    %s\n",
				created.SilenceID, cluster.Name, created.Alertmanager.Namespace, created.Alertmanager.Service,
				describeSilenceMatchers(created.Matchers), created.EndsAt)
			return nil
		},
	}
	command.Flags().StringArray("matcher", nil, "Label matcher as name=value or name=~regex (repeatable, at least one)")
	command.Flags().String("duration", "", "How long the silence lasts, e.g. 30m, 2h, 2w (5m to 30d; default 2h)")
	command.Flags().String("comment", "", "Why the alerts are silenced (required)")
	registerAlertmanagerLocationFlags(command)
	registerStructuredOutputFlags(command)
	return command
}

func newClusterAlertmanagerSilencesExpireCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "expire <silence-id>",
		Short: "Expire a silence so its alerts notify again",
		Long: `Expire one silence on the active cluster's Alertmanager; the alerts it
muted notify again. 'list' shows the ids. The command asks first (--yes
skips, declining exits 4); a silence Alertmanager does not have exits 3.

  ankra cluster alertmanager silences expire 35833f27-aaed-47c3-956e-9b071140f062 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			silenceID := strings.TrimSpace(args[0])
			if silenceID == "" {
				return withExitCode(exitUsage, errors.New("a silence id is required"))
			}
			location, locationError := alertmanagerLocationFromFlags(cmd)
			if locationError != nil {
				return locationError
			}
			cluster, clusterError := resolveActiveCluster(cmd)
			if clusterError != nil {
				return clusterError
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
				fmt.Sprintf("Expire silence %s on cluster %q? Its alerts will notify again. [y/N]: ", silenceID, cluster.Name),
				yes); confirmError != nil {
				return confirmError
			}
			expired, expireError := apiClient.ExpireAlertmanagerSilence(cluster.ID, silenceID, location)
			if expireError != nil {
				return silenceRequestError("expiring the Alertmanager silence", expireError)
			}
			if rendered, renderError := renderStructured(cmd, expired); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Silence %s expired on cluster %q (%s/%s)\n",
				expired.SilenceID, cluster.Name, expired.Alertmanager.Namespace, expired.Alertmanager.Service)
			return nil
		},
	}
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerAlertmanagerLocationFlags(command)
	registerStructuredOutputFlags(command)
	return command
}

func init() {
	clusterCmd.AddCommand(newClusterAlertmanagerCommand())
}
