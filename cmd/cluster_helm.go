package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var clusterHelmCmd = &cobra.Command{
	Use:   "helm",
	Short: "Manage Helm releases in the cluster",
	Long:  "Commands to list, inspect, roll back, upgrade and uninstall Helm releases running in the cluster.",
}

var clusterHelmReleasesCmd = &cobra.Command{
	Use:   "releases",
	Short: "List Helm releases in the cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		namespace, _ := cmd.Flags().GetString("namespace")
		allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
		outputFormat, _ := cmd.Flags().GetString("output")

		opts := &client.HelmReleasesOptions{AllNamespaces: true}
		if namespace != "" && !allNamespaces {
			opts = &client.HelmReleasesOptions{Namespace: namespace}
		}

		response, err := apiClient.ListHelmReleases(cluster.ID, opts)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			jsonData, _ := json.MarshalIndent(response, "", "  ")
			fmt.Println(string(jsonData))
			return nil
		}

		allItems := []interface{}{}
		for _, resp := range response.ResourceResponses {
			allItems = append(allItems, resp.Items...)
		}

		if len(allItems) == 0 {
			fmt.Println("No Helm releases found.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Namespace", "Revision", "Status", "Chart", "App Version"})

		for _, item := range allItems {
			if release, ok := item.(map[string]interface{}); ok {
				status := getNestedString(release, "status")
				switch status {
				case "deployed":
					status = text.FgGreen.Sprint(status)
				case "failed":
					status = text.FgRed.Sprint(status)
				case "pending-install", "pending-upgrade":
					status = text.FgYellow.Sprint(status)
				}

				t.AppendRow(table.Row{
					getNestedString(release, "name"),
					getNestedString(release, "namespace"),
					getNestedString(release, "revision"),
					status,
					getNestedString(release, "chart"),
					getNestedString(release, "app_version"),
				})
			}
		}
		t.Render()
		return nil
	},
}

var clusterHelmUninstallCmd = &cobra.Command{
	Use:   "uninstall <release_name>",
	Short: "Uninstall a Helm release from the cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		releaseName := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")

		if namespace == "" {
			return errors.New("--namespace (-n) is required for uninstall")
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Uninstall Helm release %q (namespace %q) from cluster %q? [y/N]: ", releaseName, namespace, cluster.Name),
			yes); err != nil {
			return err
		}

		result, err := apiClient.UninstallHelmRelease(cluster.ID, releaseName, namespace)
		if err != nil {
			return err
		}

		fmt.Printf("Helm release '%s' uninstalled from namespace '%s'.\n", releaseName, namespace)
		if result.Message != nil && *result.Message != "" {
			fmt.Printf("  Message: %s\n", *result.Message)
		}
		return nil
	},
}

