package cmd

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// retentionKeys are the buckets --retention accepts, in the order the policy
// prints them.
var retentionKeys = []string{"hourly", "daily", "weekly", "monthly", "yearly", "minimum-count", "minimum-age"}

// parseRetention reads --retention daily=7,weekly=4,minimum-age=24h into the
// policy's retention. Underscored spellings are accepted too, because the API
// and the portal both write minimum_count.
func parseRetention(raw string) (*client.BackupRetention, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	retention := client.BackupRetention{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, found := strings.Cut(pair, "=")
		if !found {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"--retention takes key=value pairs, got %q - for example daily=7,weekly=4,monthly=6", pair))
		}
		key = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(key, "_", "-")))
		value = strings.TrimSpace(value)
		if key == "minimum-age" {
			retention.MinimumAge = value
			continue
		}
		count, parseError := strconv.Atoi(value)
		if parseError != nil {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"--retention %s=%s is not a number of restore points to keep", key, value))
		}
		if count < 0 {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"--retention %s=%s is negative; 0 keeps none of that bucket", key, value))
		}
		switch key {
		case "hourly":
			retention.Hourly = count
		case "daily":
			retention.Daily = count
		case "weekly":
			retention.Weekly = count
		case "monthly":
			retention.Monthly = count
		case "yearly":
			retention.Yearly = count
		case "minimum-count":
			retention.MinimumCount = count
		default:
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"--retention does not have a %q bucket - expected one of %s",
				key, strings.Join(retentionKeys, ", ")))
		}
	}
	return &retention, nil
}

// backupPolicyRefusal turns the platform's 422 into every field-level reason
// at once. The platform answers them together precisely so an operator can fix
// a schedule and a retention in one edit.
func backupPolicyRefusal(out io.Writer, policyError error) error {
	var validation *client.BackupPolicyValidationError
	if !errors.As(policyError, &validation) {
		return backupLaneError("protecting stack", policyError)
	}
	detail := validation.Detail
	if detail == "" {
		detail = "The backup policy is not valid."
	}
	_, _ = fmt.Fprintf(out, "%s\n", detail)
	for _, violation := range validation.Violations {
		_, _ = fmt.Fprintf(out, "  - %s: %s\n", violation.Key, violation.Message)
	}
	return withExitCode(exitUsage, errors.New(detail))
}

func printBackupPolicy(out io.Writer, protection *client.StackProtection) {
	policy := protection.Policy
	_, _ = fmt.Fprintf(out, "Stack '%s':\n", protection.StackName)
	enabled := "off"
	if policy.Enabled {
		enabled = "on"
	}
	_, _ = fmt.Fprintf(out, "  Protection:   %s\n", enabled)
	if policy.Vault != "" {
		_, _ = fmt.Fprintf(out, "  Vault:        %s\n", policy.Vault)
	}
	if policy.Schedule != "" {
		_, _ = fmt.Fprintf(out, "  Schedule:     %s\n", policy.Schedule)
	}
	if policy.Enabled {
		_, _ = fmt.Fprintf(out, "  Retention:    %s\n", describeRetention(policy.Retention))
		databases := "excluded"
		if policy.Selection.Databases {
			databases = "included"
		}
		_, _ = fmt.Fprintf(out, "  Databases:    %s\n", databases)
		volumes := "only where named"
		if len(policy.Selection.PersistentVolumeClaims) > 0 {
			volumes = strings.Join(policy.Selection.PersistentVolumeClaims, ", ")
		}
		_, _ = fmt.Fprintf(out, "  Volumes:      %s\n", volumes)
	}
	if protection.BackupStack != "" {
		_, _ = fmt.Fprintf(out, "  Backup stack: %s\n", protection.BackupStack)
	}
	if protection.CommitSHA != nil && *protection.CommitSHA != "" {
		_, _ = fmt.Fprintf(out, "  Commit:       %s\n", *protection.CommitSHA)
	}
	if protection.GitPushMessage != "" {
		_, _ = fmt.Fprintf(out, "  Git:          %s\n", protection.GitPushMessage)
	}
	printWarnings(out, protection.Warnings)
}

// describeRetention renders the retention as the buckets that keep anything,
// so a policy that keeps seven dailies does not print five zeroes to say so.
func describeRetention(retention client.BackupRetention) string {
	parts := make([]string, 0, len(retentionKeys))
	for _, bucket := range []struct {
		label string
		count int
	}{
		{"hourly", retention.Hourly}, {"daily", retention.Daily}, {"weekly", retention.Weekly},
		{"monthly", retention.Monthly}, {"yearly", retention.Yearly},
	} {
		if bucket.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", bucket.count, bucket.label))
		}
	}
	if retention.MinimumCount > 0 {
		parts = append(parts, fmt.Sprintf("never below %d", retention.MinimumCount))
	}
	if retention.MinimumAge != "" {
		parts = append(parts, "never younger than "+retention.MinimumAge)
	}
	if len(parts) == 0 {
		return "the platform default"
	}
	return strings.Join(parts, ", ")
}

