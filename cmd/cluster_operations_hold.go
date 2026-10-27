package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// The staff recovery levers of a failed Kubernetes version hop: the
// operations hold (`ankra cluster operations hold set|get|lift`) and the
// version record repair (`ankra cluster upgrade rollback-record`). They are
// Ankra staff operations: the platform admits only a signed-in staff browser
// session for them and refuses API tokens, so these commands never use the
// saved login. They read the staff session from ANKRA_STAFF_SESSION and take
// the cluster by id, because a staff member is normally not a member of the
// customer's organisation and cluster names would not resolve.

const (
	envAnkraStaffSession = "ANKRA_STAFF_SESSION"

	minimumOperationsHold = time.Minute
	maximumOperationsHold = 24 * time.Hour
	staffRecoveryTimeout  = 60 * time.Second

	// annotationClusterIDOnly marks a command that takes a cluster id and
	// cannot resolve a name, with the reason; the cluster-argument ratchet
	// (TestEveryClusterArgumentAdvertisesTheName) exempts it.
	annotationClusterIDOnly  = "ankra.cluster_id_only"
	staffClusterIDOnlyReason = "staff session: the staff member is not a member of the cluster's organisation, so names do not resolve"
)

// staffRecoveryClient is the slice of client.StaffSessionClient these
// commands use; tests replace newStaffRecoveryClient.
type staffRecoveryClient interface {
	GetOperationsHold(ctx context.Context, clusterID string) (*client.OperationsHold, error)
	SetOperationsHold(ctx context.Context, clusterID string, duration time.Duration, reason string) (*client.OperationsHold, error)
	LiftOperationsHold(ctx context.Context, clusterID string, reason string, force bool) (*client.OperationsHoldLift, error)
	RollbackK8sVersionRecord(ctx context.Context, clusterID string, version string, reason string) (*client.K8sVersionRecordRepair, error)
}

var newStaffRecoveryClient = func(baseURL string, sessionToken string) staffRecoveryClient {
	return client.NewStaffSessionClient(baseURL, sessionToken)
}

// staffRecoveryClientFor resolves the staff session and the platform URL
// (--base-url, then ANKRA_BASE_URL, then the saved login's, then the
// default) and validates the cluster id.
func staffRecoveryClientFor(cmd *cobra.Command, clusterArgument string) (staffRecoveryClient, string, error) {
	clusterID := strings.TrimSpace(clusterArgument)
	if _, parseError := uuid.Parse(clusterID); parseError != nil {
		return nil, "", withExitCode(exitUsage, fmt.Errorf(
			"%q is not a cluster id: staff recovery commands take the cluster's UUID, not its name", clusterArgument))
	}
	sessionToken := strings.TrimSpace(os.Getenv(envAnkraStaffSession))
	if sessionToken == "" {
		return nil, "", withExitCode(exitAuth, fmt.Errorf(
			"%s is not set: this is an Ankra staff operation and needs a signed-in staff session "+
				"(the ankra_session cookie of your admin-portal sign-in); an API token cannot call it", envAnkraStaffSession))
	}
	rawBaseURL := ""
	if flagBaseURL, isSet := flagValue(cmd.Root().PersistentFlags().Lookup("base-url")); isSet && flagBaseURL != "" {
		rawBaseURL = flagBaseURL
	} else if environmentBaseURL := os.Getenv(envAnkraBaseURL); environmentBaseURL != "" {
		rawBaseURL = environmentBaseURL
	} else if _, savedBaseURL := readSavedCredentials(); savedBaseURL != "" {
		rawBaseURL = savedBaseURL
	} else {
		rawBaseURL = defaultBaseURL
	}
	normalized, normalizeError := client.NormalizeBaseURL(rawBaseURL, os.Getenv(envAllowInsecureHTTP) == "1")
	if normalizeError != nil {
		return nil, "", withExitCode(exitUsage, fmt.Errorf("invalid Ankra base URL: %w", normalizeError))
	}
	return newStaffRecoveryClient(normalized, sessionToken), clusterID, nil
}

// staffRecoveryError classifies a refusal: a 422 (a bad duration, version
// or a missing reason) is fixed by changing the invocation.
func staffRecoveryError(action string, requestError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(requestError, &unexpected) && unexpected.StatusCode == 422 {
		return withExitCode(exitUsage, fmt.Errorf("%s: %w", action, requestError))
	}
	return fmt.Errorf("%s: %w", action, requestError)
}

