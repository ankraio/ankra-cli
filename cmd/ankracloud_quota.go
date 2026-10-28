package cmd

// `ankra cluster ankracloud quota` (ankra-bjkszh.11).
//
// Running out of Ankra Cloud account quota used to show up only after the
// fact: a PersistentVolumeClaim Pending on ProvisioningFailed, or a node-group
// scale failing inside a reconcile operation. This verb reads the account's
// limits, usage and what is left through the cluster's own credential, so the
// headroom can be checked before a deploy that needs it.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var ankraCloudQuotaCmd = &cobra.Command{
	Use:   "quota <cluster_id|name>",
	Short: "Show the Ankra Cloud account quota an Ankra Cloud cluster draws on",
	Long: "Show the limits of the Ankra Cloud account behind an Ankra Cloud cluster, what the account " +
		"already holds, and what is left: servers, cores, memory, storage, floating IPs, private networks " +
		"and routers.\n\n" +
		"Storage is shared by server disks and every PersistentVolumeClaim the Ankra Cloud CSI provisions, " +
		"so a claim larger than what is left stays Pending. Applies and stack deploys that need more storage " +
		"than is left say so in their warnings.\n\n" +
		"Quota applies to Ankra Cloud clusters only: a cluster built on another provider, or imported, " +
		"is refused with exit code 2.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		report, fetchError := apiClient.GetAnkraCloudQuotas(clusterID)
		if fetchError != nil {
			return ankraCloudQuotaError(args[0], clusterID, fetchError)
		}
		if handled, renderError := renderStructured(cmd, report); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		printClusterQuotaReport(cmd.OutOrStdout(), report)
		return nil
	},
}

// ankraCloudQuotaError explains a failed quota read. The quota route answers
// 404 for a cluster that exists but was not built on Ankra Cloud, which used
// to reach the user as a bare "404 Not Found" (exit 3, as if the cluster did
// not exist). On a 404 the cluster's kind is read: a cluster of another kind
// is a wrong argument, so it exits 2 with a sentence saying so. When the
// cluster is an Ankra Cloud one, or its kind cannot be read, the platform's
// own answer is kept.
func ankraCloudQuotaError(typed string, clusterID string, fetchError error) error {
	wrapped := fmt.Errorf("fetching the Ankra Cloud quota: %w", fetchError)
	var unexpected *client.UnexpectedResponseError
	if !errors.As(fetchError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		return wrapped
	}
	cluster, lookupError := apiClient.GetClusterByID(clusterID)
	if lookupError != nil || cluster.Kind == "" || cluster.Kind == "ankracloud" {
		return wrapped
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"%s is not an Ankra Cloud cluster (its kind is %s); quota applies to Ankra Cloud clusters only",
		typed, cluster.Kind))
}

// quotaDimensionLabels names each dimension the way the Ankra Cloud console
// does.
var quotaDimensionLabels = map[string]string{
	"servers":             "Servers",
	"vcpus":               "Cores",
	"memory_gb":           "Memory",
	"storage_gb":          "Storage",
	"floating_ips":        "Floating IPs",
	"networks":            "Private networks",
	"routers":             "Routers",
	"volume_gb":           "Volume storage",
	"load_balancers":      "Load balancers",
	"primary_ips":         "Primary IPs",
	"public_ipv4":         "Public IPv4",
	"instances":           "Instances",
	"ssh_keys":            "SSH keys",
	"firewalls":           "Firewalls",
	"reserved_ips":        "Reserved IPs",
	"volumes":             "Volumes",
	"public_ipv6":         "Public IPv6",
	"kubernetes_clusters": "Kubernetes clusters",
}

func quotaDimensionLabel(dimension string) string {
	if label, known := quotaDimensionLabels[dimension]; known {
		return label
	}
	return dimension
}

func formatQuotaAmount(value float64, unit string) string {
	amount := strconv.FormatFloat(value, 'f', -1, 64)
	if unit == "GB" {
		return amount + " GiB"
	}
	return amount
}

func printClusterQuotaReport(out io.Writer, report *client.ClusterQuotaReport) {
	quotaTable := table.NewWriter()
	quotaTable.SetOutputMirror(out)
	quotaTable.SetStyle(table.StyleRounded)
	quotaTable.AppendHeader(table.Row{"Quota", "Used", "Limit", "Left", "Use"})
	for _, dimension := range report.Dimensions {
		limit, left, utilisation := "unknown", "unknown", "-"
		if dimension.Limit != nil {
			limit = formatQuotaAmount(*dimension.Limit, dimension.Unit)
		}
		if dimension.Remaining != nil {
			left = formatQuotaAmount(*dimension.Remaining, dimension.Unit)
		}
		if dimension.UtilisationPercent != nil {
			utilisation = fmt.Sprintf("%.0f%%", *dimension.UtilisationPercent)
		}
		if dimension.Exhausted {
			utilisation += " (full)"
		}
		quotaTable.AppendRow(table.Row{quotaDimensionLabel(dimension.Dimension),
			formatQuotaAmount(dimension.Used, dimension.Unit), limit, left, utilisation})
	}
	quotaTable.Render()

	if len(report.Pressured) > 0 {
		names := make([]string, 0, len(report.Pressured))
		for _, dimension := range report.Pressured {
			names = append(names, strings.ToLower(quotaDimensionLabel(dimension.Dimension)))
		}
		_, _ = fmt.Fprintf(out, "\nNear or at the limit: %s.\n", strings.Join(names, ", "))
	}
	if report.Increase.Instructions != "" {
		_, _ = fmt.Fprintf(out, "\nTo raise a limit: %s\n", report.Increase.Instructions)
	}
}

// formatValidationWarning renders one advisory finding of a validate, apply
// or stack write, leaving out the parts the platform did not fill in.
func formatValidationWarning(warning client.ValidationWarning) string {
	subject := strings.TrimSpace(strings.Join([]string{warning.Kind, quoteIfSet(warning.Name)}, " "))
	if subject == "" {
		return warning.Message
	}
	return subject + ": " + warning.Message
}

func quoteIfSet(value string) string {
	if value == "" {
		return ""
	}
	return strconv.Quote(value)
}

// printValidationWarnings prints the advisory findings of a write under a
// Warnings heading; nothing when there are none.
func printValidationWarnings(out io.Writer, warnings []client.ValidationWarning) {
	if len(warnings) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nWarnings:")
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(out, "  - %s\n", formatValidationWarning(warning))
	}
}

func init() {
	registerStructuredOutputFlags(ankraCloudQuotaCmd)
	ankraCloudCmd.AddCommand(ankraCloudQuotaCmd)
}
