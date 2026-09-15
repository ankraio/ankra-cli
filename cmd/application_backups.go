package cmd

// Backups on an application (epic ankra-0xsdd, bead ankra-0xsdd.44).
//
// The stack verbs under `ankra cluster stacks` already protect and capture a
// stack. These exist because using them for an application means knowing the
// name of the stack it deploys as - which the person who deployed the
// application never chose and has no reason to know. Here the deployment is
// named by its CLUSTER and the platform resolves the stack, so
//
//	ankra application protect shop --cluster production
//
// replaces looking up `deploy-shop` first. Everything else is the same lane:
// the restore point these take is the same artifact the stack's own listing
// shows, and `ankra cluster stacks restore-points` still reads it.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// applicationDeploymentTarget is the deployment a command acts on, resolved
// from the application's own Backups read rather than from the cluster list:
// it is the same answer the section shows, so a refusal can name the clusters
// this application actually runs on.
type applicationDeploymentTarget struct {
	deployment client.ApplicationBackupDeployment
	backups    *client.ApplicationBackups
}

// resolveApplicationDeployment matches --cluster against the application's
// deployments by id or by name.
func resolveApplicationDeployment(applicationID string, clusterReference string) (applicationDeploymentTarget, error) {
	backups, readError := apiClient.GetApplicationBackups(applicationID)
	if readError != nil {
		return applicationDeploymentTarget{}, backupLaneError("reading the application's backups", readError)
	}
	// An id match is exact and unique, so it wins outright. A NAME match is
	// collected rather than taken: two clusters whose names differ only in
	// case would otherwise send the capture to whichever the listing
	// happened to return first, which is a coin toss over where somebody's
	// data is written.
	matched := []client.ApplicationBackupDeployment{}
	for _, deployment := range backups.Deployments {
		if deployment.ClusterID == clusterReference {
			return applicationDeploymentTarget{deployment: deployment, backups: backups}, nil
		}
		if strings.EqualFold(deployment.ClusterName, clusterReference) {
			matched = append(matched, deployment)
		}
	}
	if len(matched) == 1 {
		return applicationDeploymentTarget{deployment: matched[0], backups: backups}, nil
	}
	if len(matched) > 1 {
		identifiers := make([]string, 0, len(matched))
		for _, deployment := range matched {
			identifiers = append(identifiers, deployment.ClusterID)
		}
		return applicationDeploymentTarget{}, withExitCode(exitUsage, fmt.Errorf(
			"%d of this application's deployments are on a cluster named %q - pass the cluster id instead (%s)",
			len(matched), clusterReference, strings.Join(identifiers, ", ")))
	}
	names := make([]string, 0, len(backups.Deployments))
	for _, deployment := range backups.Deployments {
		names = append(names, deployment.ClusterName)
	}
	if len(names) == 0 {
		return applicationDeploymentTarget{}, withExitCode(exitUsage, fmt.Errorf(
			"application %q is not deployed to any cluster yet", backups.ApplicationName))
	}
	return applicationDeploymentTarget{}, withExitCode(exitUsage, fmt.Errorf(
		"application %q is not deployed on cluster %q; it runs on: %s",
		backups.ApplicationName, clusterReference, strings.Join(names, ", ")))
}

// requireDeployedStack refuses a write against a deployment whose first
// deploy has not created its stack. It is not the same answer as "not found":
// the deployment is recorded and there is simply nothing to protect yet.
func (target applicationDeploymentTarget) requireDeployedStack() error {
	if target.deployment.StackName != nil && *target.deployment.StackName != "" {
		return nil
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"deployment of %q on %s has not created its stack yet; deploy it first",
		target.backups.ApplicationName, target.deployment.ClusterName))
}