func requiredReasonFlag(cmd *cobra.Command) (string, error) {
	reason, _ := cmd.Flags().GetString("reason")
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", withExitCode(exitUsage, errors.New("--reason is required: it is recorded on the audit row"))
	}
	return reason, nil
}

func formatInstant(instant *time.Time) string {
	if instant == nil {
		return "-"
	}
	return instant.UTC().Format(time.RFC3339)
}

func printOperationsHold(out io.Writer, hold *client.OperationsHold) {
	if !hold.Held {
		_, _ = fmt.Fprintf(out, "No operations hold on cluster %s: the platform acts on it normally.\n", hold.ClusterID)
	} else {
		_, _ = fmt.Fprintf(out, "Cluster %s is held: no new write operation is admitted.\n", hold.ClusterID)
		_, _ = fmt.Fprintf(out, "  Until:     %s (%s left)\n", formatInstant(hold.BlockedUntil),
			(time.Duration(hold.RemainingSeconds) * time.Second).String())
		switch hold.Source {
		case "staff_hold":
			setBy := "-"
			if hold.SetBy != nil {
				setBy = *hold.SetBy
			}
			reason := "-"
			if hold.Reason != nil {
				reason = *hold.Reason
			}
			_, _ = fmt.Fprintf(out, "  Set by:    %s at %s\n", setBy, formatInstant(hold.SetAt))
			_, _ = fmt.Fprintf(out, "  Reason:    %s\n", reason)
		default:
			_, _ = fmt.Fprintln(out, "  Set by:    not a staff hold (a force stop's block or a direct database write)")
		}
	}
	if check := hold.RecordCheck; check != nil {
		verdict := "NOT confirmed yet (the lift is refused until it is)"
		if check.Confirmed {
			verdict = "confirmed"
		}
		_, _ = fmt.Fprintf(out, "Version record repair to %s at %s: %s\n", check.Version,
			check.RepairedAt.UTC().Format(time.RFC3339), verdict)
		for _, node := range check.Nodes {
			name := node.Name
			if name == "" {
				name = node.ResourceID
			}
			running := node.RunningVersion
			if running == "" {
				running = "not seen"
			}
			_, _ = fmt.Fprintf(out, "  %-14s %-14s %-8s pins %s, read binary %s, kubelet %s at %s\n",
				node.Status, node.Role, name, node.Version, stringOrDash(node.ObservedVersion), running,
				formatInstant(node.ObservedAt))
		}
	}
}

func stringOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

var clusterOperationsHoldCmd = &cobra.Command{
	Use:   "hold",
	Short: "Hold a cluster still during a manual recovery (Ankra staff only)",
	Long: `Set, read or lift a cluster's operations hold.

While a hold is in force the platform admits no new write operation on the
cluster: reconciliation, retries, node remediation and Kubernetes upgrades all
stop, while reads keep running. It is the lever that keeps the platform from
undoing a manual recovery, for example after a failed Kubernetes version hop.

These are Ankra staff operations. The platform refuses API tokens for them, so
they do not use your saved login: set ANKRA_STAFF_SESSION to the session of
your admin-portal sign-in, and pass the cluster by id. Every change is audited
with your name and the reason.`,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
}