var clusterStacksProtectCmd = &cobra.Command{
	Use:   "protect <stack>",
	Short: "Protect a stack: schedule its backups into a vault",
	Long: `Turn on protection for a stack.

Protection is a property of the stack, not a separate object: the command
writes a backup block onto the stack's definition, and the platform converges
on it by installing the backup data plane on the cluster. Until that plane
exists the command reports the backup stack as "installing" rather than
claiming the stack is protected.

--vault is required; a protected stack with nowhere to write to is not
protected. --schedule takes hourly, daily, weekly, or a five-field cron
expression. --retention takes the buckets to keep, as key=value pairs.

Examples:
  ankra cluster stacks protect shop --vault production-backups
  ankra cluster stacks protect shop --vault production-backups --schedule daily --retention daily=7,weekly=4,monthly=6
  ankra cluster stacks protect shop --vault production-backups --include-pvc shop/data --backup-now --wait`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		vaultReference, _ := cmd.Flags().GetString("vault")
		vaultID, vaultError := resolveBackupVaultID(apiClient, vaultReference)
		if vaultError != nil {
			return vaultError
		}
		schedule, _ := cmd.Flags().GetString("schedule")
		retentionRaw, _ := cmd.Flags().GetString("retention")
		retention, retentionError := parseRetention(retentionRaw)
		if retentionError != nil {
			return retentionError
		}
		selection, selectionError := buildSelection(cmd)
		if selectionError != nil {
			return selectionError
		}
		backupNow, _ := cmd.Flags().GetBool("backup-now")

		protection, protectError := apiClient.ProtectStack(cluster.ID, stackName, client.ProtectStackRequest{
			VaultID:   vaultID,
			Schedule:  schedule,
			Retention: retention,
			Selection: selection,
			BackupNow: backupNow,
		})
		if protectError != nil {
			return backupPolicyRefusal(cmd.ErrOrStderr(), protectError)
		}

		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}
		wait, waitFlagError := asyncWriteWaitFlag(cmd)
		if waitFlagError != nil {
			return waitFlagError
		}
		shouldFollow := wait && protection.RunID != nil && *protection.RunID != ""

		if !shouldFollow {
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, protection)
			}
			printBackupPolicy(cmd.OutOrStdout(), protection)
			printProtectFollowUp(cmd.OutOrStdout(), protection, wait)
			return nil
		}

		requestContext, cancel, contextError := asyncWriteRequestContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancel()
		if format == outputDefault {
			printBackupPolicy(cmd.OutOrStdout(), protection)
		}
		run, runError := followRunToCompletion(requestContext, apiClient, *protection.RunID, cmd.ErrOrStderr())
		if runError != nil {
			return asyncWriteError("taking the first restore point", true, runError)
		}
		if outcomeError := runOutcomeError("taking the first restore point", run); outcomeError != nil {
			return outcomeError
		}
		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, protection)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nFirst restore point of '%s' is sealed. Inspect it with "+
			"'ankra cluster stacks restore-points list %s'.\n", stackName, stackName)
		return nil
	},
}

// printProtectFollowUp says what to do next, which depends on whether the
// data plane is up yet and whether a capture was started.
func printProtectFollowUp(out io.Writer, protection *client.StackProtection, wait bool) {
	if protection.BackupStack == client.BackupStackInstalling {
		_, _ = fmt.Fprintf(out, "\nThe backup data plane is still installing on this cluster. "+
			"A capture started before it is ready waits rather than failing.\n")
	}
	if protection.RunID == nil || *protection.RunID == "" {
		_, _ = fmt.Fprintf(out, "\nProtection is on. Take a restore point now with "+
			"'ankra cluster stacks restore-points create %s'.\n", protection.StackName)
		return
	}
	if wait {
		return
	}
	_, _ = fmt.Fprintf(out, "\nFirst restore point is being taken. Watch it with 'ankra runs get %s', "+
		"or re-run with --wait to block until it is sealed.\n", *protection.RunID)
}

var clusterStacksUnprotectCmd = &cobra.Command{
	Use:   "unprotect <stack>",
	Short: "Turn off a stack's scheduled backups",
	Long: `Turn off protection for a stack.

Every restore point already taken is kept and stays restorable: removing
protection is a decision about the future, not about the past. The scheduled
backups and the rendered object stores disappear on the next render.

Confirmation is typing the stack's own name, not y/N; --yes skips it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		if confirmError := confirmStackName(cmd, stackName, yes,
			fmt.Sprintf("Unprotecting stack %q stops its scheduled backups. "+
				"The restore points already taken are kept and stay restorable.", stackName)); confirmError != nil {
			return confirmError
		}
		protection, unprotectError := apiClient.UnprotectStack(cluster.ID, stackName,
			client.UnprotectStackRequest{Confirm: stackName})
		if unprotectError != nil {
			return backupLaneError("unprotecting stack", unprotectError)
		}
		if rendered, renderError := renderStructured(cmd, protection); rendered || renderError != nil {
			return renderError
		}
		printBackupPolicy(cmd.OutOrStdout(), protection)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"\nStack '%s' is no longer protected. Its restore points are still there: "+
				"'ankra cluster stacks restore-points list %s'.\n", stackName, stackName)
		return nil
	},
}

func init() {
	clusterStacksProtectCmd.Flags().String("vault", "",
		"Backup vault the restore points are written to (name or id, required)")
	clusterStacksProtectCmd.Flags().String("schedule", "",
		"hourly, daily, weekly, or a five-field cron expression")
	clusterStacksProtectCmd.Flags().String("retention", "",
		"Restore points to keep, as key=value pairs: "+strings.Join(retentionKeys, ", ")+
			" (for example daily=7,weekly=4,monthly=6)")
	clusterStacksProtectCmd.Flags().Bool("backup-now", false,
		"Take the first restore point immediately instead of waiting for the schedule")
	registerSelectionFlags(clusterStacksProtectCmd)
	registerAsyncWriteFlagsWithTimeout(clusterStacksProtectCmd, backupRunWaitTimeout)
	_ = clusterStacksProtectCmd.MarkFlagRequired("vault")

	clusterStacksUnprotectCmd.Flags().Bool("yes", false, "Skip the typed stack-name confirmation")

	registerStructuredOutputFlags(clusterStacksProtectCmd, clusterStacksUnprotectCmd)

	clusterStacksCmd.AddCommand(clusterStacksProtectCmd)
	clusterStacksCmd.AddCommand(clusterStacksUnprotectCmd)
}