func newApplicationBackupsCommand() *cobra.Command {
	backupsCommand := &cobra.Command{
		Use:   "backups <application-id>",
		Short: "Show an application's backup protection, deployment by deployment",
		Long: `Show an application's backup protection, deployment by deployment.

One row per cluster the application is deployed to: whether that deployment
carries a database, whether its data is protected, when it was last backed up
and when it is next scheduled.

The verdict has three values and none of them is a boolean: 'unknown' means
Ankra could not establish whether this deployment is protected, which is not
the same answer as 'unprotected'.`,
		Example: `  ankra application backups shop
  ankra application backups shop -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			backups, readError := apiClient.GetApplicationBackups(applicationID)
			if readError != nil {
				return backupLaneError("reading the application's backups", readError)
			}
			if rendered, renderError := renderStructured(command, backups); rendered || renderError != nil {
				return renderError
			}
			printApplicationBackups(command.OutOrStdout(), backups)
			return nil
		},
	}
	registerStructuredOutputFlags(backupsCommand)
	return backupsCommand
}

func newApplicationProtectCommand() *cobra.Command {
	protectCommand := &cobra.Command{
		Use:   "protect <application-id>",
		Short: "Turn scheduled backups on for one of an application's deployments",
		Long: `Turn scheduled backups on for one of an application's deployments.

The deployment is named by the cluster it runs on; Ankra resolves which stack
that is. Without --vault the organisation's single verified backup vault is
used, and an organisation with more than one is refused rather than chosen
for - where a restore point lives is not a decision to make on somebody's
behalf.`,
		Example: `  ankra application protect shop --cluster production
  ankra application protect shop --cluster production --vault production-backups --backup-now`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			clusterReference := strings.TrimSpace(mustFlagString(command, "cluster"))
			if clusterReference == "" {
				return withExitCode(exitUsage, errors.New(
					"--cluster is required: it names the deployment to protect"))
			}
			target, targetError := resolveApplicationDeployment(applicationID, clusterReference)
			if targetError != nil {
				return targetError
			}
			if stackError := target.requireDeployedStack(); stackError != nil {
				return stackError
			}
			vaultID, vaultError := resolveSelectedVault(command)
			if vaultError != nil {
				return vaultError
			}
			backupNow, _ := command.Flags().GetBool("backup-now")
			protection, protectError := apiClient.ProtectApplicationDeployment(applicationID,
				target.deployment.DeploymentID, client.ProtectApplicationDeploymentRequest{
					VaultID:   vaultID,
					Schedule:  strings.TrimSpace(mustFlagString(command, "schedule")),
					BackupNow: backupNow,
				})
			if protectError != nil {
				return backupLaneError("protecting the deployment", protectError)
			}
			if rendered, renderError := renderStructured(command, protection); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Deployment on %s:\n", target.deployment.ClusterName)
			printBackupPolicy(command.OutOrStdout(), protection)
			return nil
		},
	}
	protectCommand.Flags().String("cluster", "", "Cluster the deployment runs on (name or id, required)")
	protectCommand.Flags().String("vault", "",
		"Backup vault the restore points are written to (name or id; the organisation's only ready vault when omitted, refused when it has several)")
	protectCommand.Flags().String("schedule", "",
		"hourly, daily, weekly, or a five-field cron expression (daily when omitted)")
	protectCommand.Flags().Bool("backup-now", false,
		"Take the first restore point immediately instead of waiting for the schedule")
	registerStructuredOutputFlags(protectCommand)
	return protectCommand
}

func newApplicationBackupCommand() *cobra.Command {
	backupCommand := &cobra.Command{
		Use:   "backup <application-id>",
		Short: "Back up one of an application's deployments now",
		Long: `Back up one of an application's deployments now.

The capture is dispatched by the platform, so the command answers with the
restore point in 'creating' and the run that will seal it. With --wait it
follows that run to completion.

The deployment is named by the cluster it runs on; Ankra resolves which stack
that is, and the restore point it takes is the same artifact
'ankra cluster stacks restore-points list' shows for that stack.`,
		Example: `  ankra application backup shop --cluster production
  ankra application backup shop --cluster production --note "before the 3.2 migration" --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			clusterReference := strings.TrimSpace(mustFlagString(command, "cluster"))
			if clusterReference == "" {
				return withExitCode(exitUsage, errors.New(
					"--cluster is required: it names the deployment to back up"))
			}
			target, targetError := resolveApplicationDeployment(applicationID, clusterReference)
			if targetError != nil {
				return targetError
			}
			if stackError := target.requireDeployedStack(); stackError != nil {
				return stackError
			}
			vaultID, vaultError := resolveSelectedVault(command)
			if vaultError != nil {
				return vaultError
			}
			result, createError := apiClient.CreateApplicationRestorePoint(applicationID,
				target.deployment.DeploymentID, client.CreateRestorePointRequest{
					VaultID: vaultID,
					Note:    mustFlagString(command, "note"),
				})
			if createError != nil {
				return backupLaneError("backing up the deployment", createError)
			}
			wait, waitFlagError := asyncWriteWaitFlag(command)
			if waitFlagError != nil {
				return waitFlagError
			}
			if !wait {
				if format != outputDefault {
					return encodeStructured(command.OutOrStdout(), format, result)
				}
				_, _ = fmt.Fprintf(command.OutOrStdout(),
					"Restore point %s is being taken of %s on %s.\n",
					result.RestorePointID, target.backups.ApplicationName, target.deployment.ClusterName)
				printWarnings(command.OutOrStdout(), result.Warnings)
				_, _ = fmt.Fprintf(command.OutOrStdout(),
					"\nWatch it with 'ankra runs get %s', or re-run with --wait to block until it is sealed.\n",
					result.RunID)
				return nil
			}
			requestContext, cancel, contextError := asyncWriteRequestContext(command)
			if contextError != nil {
				return contextError
			}
			defer cancel()
			printWarnings(command.ErrOrStderr(), result.Warnings)
			run, waitError := followRunToCompletion(requestContext, apiClient, result.RunID, command.ErrOrStderr())
			if waitError != nil {
				return asyncWriteError("backing up the deployment", true, waitError)
			}
			if outcomeError := runOutcomeError("backing up the deployment", run); outcomeError != nil {
				return outcomeError
			}
			sealed, getError := apiClient.GetStackRestorePoint(target.deployment.ClusterID,
				*target.deployment.StackName, result.RestorePointID)
			if getError != nil {
				return backupLaneError("reading the sealed restore point", getError)
			}
			if format != outputDefault {
				return encodeStructured(command.OutOrStdout(), format, sealed)
			}
			printRestorePointDetail(command.OutOrStdout(), sealed)
			return nil
		},
	}
	backupCommand.Flags().String("cluster", "", "Cluster the deployment runs on (name or id, required)")
	backupCommand.Flags().String("vault", "",
		"Backup vault to write to (name or id; the deployment's own backup policy, then the organisation's only ready vault, when omitted)")
	backupCommand.Flags().String("note", "", "Why this backup was taken, recorded on the audit row and the run")
	registerAsyncWriteFlagsWithTimeout(backupCommand, backupRunWaitTimeout)
	registerStructuredOutputFlags(backupCommand)
	return backupCommand
}

