package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// backupCmd is the parent command for backup operations.
var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup operations",
	Long:  "Commands for managing the organisation's backup infrastructure.",
}

var backupVaultsCmd = &cobra.Command{
	Use:     "vaults",
	Aliases: []string{"vault"},
	Short:   "Manage backup vaults",
	Long: "Manage the organisation's backup vaults: S3-compatible object-storage " +
		"targets that cluster backups are written to. The platform verifies each " +
		"vault's credentials against its bucket and reports the outcome as the " +
		"vault's status.",
}

// backupsNotEnabledDetail is the verbatim 403 detail the platform answers on
// every backup vault route while the organisation's `backups` feature is
// dark. Matching the text (rather than the bare status) keeps a permission
// refusal - also a 403 - on its own path.
const backupsNotEnabledDetail = "Backups are not enabled for this organisation."

// backupLaneError wraps a backup vault API error for the terminal. The dark
// lane is the one case with something better to say than the raw detail:
// the fix is organisational (enable the feature, or select the right
// organisation), not a permission or a typo, so the message says so.
func backupLaneError(operation string, apiError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(apiError, &unexpected) && unexpected.StatusCode == http.StatusForbidden &&
		strings.TrimSpace(unexpected.Error()) == backupsNotEnabledDetail {
		return fmt.Errorf("%s: %s Backup vaults are rolling out gradually - ask Ankra to enable the "+
			"`backups` feature for this organisation, or check which organisation is selected "+
			"with `ankra org current`", operation, backupsNotEnabledDetail)
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

// backupVaultIDPattern matches the canonical UUID form the API expects for a
// vault id. Anything else is treated as a vault name to resolve.
var backupVaultIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveBackupVaultID accepts either a vault id or a vault name. `backup
// vaults list` prints a NAME column, so a name is the obvious thing to pass
// to the next command; resolving it here spares the user a raw server-side
// uuid-parsing error.
func resolveBackupVaultID(vaults APIClient, reference string) (string, error) {
	if backupVaultIDPattern.MatchString(reference) {
		return reference, nil
	}
	listing, listError := vaults.ListBackupVaults()
	if listError != nil {
		// The lookup is a convenience, not the operation. If listing is
		// unavailable, hand the reference to the API unchanged and let the
		// real call decide.
		return reference, nil
	}
	matched := []client.BackupVault{}
	for _, vault := range listing.Items {
		if vault.Name == reference {
			matched = append(matched, vault)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0].ID, nil
	case 0:
		return "", fmt.Errorf(
			"no backup vault named %q - run 'ankra backup vaults list' to see the available vaults", reference)
	default:
		identifiers := make([]string, 0, len(matched))
		for _, vault := range matched {
			identifiers = append(identifiers, vault.ID)
		}
		return "", fmt.Errorf("%d backup vaults are named %q - pass the id instead (%s)",
			len(matched), reference, strings.Join(identifiers, ", "))
	}
}

// backupVaultVerifyHint tells the user what to do after a failed credential
// check: the stored keys are replaced by recreating them server-side, so the
// actionable next step is fixing the keys at the provider and re-verifying.
func backupVaultVerifyHint(vaultName string) string {
	return fmt.Sprintf("fix the access keys, then run 'ankra backup vaults verify %s'", vaultName)
}

// backupVaultStatusError turns a vault whose verification failed into a
// non-zero exit carrying the failure excerpt, so scripts see the broken
// vault instead of a green create.
func backupVaultStatusError(vault *client.BackupVault) error {
	message := fmt.Sprintf("backup vault %q failed verification", vault.Name)
	if vault.ErrorExcerpt != nil && *vault.ErrorExcerpt != "" {
		message += ": " + *vault.ErrorExcerpt
	}
	return fmt.Errorf("%s - %s", message, backupVaultVerifyHint(vault.Name))
}

func printBackupVault(vault *client.BackupVault) {
	fmt.Println("Backup Vault:")
	fmt.Printf("  Name:          %s\n", vault.Name)
	fmt.Printf("  ID:            %s\n", vault.ID)
	fmt.Printf("  Provider:      %s\n", vault.Provider)
	fmt.Printf("  Endpoint:      %s\n", vault.Endpoint)
	region := vault.Region
	if region == "" {
		region = "-"
	}
	fmt.Printf("  Region:        %s\n", region)
	fmt.Printf("  Bucket:        %s\n", vault.Bucket)
	pathStyle := "no"
	if vault.PathStyle {
		pathStyle = "yes"
	}
	fmt.Printf("  Path style:    %s\n", pathStyle)
	fmt.Printf("  Status:        %s\n", vault.Status)
	lastVerified := "-"
	if vault.LastVerifiedAt != nil && *vault.LastVerifiedAt != "" {
		lastVerified = formatTimeAgo(*vault.LastVerifiedAt)
	}
	fmt.Printf("  Last verified: %s\n", lastVerified)
	fmt.Printf("  Created:       %s\n", formatTimeAgo(vault.CreatedAt))
	if vault.Status == "error" {
		fmt.Println("\nLast verification error:")
		if vault.ErrorExcerpt != nil && *vault.ErrorExcerpt != "" {
			fmt.Printf("  %s\n", *vault.ErrorExcerpt)
		} else {
			fmt.Println("  (no error detail recorded)")
		}
		fmt.Printf("\nTo recover: %s\n", backupVaultVerifyHint(vault.Name))
	}
}

var backupVaultsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's backup vaults",
	RunE: func(cmd *cobra.Command, args []string) error {
		listing, listError := apiClient.ListBackupVaults()
		if listError != nil {
			return backupLaneError("listing backup vaults", listError)
		}
		if rendered, renderError := renderStructured(cmd, listing); rendered || renderError != nil {
			return renderError
		}
		if len(listing.Items) == 0 {
			fmt.Println("No backup vaults found. Create one with 'ankra backup vaults create <name> --endpoint <url> --bucket <bucket>'.")
			return nil
		}

		writer := table.NewWriter()
		writer.SetOutputMirror(os.Stdout)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Name", "Provider", "Bucket", "Endpoint", "Status", "Last Verified"})
		for _, vault := range listing.Items {
			lastVerified := "-"
			if vault.LastVerifiedAt != nil && *vault.LastVerifiedAt != "" {
				lastVerified = formatTimeAgo(*vault.LastVerifiedAt)
			}
			writer.AppendRow(table.Row{
				vault.Name,
				vault.Provider,
				vault.Bucket,
				vault.Endpoint,
				vault.Status,
				lastVerified,
			})
		}
		writer.Render()
		return nil
	},
}

var backupVaultsGetCmd = &cobra.Command{
	Use:   "get [vault-name|vault-id]",
	Short: "Show a backup vault's details and verification status",
	Long: "Describe a backup vault: its endpoint, bucket, verification status, " +
		"and - when the last credential check failed - the failure excerpt.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultID, resolveError := resolveBackupVaultID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		vault, getError := apiClient.GetBackupVault(vaultID)
		if getError != nil {
			return backupLaneError("getting backup vault", getError)
		}
		if rendered, renderError := renderStructured(cmd, vault); rendered || renderError != nil {
			return renderError
		}
		printBackupVault(vault)
		return nil
	},
}

var backupVaultsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a backup vault backed by an S3-compatible bucket",
	Long: `Create a backup vault: an S3-compatible bucket cluster backups are written to.

The access keys are prompted for interactively when not passed as flags, so
they never have to appear in your shell history. The platform verifies the
keys against the bucket immediately and the command reports the outcome.

Example:
  ankra backup vaults create offsite --endpoint https://s3.example.com --bucket cluster-backups`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		endpoint, _ := cmd.Flags().GetString("endpoint")
		bucket, _ := cmd.Flags().GetString("bucket")
		provider, _ := cmd.Flags().GetString("provider")
		region, _ := cmd.Flags().GetString("region")
		pathStyle, _ := cmd.Flags().GetBool("path-style")
		accessKeyID, _ := cmd.Flags().GetString("access-key-id")
		secretAccessKey, _ := cmd.Flags().GetString("secret-access-key")

		if accessKeyID == "" {
			prompt := promptui.Prompt{
				Label: "Access key ID",
				Validate: func(input string) error {
					if len(input) == 0 {
						return fmt.Errorf("access key ID cannot be empty")
					}
					return nil
				},
			}
			promptedValue, promptError := prompt.Run()
			if promptError != nil {
				return errors.New("prompt cancelled")
			}
			accessKeyID = promptedValue
		}
		if secretAccessKey == "" {
			prompt := promptui.Prompt{
				Label: "Secret access key",
				Mask:  '*',
				Validate: func(input string) error {
					if len(input) == 0 {
						return fmt.Errorf("secret access key cannot be empty")
					}
					return nil
				},
			}
			promptedValue, promptError := prompt.Run()
			if promptError != nil {
				return errors.New("prompt cancelled")
			}
			secretAccessKey = promptedValue
		}

		vault, createError := apiClient.CreateBackupVault(client.CreateBackupVaultRequest{
			Name:            name,
			Provider:        provider,
			Endpoint:        endpoint,
			Region:          region,
			Bucket:          bucket,
			PathStyle:       pathStyle,
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
		})
		if createError != nil {
			return backupLaneError("creating backup vault", createError)
		}

		printBackupVault(vault)
		if vault.Status == "error" {
			return backupVaultStatusError(vault)
		}
		fmt.Printf("\nBackup vault '%s' created (status: %s).\n", vault.Name, vault.Status)
		return nil
	},
}