var clusterHelmGetCmd = &cobra.Command{
	Use:   "get <release_name>",
	Short: "Show a Helm release: chart, status, values and notes",
	Long: `Show one Helm release - the chart and revision it is on, when it was
deployed, the values it was installed with and the chart's notes.

'-o values' prints only the values the release was installed with, as YAML:
edit that file and hand it back to 'cluster helm upgrade --values'.

Examples:
  ankra cluster helm get traefik -n traefik
  ankra cluster helm get traefik -n traefik -o values > traefik-values.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		releaseName := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")
		outputFormat, _ := cmd.Flags().GetString("output")
		if namespace == "" {
			return withExitCode(exitUsage, errors.New("--namespace (-n) is required for helm get"))
		}
		switch outputFormat {
		case "table", "json", "yaml", "values":
		default:
			return withExitCode(exitUsage, fmt.Errorf("unsupported output format %q: use table, json, yaml or values", outputFormat))
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		detail, err := apiClient.GetHelmReleaseDetail(cluster.ID, namespace, releaseName)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		switch outputFormat {
		case "json":
			encoded, marshalError := json.MarshalIndent(detail, "", "  ")
			if marshalError != nil {
				return fmt.Errorf("marshalling to JSON: %w", marshalError)
			}
			_, _ = fmt.Fprintln(out, string(encoded))
			return nil
		case "yaml":
			encoded, marshalError := yamlWithWireKeys(detail)
			if marshalError != nil {
				return fmt.Errorf("marshalling to YAML: %w", marshalError)
			}
			_, _ = fmt.Fprint(out, string(encoded))
			return nil
		case "values":
			return writeHelmValuesYAML(out, detail.UserValues)
		}

		metadata := detail.Metadata
		_, _ = fmt.Fprintf(out, "Name:           %s\n", metadata.Name)
		_, _ = fmt.Fprintf(out, "Namespace:      %s\n", metadata.Namespace)
		_, _ = fmt.Fprintf(out, "Revision:       %d\n", metadata.Revision)
		_, _ = fmt.Fprintf(out, "Status:         %s\n", metadata.Status)
		_, _ = fmt.Fprintf(out, "Chart:          %s\n", stringFromPointer(metadata.Chart))
		_, _ = fmt.Fprintf(out, "Chart version:  %s\n", stringFromPointer(metadata.ChartVersion))
		_, _ = fmt.Fprintf(out, "App version:    %s\n", stringFromPointer(metadata.AppVersion))
		_, _ = fmt.Fprintf(out, "First deployed: %s\n", stringFromPointer(metadata.FirstDeployed))
		_, _ = fmt.Fprintf(out, "Last deployed:  %s\n", stringFromPointer(metadata.LastDeployed))
		_, _ = fmt.Fprintf(out, "Description:    %s\n", stringFromPointer(metadata.Description))
		_, _ = fmt.Fprintf(out, "\nUser values:\n")
		if len(detail.UserValues) == 0 {
			_, _ = fmt.Fprintln(out, "  (chart defaults)")
		} else if err := writeHelmValuesYAML(out, detail.UserValues); err != nil {
			return err
		}
		if detail.Notes != nil && strings.TrimSpace(*detail.Notes) != "" {
			_, _ = fmt.Fprintf(out, "\nNotes:\n%s\n", strings.TrimRight(*detail.Notes, "\n"))
		}
		return nil
	},
}

var clusterHelmHistoryCmd = &cobra.Command{
	Use:   "history <release_name>",
	Short: "List the revisions of a Helm release",
	Long: `List a Helm release's revisions, newest first, with the chart and status of
each - the revision numbers 'cluster helm rollback --revision' takes.

Example:
  ankra cluster helm history traefik -n traefik`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		releaseName := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")
		limit, _ := cmd.Flags().GetInt("limit")
		outputFormat, _ := cmd.Flags().GetString("output")
		if namespace == "" {
			return withExitCode(exitUsage, errors.New("--namespace (-n) is required for helm history"))
		}
		if limit < 1 || limit > 200 {
			return withExitCode(exitUsage, errors.New("--limit must be between 1 and 200"))
		}
		if outputFormat != "table" && outputFormat != "json" {
			return withExitCode(exitUsage, fmt.Errorf("unsupported output format %q: use table or json", outputFormat))
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		history, err := apiClient.GetHelmReleaseHistory(cluster.ID, namespace, releaseName, limit)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		if outputFormat == "json" {
			encoded, marshalError := json.MarshalIndent(history, "", "  ")
			if marshalError != nil {
				return fmt.Errorf("marshalling to JSON: %w", marshalError)
			}
			_, _ = fmt.Fprintln(out, string(encoded))
			return nil
		}
		if len(history.Revisions) == 0 {
			_, _ = fmt.Fprintf(out, "No revisions found for release %q in namespace %q.\n", releaseName, namespace)
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(out)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Revision", "Updated", "Status", "Chart", "App Version", "Description"})
		for _, revision := range history.Revisions {
			status := revision.Status
			switch status {
			case "deployed":
				status = text.FgGreen.Sprint(status)
			case "failed":
				status = text.FgRed.Sprint(status)
			case "superseded":
				status = text.FgHiBlack.Sprint(status)
			}
			t.AppendRow(table.Row{
				revision.Revision,
				stringFromPointer(revision.Updated),
				status,
				stringFromPointer(revision.Chart),
				stringFromPointer(revision.AppVersion),
				stringFromPointer(revision.Description),
			})
		}
		t.Render()
		return nil
	},
}

var clusterHelmRollbackCmd = &cobra.Command{
	Use:   "rollback <release_name>",
	Short: "Roll a Helm release back to an earlier revision",
	Long: `Roll a Helm release back to one of its earlier revisions, as listed by
'cluster helm history'. The rollback runs on the cluster's Ankra agent and,
by default, waits for the rolled-back resources to become ready before
returning. A release that an Ankra addon manages is refused: change the
addon's values in the stack instead, so Git stays the source of truth.

Examples:
  ankra cluster helm rollback traefik -n traefik --revision 3
  ankra cluster helm rollback traefik -n traefik --revision 3 --wait=false --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		releaseName := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")
		revision, _ := cmd.Flags().GetInt("revision")
		wait, _ := cmd.Flags().GetBool("wait")
		timeoutSeconds, _ := cmd.Flags().GetInt("timeout")
		yes, _ := cmd.Flags().GetBool("yes")
		if namespace == "" {
			return withExitCode(exitUsage, errors.New("--namespace (-n) is required for helm rollback"))
		}
		if revision < 1 {
			return withExitCode(exitUsage, errors.New("--revision is required and must be 1 or higher (see 'cluster helm history')"))
		}
		if timeoutSeconds < 1 || timeoutSeconds > 3600 {
			return withExitCode(exitUsage, errors.New("--timeout must be between 1 and 3600 seconds"))
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Roll back Helm release %q (namespace %q) on cluster %q to revision %d? [y/N]: ",
				releaseName, namespace, cluster.Name, revision),
			yes); err != nil {
			return err
		}

		result, err := apiClient.RollbackHelmRelease(cluster.ID, namespace, releaseName, client.RollbackHelmReleaseRequest{
			Revision:       revision,
			Wait:           wait,
			TimeoutSeconds: timeoutSeconds,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Helm release %q rolled back to revision %d; it is now revision %d (%s).\n",
			releaseName, revision, result.Revision, formatElapsedMilliseconds(result.ElapsedMS))
		return nil
	},
}