// printApplicationBackups renders the section as the default output.
//
// It never prints a bare "unprotected" for a deployment whose posture Ankra
// could not read: an unread verdict prints as unknown with its reason, so the
// operator is never told that something is unprotected on the strength of a
// read that did not happen.
func printApplicationBackups(out io.Writer, backups *client.ApplicationBackups) {
	_, _ = fmt.Fprintf(out, "Application '%s':\n", backups.ApplicationName)
	_, _ = fmt.Fprintf(out, "  Database:        %s\n", describeApplicationDatabaseStatus(backups.Database))
	databaseBackup := "off"
	if backups.DatabaseBackup {
		databaseBackup = "on"
	}
	_, _ = fmt.Fprintf(out, "  Database backup: %s\n", databaseBackup)
	if backups.ReadyVaultKnown {
		_, _ = fmt.Fprintf(out, "  Ready vaults:    %d\n", backups.ReadyVaultCount)
	} else {
		_, _ = fmt.Fprintln(out, "  Ready vaults:    unknown (the vault listing could not be read)")
	}
	if len(backups.Deployments) == 0 {
		_, _ = fmt.Fprintln(out, "\nThis application is not deployed to any cluster yet.")
		printWarnings(out, backups.Warnings)
		return
	}
	_, _ = fmt.Fprintln(out, "\nDeployments:")
	for _, deployment := range backups.Deployments {
		_, _ = fmt.Fprintf(out, "  %s\n", deployment.ClusterName)
		stackName := "not created yet"
		if deployment.StackName != nil && *deployment.StackName != "" {
			stackName = *deployment.StackName
		}
		_, _ = fmt.Fprintf(out, "    Stack:       %s\n", stackName)
		_, _ = fmt.Fprintf(out, "    Database:    %s\n", describeDeploymentDatabase(deployment))
		_, _ = fmt.Fprintf(out, "    Protection:  %s\n", describeDeploymentProtection(deployment))
		if deployment.Posture != nil {
			if deployment.Posture.VaultName != "" {
				_, _ = fmt.Fprintf(out, "    Vault:       %s\n", deployment.Posture.VaultName)
			}
			if deployment.Posture.Schedule != "" {
				_, _ = fmt.Fprintf(out, "    Schedule:    %s\n", deployment.Posture.Schedule)
			}
			_, _ = fmt.Fprintf(out, "    Last backup: %s\n",
				optionalTimestampText(deployment.Posture.LastRestorePointAt, "none yet"))
			_, _ = fmt.Fprintf(out, "    Next backup: %s\n",
				optionalTimestampText(deployment.Posture.NextScheduledAt, "not scheduled"))
		}
	}
	printWarnings(out, backups.Warnings)
}