var clusterOperationsHoldSetCmd = &cobra.Command{
	Annotations: map[string]string{annotationClusterIDOnly: staffClusterIDOnlyReason},
	Use:         "set <cluster_id> --for <duration> --reason <text>",
	Short:       "Hold the cluster for a duration (1m to 24h), replacing any hold in force",
	Long: `Hold the cluster for --for from now (1m to 24h). Running it again extends
or shortens the hold to the new duration; both are audited.

Examples:
  ankra cluster operations hold set 62f4559a-a44d-46d7-aab3-a57c9dd6b4c6 --for 4h --reason "k3s hop failed, manual rollback"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		duration, _ := cmd.Flags().GetDuration("for")
		if duration < minimumOperationsHold || duration > maximumOperationsHold {
			return withExitCode(exitUsage, fmt.Errorf("--for must be between %s and %s, got %s",
				minimumOperationsHold, maximumOperationsHold, duration))
		}
		if duration%time.Second != 0 {
			return withExitCode(exitUsage, fmt.Errorf("--for must be a whole number of seconds, got %s", duration))
		}
		reason, reasonError := requiredReasonFlag(cmd)
		if reasonError != nil {
			return reasonError
		}
		staffClient, clusterID, clientError := staffRecoveryClientFor(cmd, args[0])
		if clientError != nil {
			return clientError
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), staffRecoveryTimeout)
		defer cancel()
		hold, setError := staffClient.SetOperationsHold(ctx, clusterID, duration, reason)
		if setError != nil {
			return staffRecoveryError("setting the operations hold", setError)
		}
		if written, renderError := renderStructured(cmd, hold); written || renderError != nil {
			return renderError
		}
		printOperationsHold(cmd.OutOrStdout(), hold)
		return nil
	},
}

var clusterOperationsHoldGetCmd = &cobra.Command{
	Annotations: map[string]string{annotationClusterIDOnly: staffClusterIDOnlyReason},
	Use:         "get <cluster_id>",
	Short:       "Show the cluster's operations hold and any pending version record check",
	Args:        cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		staffClient, clusterID, clientError := staffRecoveryClientFor(cmd, args[0])
		if clientError != nil {
			return clientError
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), staffRecoveryTimeout)
		defer cancel()
		hold, getError := staffClient.GetOperationsHold(ctx, clusterID)
		if getError != nil {
			return staffRecoveryError("reading the operations hold", getError)
		}
		if written, renderError := renderStructured(cmd, hold); written || renderError != nil {
			return renderError
		}
		printOperationsHold(cmd.OutOrStdout(), hold)
		return nil
	},
}

var clusterOperationsHoldLiftCmd = &cobra.Command{
	Annotations: map[string]string{annotationClusterIDOnly: staffClusterIDOnlyReason},
	Use:         "lift <cluster_id> --reason <text>",
	Short:       "Lift the cluster's operations hold so the platform acts on it again",
	Long: `Lift the cluster's operations hold, whatever set it.

When a Kubernetes version record repair was made under the hold, the platform
refuses the lift until its reads have confirmed every node at the version the
record pins ('hold get' shows the check). --force overrides that refusal and
is recorded on the audit row; it asks for confirmation unless --yes is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, reasonError := requiredReasonFlag(cmd)
		if reasonError != nil {
			return reasonError
		}
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")
		staffClient, clusterID, clientError := staffRecoveryClientFor(cmd, args[0])
		if clientError != nil {
			return clientError
		}
		if force {
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), fmt.Sprintf(
				"Lift the hold on %s even if the version record is not confirmed? The platform may act on the nodes at once. [y/N] ",
				clusterID), yes); confirmError != nil {
				return confirmError
			}
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), staffRecoveryTimeout)
		defer cancel()
		lift, liftError := staffClient.LiftOperationsHold(ctx, clusterID, reason, force)
		if liftError != nil {
			return staffRecoveryError("lifting the operations hold", liftError)
		}
		if written, renderError := renderStructured(cmd, lift); written || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		switch {
		case !lift.Lifted:
			_, _ = fmt.Fprintf(out, "No operations hold was in force on cluster %s; nothing changed.\n", lift.ClusterID)
		case lift.Forced:
			_, _ = fmt.Fprintf(out, "Lifted the hold on cluster %s (it ran until %s), overriding an unconfirmed version record repair.\n",
				lift.ClusterID, formatInstant(lift.LiftedBlockedUntil))
		default:
			_, _ = fmt.Fprintf(out, "Lifted the hold on cluster %s (it ran until %s). The platform acts on the cluster again.\n",
				lift.ClusterID, formatInstant(lift.LiftedBlockedUntil))
		}
		return nil
	},
}