var clusterHelmUpgradeCmd = &cobra.Command{
	Use:   "upgrade <release_name>",
	Short: "Upgrade a Helm release in place with a chart and a values file",
	Long: `Upgrade a Helm release in place: the agent runs 'helm upgrade' with the
chart reference and the values file you pass. The values file REPLACES the
release's values wholesale - start from 'cluster helm get <release> -o values'
so nothing you did not mean to change resets to a chart default. Without
--version the release stays on the chart version it already runs.

A release that an Ankra addon manages is refused: change the addon in the
stack instead, so Git stays the source of truth.

Examples:
  ankra cluster helm get traefik -n traefik -o values > traefik-values.yaml
  ankra cluster helm upgrade traefik -n traefik --chart traefik/traefik --values traefik-values.yaml
  ankra cluster helm upgrade traefik -n traefik --chart traefik --repo https://traefik.github.io/charts --version 30.0.0 --values traefik-values.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		releaseName := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")
		chartReference, _ := cmd.Flags().GetString("chart")
		repositoryURL, _ := cmd.Flags().GetString("repo")
		chartVersion, _ := cmd.Flags().GetString("version")
		valuesPath, _ := cmd.Flags().GetString("values")
		wait, _ := cmd.Flags().GetBool("wait")
		timeoutSeconds, _ := cmd.Flags().GetInt("timeout")
		yes, _ := cmd.Flags().GetBool("yes")
		if namespace == "" {
			return withExitCode(exitUsage, errors.New("--namespace (-n) is required for helm upgrade"))
		}
		if chartReference == "" {
			return withExitCode(exitUsage, errors.New("--chart is required (e.g. traefik/traefik, or a chart name with --repo)"))
		}
		if valuesPath == "" {
			return withExitCode(exitUsage, errors.New("--values <file> is required: the file replaces the release's values, start from 'cluster helm get <release> -o values'"))
		}
		if timeoutSeconds < 1 || timeoutSeconds > 3600 {
			return withExitCode(exitUsage, errors.New("--timeout must be between 1 and 3600 seconds"))
		}
		valuesYAML, readError := os.ReadFile(valuesPath)
		if readError != nil {
			return withExitCode(exitUsage, fmt.Errorf("reading --values file: %w", readError))
		}
		var parsedValues interface{}
		if parseError := yaml.Unmarshal(valuesYAML, &parsedValues); parseError != nil {
			return withExitCode(exitUsage, fmt.Errorf("--values file %s is not valid YAML: %w", valuesPath, parseError))
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		chartLabel := chartReference
		if chartVersion != "" {
			chartLabel += " " + chartVersion
		}
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Upgrade Helm release %q (namespace %q) on cluster %q with chart %s and the values in %s? [y/N]: ",
				releaseName, namespace, cluster.Name, chartLabel, valuesPath),
			yes); err != nil {
			return err
		}

		result, err := apiClient.UpgradeHelmRelease(cluster.ID, namespace, releaseName, client.UpgradeHelmReleaseRequest{
			ChartRef:       chartReference,
			RepoURL:        repositoryURL,
			ChartVersion:   chartVersion,
			ValuesYAML:     string(valuesYAML),
			Wait:           wait,
			TimeoutSeconds: timeoutSeconds,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Helm release %q upgraded; it is now revision %d (%s).\n",
			releaseName, result.Revision, formatElapsedMilliseconds(result.ElapsedMS))
		return nil
	},
}

// yamlWithWireKeys renders a value with the same keys '-o json' prints. The
// client structs carry json tags only, so marshalling them straight to YAML
// would emit lower-cased Go field names ("uservalues") instead of the wire
// names ("user_values") and the two machine-readable formats would disagree.
func yamlWithWireKeys(value interface{}) ([]byte, error) {
	encoded, marshalError := json.Marshal(value)
	if marshalError != nil {
		return nil, marshalError
	}
	var generic interface{}
	if unmarshalError := json.Unmarshal(encoded, &generic); unmarshalError != nil {
		return nil, unmarshalError
	}
	return yaml.Marshal(generic)
}

func writeHelmValuesYAML(out io.Writer, values map[string]interface{}) error {
	if values == nil {
		values = map[string]interface{}{}
	}
	encoded, marshalError := yaml.Marshal(values)
	if marshalError != nil {
		return fmt.Errorf("marshalling values to YAML: %w", marshalError)
	}
	_, _ = fmt.Fprint(out, string(encoded))
	return nil
}

func stringFromPointer(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatElapsedMilliseconds(elapsedMilliseconds int) string {
	return fmt.Sprintf("%.1fs", float64(elapsedMilliseconds)/1000)
}

func init() {
	clusterHelmReleasesCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
	clusterHelmReleasesCmd.Flags().BoolP("all-namespaces", "A", false, "List across all namespaces (default)")
	clusterHelmReleasesCmd.Flags().StringP("output", "o", "table", "Output format: table, json")

	clusterHelmUninstallCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")
	clusterHelmUninstallCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	clusterHelmGetCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")
	clusterHelmGetCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml, values")

	clusterHelmHistoryCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")
	clusterHelmHistoryCmd.Flags().Int("limit", 25, "Number of revisions to show (1-200)")
	clusterHelmHistoryCmd.Flags().StringP("output", "o", "table", "Output format: table, json")

	clusterHelmRollbackCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")
	clusterHelmRollbackCmd.Flags().Int("revision", 0, "Revision to roll back to (required, see 'cluster helm history')")
	clusterHelmRollbackCmd.Flags().Bool("wait", true, "Wait for the rolled-back resources to become ready")
	clusterHelmRollbackCmd.Flags().Int("timeout", 600, "Seconds to wait for the rollback (1-3600)")
	clusterHelmRollbackCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	clusterHelmUpgradeCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")
	clusterHelmUpgradeCmd.Flags().String("chart", "", "Chart reference (required): repo/name, an OCI reference, or a name with --repo")
	clusterHelmUpgradeCmd.Flags().String("repo", "", "Chart repository URL when --chart is a bare chart name")
	clusterHelmUpgradeCmd.Flags().String("version", "", "Chart version to upgrade to (default: the version the release already runs)")
	clusterHelmUpgradeCmd.Flags().StringP("values", "f", "", "Values file that replaces the release's values (required)")
	clusterHelmUpgradeCmd.Flags().Bool("wait", true, "Wait for the upgraded resources to become ready")
	clusterHelmUpgradeCmd.Flags().Int("timeout", 600, "Seconds to wait for the upgrade (1-3600)")
	clusterHelmUpgradeCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	clusterHelmCmd.AddCommand(clusterHelmReleasesCmd)
	clusterHelmCmd.AddCommand(clusterHelmGetCmd)
	clusterHelmCmd.AddCommand(clusterHelmHistoryCmd)
	clusterHelmCmd.AddCommand(clusterHelmRollbackCmd)
	clusterHelmCmd.AddCommand(clusterHelmUpgradeCmd)
	clusterHelmCmd.AddCommand(clusterHelmUninstallCmd)

	clusterCmd.AddCommand(clusterHelmCmd)
}