// describeApplicationDatabaseStatus keeps the contract's three values apart.
func describeApplicationDatabaseStatus(database client.ApplicationDatabase) string {
	switch database.Status {
	case "recorded":
		engine := database.Engine
		if engine == "" {
			engine = "declared"
		}
		return engine
	case "absent":
		return "none declared"
	default:
		if database.Reason != "" {
			return "unknown (" + database.Reason + ")"
		}
		return "unknown"
	}
}

// describeDeploymentDatabase answers the same question for one deployment,
// keeping "no database" apart from "Ankra could not tell".
func describeDeploymentDatabase(deployment client.ApplicationBackupDeployment) string {
	if !deployment.HasDatabaseKnown {
		return "unknown"
	}
	if deployment.HasDatabase {
		return "yes"
	}
	return "no"
}

// describeDeploymentProtection renders the posture's three-valued verdict,
// with the reason when there is one. A deployment with no posture at all is
// "unknown", never "unprotected".
func describeDeploymentProtection(deployment client.ApplicationBackupDeployment) string {
	if deployment.Posture == nil {
		return "unknown (this deployment has no stack to read a posture from yet)"
	}
	switch deployment.Posture.ProtectionState {
	case "protected":
		if deployment.Posture.FailingRuns24h > 0 {
			return fmt.Sprintf("protected, but %d backup(s) failed in the last 24 hours",
				deployment.Posture.FailingRuns24h)
		}
		return "protected"
	case "unprotected":
		if deployment.Posture.UnprotectedReason != "" {
			return "unprotected (" + deployment.Posture.UnprotectedReason + ")"
		}
		return "unprotected"
	default:
		if deployment.Posture.UnknownReason != "" {
			return "unknown (" + deployment.Posture.UnknownReason + ")"
		}
		return "unknown"
	}
}

func optionalTimestampText(timestamp *string, absent string) string {
	if timestamp == nil || *timestamp == "" {
		return absent
	}
	return *timestamp
}