var clusterUpgradeRollbackRecordCmd = &cobra.Command{
	Use:   "rollback-record <cluster_id> <version> --reason <text>",
	Short: "Put the Kubernetes version record back after a manual rollback (Ankra staff only)",
	Long: `Repair a cluster's Kubernetes version record after its nodes were rolled back
by hand from a failed version hop.

A failed hop leaves the platform's record pinning the new version, and the
platform then re-runs the hop as soon as it may act again. This writes every
control plane's record back to <version> and up, each worker to <version> (or
to the newer version it runs), and the cluster's Kubernetes version to
<version>. The platform refuses it unless an operations hold is in force with at
least 10 minutes left, no write is running on the cluster, and its last read of
every control plane shows exactly <version> (as the node reports it, e.g.
v1.34.5+k3s1). Then wait for 'ankra cluster operations hold get' to show the
record confirmed before lifting the hold.

This is an Ankra staff operation: set ANKRA_STAFF_SESSION and pass the cluster
by id. It asks for confirmation unless --yes is given.

Examples:
  ankra cluster upgrade rollback-record 62f4559a-a44d-46d7-aab3-a57c9dd6b4c6 v1.34.5+k3s1 --reason "cp-0 rolled back (tier 1)"`,
	Args:        cobra.ExactArgs(2),
	Annotations: map[string]string{annotationRequiresAuth: "false", annotationClusterIDOnly: staffClusterIDOnlyReason},
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, reasonError := requiredReasonFlag(cmd)
		if reasonError != nil {
			return reasonError
		}
		version := strings.TrimSpace(args[1])
		yes, _ := cmd.Flags().GetBool("yes")
		staffClient, clusterID, clientError := staffRecoveryClientFor(cmd, args[0])
		if clientError != nil {
			return clientError
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), fmt.Sprintf(
			"Rewrite the Kubernetes version record of %s to %s? [y/N] ", clusterID, version), yes); confirmError != nil {
			return confirmError
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), staffRecoveryTimeout)
		defer cancel()
		repair, repairError := staffClient.RollbackK8sVersionRecord(ctx, clusterID, version, reason)
		if repairError != nil {
			return staffRecoveryError("repairing the Kubernetes version record", repairError)
		}
		if written, renderError := renderStructured(cmd, repair); written || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Kubernetes version record of cluster %s repaired to %s at %s.\n",
			repair.ClusterID, repair.Version, repair.RepairedAt.UTC().Format(time.RFC3339))
		for _, node := range repair.Nodes {
			name := node.Name
			if name == "" {
				name = node.ResourceID
			}
			freshness := "read before its last write attempt"
			if node.ObservationPostdatesAttempt {
				freshness = "read after its last write attempt"
			}
			_, _ = fmt.Fprintf(out, "  %-14s %-8s %s %s -> %s up (binary %s, %s at %s)\n",
				node.Role, name, node.PreviousState, stringOrDash(node.PreviousVersion), node.Version,
				stringOrDash(node.ObservedVersion), freshness, formatInstant(node.ObservedAt))
		}
		_, _ = fmt.Fprintf(out, "The hold stands until %s. Wait for 'ankra cluster operations hold get %s' to show the record confirmed, then lift it.\n",
			repair.BlockedUntil.UTC().Format(time.RFC3339), repair.ClusterID)
		return nil
	},
}

func init() {
	clusterOperationsHoldSetCmd.Flags().Duration("for", 0, "how long to hold the cluster from now (1m to 24h, e.g. 4h)")
	clusterOperationsHoldSetCmd.Flags().String("reason", "", "why the platform must stop acting on the cluster (audited)")
	_ = clusterOperationsHoldSetCmd.MarkFlagRequired("for")
	clusterOperationsHoldLiftCmd.Flags().String("reason", "", "why the platform may act on the cluster again (audited)")
	clusterOperationsHoldLiftCmd.Flags().Bool("force", false, "lift even though a version record repair is not confirmed yet")
	clusterOperationsHoldLiftCmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt of --force")
	clusterUpgradeRollbackRecordCmd.Flags().String("reason", "", "which rollback the record is brought in line with (audited)")
	clusterUpgradeRollbackRecordCmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	registerStructuredOutputFlags(clusterOperationsHoldSetCmd, clusterOperationsHoldGetCmd,
		clusterOperationsHoldLiftCmd, clusterUpgradeRollbackRecordCmd)

	clusterOperationsHoldCmd.AddCommand(clusterOperationsHoldSetCmd)
	clusterOperationsHoldCmd.AddCommand(clusterOperationsHoldGetCmd)
	clusterOperationsHoldCmd.AddCommand(clusterOperationsHoldLiftCmd)
	clusterOperationsCmd.AddCommand(clusterOperationsHoldCmd)
	clusterUpgradeCmd.AddCommand(clusterUpgradeRollbackRecordCmd)
}