var backupVaultsVerifyCmd = &cobra.Command{
	Use:   "verify [vault-name|vault-id]",
	Short: "Re-check a backup vault's credentials against its bucket",
	Long: "Re-run the platform's credential check against the vault's bucket and " +
		"report the new status. Use this after rotating or fixing the access keys.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultID, resolveError := resolveBackupVaultID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		vault, verifyError := apiClient.VerifyBackupVault(vaultID)
		if verifyError != nil {
			return backupLaneError("verifying backup vault", verifyError)
		}
		if vault.Status == "error" {
			return backupVaultStatusError(vault)
		}
		fmt.Printf("Backup vault '%s' verified (status: %s).\n", vault.Name, vault.Status)
		return nil
	},
}

var backupVaultsDeleteCmd = &cobra.Command{
	Use:   "delete [vault-name|vault-id]",
	Short: "Delete a backup vault",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		vaultID, resolveError := resolveBackupVaultID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete backup vault %q? [y/N]: ", args[0]), yes); confirmError != nil {
			return confirmError
		}
		if deleteError := apiClient.DeleteBackupVault(vaultID); deleteError != nil {
			return backupLaneError("deleting backup vault", deleteError)
		}
		fmt.Printf("Backup vault '%s' deleted.\n", args[0])
		return nil
	},
}

func init() {
	backupVaultsCreateCmd.Flags().String("endpoint", "", "S3-compatible endpoint URL (required)")
	backupVaultsCreateCmd.Flags().String("bucket", "", "Bucket the backups are written to (required)")
	backupVaultsCreateCmd.Flags().String("provider", "other", "Object-storage provider")
	backupVaultsCreateCmd.Flags().String("region", "", "Bucket region (leave empty when the endpoint implies it)")
	backupVaultsCreateCmd.Flags().Bool("path-style", true, "Address the bucket path-style (https://endpoint/bucket) instead of virtual-hosted-style")
	backupVaultsCreateCmd.Flags().String("access-key-id", "", "Access key ID (prompted for when omitted)")
	backupVaultsCreateCmd.Flags().String("secret-access-key", "", "Secret access key (prompted for hidden when omitted; prefer the prompt so the key stays out of your shell history)")
	_ = backupVaultsCreateCmd.MarkFlagRequired("endpoint")
	_ = backupVaultsCreateCmd.MarkFlagRequired("bucket")

	backupVaultsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(backupVaultsListCmd, backupVaultsGetCmd)

	backupVaultsCmd.AddCommand(backupVaultsListCmd)
	backupVaultsCmd.AddCommand(backupVaultsGetCmd)
	backupVaultsCmd.AddCommand(backupVaultsCreateCmd)
	backupVaultsCmd.AddCommand(backupVaultsVerifyCmd)
	backupVaultsCmd.AddCommand(backupVaultsDeleteCmd)

	backupCmd.AddCommand(backupVaultsCmd)
	rootCmd.AddCommand(backupCmd)
}
