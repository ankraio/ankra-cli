package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

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
	if vault.ProvisionedViaCredentialID != nil {
		fmt.Printf("  Provisioned:   by Ankra via credential %s\n", *vault.ProvisionedViaCredentialID)
	}
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

// backupVaultPollInterval paces the --wait poll of a provisioning vault.
var backupVaultPollInterval = 5 * time.Second

// provisionableBackupVaultProviders mirrors the platform's set; the CLI
// uses it only to decide whether to prompt for Hetzner's Object Storage
// keys, the platform decides everything else.
var provisionableBackupVaultProviders = map[string]bool{
	"hetzner": true, "upcloud": true, "digitalocean": true, "scaleway": true,
}

// backupVaultDefaultRegions is the region each provider gets when none was
// given - the same defaults the dashboard opens with. A provider absent
// here has no safe default and asks for --region.
var backupVaultDefaultRegions = map[string]string{
	"hetzner":      "fsn1",
	"upcloud":      "europe-1",
	"digitalocean": "fra1",
	"scaleway":     "fr-par",
}

// defaultBackupVaultName is the name a vault gets when none was given:
// "backups", then "backups-2" and so on, so a second vault still needs no
// argument and never collides with the platform's unique-name rule.
func defaultBackupVaultName(existing []client.BackupVault) string {
	taken := make(map[string]bool, len(existing))
	for _, vault := range existing {
		taken[vault.Name] = true
	}
	if !taken["backups"] {
		return "backups"
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("backups-%d", suffix)
		if !taken[candidate] {
			return candidate
		}
	}
}

// selectProvisionCredential resolves --credential, or picks the only
// credential Ankra can provision from when the flag was omitted. It refuses
// to guess between several: the dashboard shows which one it preselected
// before you press the button, and a command cannot.
func selectProvisionCredential(reference string) (client.Credential, error) {
	if strings.TrimSpace(reference) != "" {
		return resolveProvisionCredential(reference)
	}
	credentials, listError := apiClient.ListCredentials(nil)
	if listError != nil {
		return client.Credential{}, fmt.Errorf("listing credentials: %w", listError)
	}
	candidates := make([]client.Credential, 0, len(credentials))
	for _, credential := range credentials {
		if provisionableBackupVaultProviders[credential.Provider] {
			candidates = append(candidates, credential)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) == 0 {
		return client.Credential{}, errors.New(
			"this organisation has no Hetzner, UpCloud, DigitalOcean or Scaleway credential to provision a bucket from; " +
				"add one under credentials, or register a bucket you own with 'ankra backup vaults create'")
	}
	described := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		described = append(described, fmt.Sprintf("%s (%s)", candidate.Name, candidate.Provider))
	}
	return client.Credential{}, fmt.Errorf(
		"this organisation has %d credentials Ankra can provision from, so pass --credential: %s",
		len(candidates), strings.Join(described, ", "))
}

// resolveProvisionCredential finds one of the organisation's provider
// credentials by name or id, so the command can tell Hetzner apart before
// it asks for keys.
func resolveProvisionCredential(reference string) (client.Credential, error) {
	credentials, listError := apiClient.ListCredentials(nil)
	if listError != nil {
		return client.Credential{}, fmt.Errorf("listing credentials: %w", listError)
	}
	var matches []client.Credential
	for _, credential := range credentials {
		if credential.ID == reference || credential.Name == reference {
			matches = append(matches, credential)
		}
	}
	switch len(matches) {
	case 0:
		return client.Credential{}, fmt.Errorf("credential %q not found; run 'ankra credentials list' to see the organisation's credentials", reference)
	case 1:
		return matches[0], nil
	}
	return client.Credential{}, fmt.Errorf("multiple credentials are named %q; pass the credential ID instead", reference)
}

// waitForBackupVaultProvisioning polls the vault until it leaves
// "provisioning" or the context expires.
func waitForBackupVaultProvisioning(ctx context.Context, vaults APIClient, vaultID string) (*client.BackupVault, error) {
	for {
		vault, getError := vaults.GetBackupVault(vaultID)
		if getError != nil {
			return nil, getError
		}
		if vault.Status != "provisioning" {
			return vault, nil
		}
		timer := time.NewTimer(backupVaultPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return vault, ctx.Err()
		case <-timer.C:
		}
	}
}

var backupVaultsProvisionCmd = &cobra.Command{
	Use:   "provision [name]",
	Short: "Create a backup vault by letting Ankra provision the bucket from a provider credential",
	Long: `Create a backup vault and let Ankra create the bucket for it, using one of
the organisation's provider credentials (Hetzner, UpCloud, DigitalOcean or
Scaleway). Ankra creates the bucket, mints or stores the access keys,
verifies the bucket and registers the vault; the vault shows "provisioning"
until that finishes.

Everything is decided for you unless you say otherwise: the name defaults to
"backups" (then "backups-2" and so on), the credential to the only one Ankra
can provision from, and the region to that provider's usual one. The command
prints what it chose before it creates anything.

Hetzner alone needs its Object Storage key pair passed in (or prompted for):
Hetzner issues those in the Cloud Console (Object Storage > Manage
credentials) and its Cloud API cannot mint them. The other providers need
nothing beyond the credential.

Examples:
  ankra backup vaults provision
  ankra backup vaults provision offsite --credential upcloud-main --region europe-1 --wait
  ankra backup vaults provision offsite --credential hetzner-main --region fsn1`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialReference, _ := cmd.Flags().GetString("credential")
		region, _ := cmd.Flags().GetString("region")
		bucket, _ := cmd.Flags().GetString("bucket")
		accessKeyID, _ := cmd.Flags().GetString("access-key-id")
		secretAccessKey, _ := cmd.Flags().GetString("secret-access-key")

		credential, resolveError := selectProvisionCredential(credentialReference)
		if resolveError != nil {
			return resolveError
		}
		if !provisionableBackupVaultProviders[credential.Provider] {
			return fmt.Errorf("credential %q is a %s credential; Ankra can provision buckets from hetzner, upcloud, "+
				"digitalocean and scaleway credentials - use 'ankra backup vaults create' to register a bucket you own",
				credentialReference, credential.Provider)
		}
		name := ""
		if len(args) == 1 {
			name = strings.TrimSpace(args[0])
		}
		if name == "" {
			existing, listError := apiClient.ListBackupVaults()
			if listError != nil {
				return backupLaneError("listing backup vaults", listError)
			}
			name = defaultBackupVaultName(existing.Items)
		}
		if region == "" {
			region = backupVaultDefaultRegions[credential.Provider]
			if region == "" {
				return fmt.Errorf("pass --region: %s has no default region", credential.Provider)
			}
		}
		fmt.Printf("Creating backup vault '%s' with credential '%s' (%s) in %s.\n",
			name, credential.Name, credential.Provider, region)

		if credential.Provider == "hetzner" {
			if accessKeyID == "" {
				prompt := promptui.Prompt{
					Label: "Hetzner Object Storage access key",
					Validate: func(input string) error {
						if len(input) == 0 {
							return fmt.Errorf("access key cannot be empty")
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
					Label: "Hetzner Object Storage secret key",
					Mask:  '*',
					Validate: func(input string) error {
						if len(input) == 0 {
							return fmt.Errorf("secret key cannot be empty")
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
		} else if accessKeyID != "" || secretAccessKey != "" {
			return fmt.Errorf("--access-key-id/--secret-access-key are only used for hetzner credentials; %s keys are minted from the credential",
				credential.Provider)
		}

		request := client.ProvisionBackupVaultRequest{
			Name: name, CredentialID: credential.ID, Region: region, Bucket: bucket,
		}
		if credential.Provider == "hetzner" {
			request.AccessKeyID, request.SecretAccessKey = accessKeyID, secretAccessKey
		}
		vault, provisionError := apiClient.ProvisionBackupVault(request)
		if provisionError != nil {
			return backupLaneError("provisioning backup vault", provisionError)
		}

		wait, waitFlagError := asyncWriteWaitFlag(cmd)
		if waitFlagError != nil {
			return waitFlagError
		}
		if !wait {
			printBackupVault(vault)
			fmt.Printf("\nBackup vault '%s' is being provisioned on %s. Check on it with "+
				"'ankra backup vaults get %s', or re-run with --wait to block until it is ready.\n",
				vault.Name, vault.Provider, vault.Name)
			return nil
		}
		ctx, cancel, contextError := asyncWriteRequestContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancel()
		fmt.Printf("Provisioning backup vault '%s' on %s...\n", vault.Name, vault.Provider)
		final, waitError := waitForBackupVaultProvisioning(ctx, apiClient, vault.ID)
		if waitError != nil {
			return asyncWriteError("provisioning backup vault", true, waitError)
		}
		printBackupVault(final)
		if final.Status == "error" {
			return backupVaultStatusError(final)
		}
		fmt.Printf("\nBackup vault '%s' is ready.\n", final.Name)
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
	Long: `Delete a backup vault.

By default this removes only Ankra's record of the vault and the access keys
it stored: the bucket, everything in it, and any provider resource Ankra
created for it are left in your cloud account.

--destroy-provider-resources also destroys what Ankra created for an
Ankra-provisioned vault - it empties and deletes the bucket, and removes the
UpCloud object storage service or DigitalOcean Spaces key that was minted
for it. Restore points in that bucket are gone for good. It is refused for a
vault that registers a bucket you created yourself.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		destroyProviderResources, _ := cmd.Flags().GetBool("destroy-provider-resources")
		vaultID, resolveError := resolveBackupVaultID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		prompt := fmt.Sprintf("Delete backup vault %q? [y/N]: ", args[0])
		if destroyProviderResources {
			// Name the bucket that is about to be emptied: the vault name
			// alone does not tell the operator what data is at stake.
			vault, getError := apiClient.GetBackupVault(vaultID)
			if getError != nil {
				return backupLaneError("reading backup vault", getError)
			}
			if vault.Kind != "ankra_provisioned" {
				return fmt.Errorf("backup vault %q registers a bucket you created, so Ankra will not destroy it; "+
					"delete it without --destroy-provider-resources and remove the bucket yourself", args[0])
			}
			prompt = fmt.Sprintf("Delete backup vault %q AND destroy bucket %q on %s, including every restore point in it? [y/N]: ",
				args[0], vault.Bucket, vault.Provider)
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), prompt, yes); confirmError != nil {
			return confirmError
		}
		if deleteError := apiClient.DeleteBackupVault(vaultID, destroyProviderResources); deleteError != nil {
			return backupLaneError("deleting backup vault", deleteError)
		}
		if destroyProviderResources {
			fmt.Printf("Backup vault '%s' deleted; its provider resources are being destroyed.\n", args[0])
			return nil
		}
		fmt.Printf("Backup vault '%s' deleted. Its bucket and any provider resources Ankra created are untouched.\n", args[0])
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

	backupVaultsProvisionCmd.Flags().String("credential", "",
		"Provider credential (name or id) Ankra creates the bucket with (default: the only one it can provision from)")
	backupVaultsProvisionCmd.Flags().String("region", "",
		"Provider region for the bucket (default: that provider's usual one - fsn1, europe-1, fra1, fr-par)")
	backupVaultsProvisionCmd.Flags().String("bucket", "", "Bucket name (default: a unique name derived from the vault name)")
	backupVaultsProvisionCmd.Flags().String("access-key-id", "", "Hetzner Object Storage access key (Hetzner only; prompted for when omitted)")
	backupVaultsProvisionCmd.Flags().String("secret-access-key", "", "Hetzner Object Storage secret key (Hetzner only; prompted for hidden when omitted)")
	registerAsyncWriteFlags(backupVaultsProvisionCmd)

	backupVaultsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	backupVaultsDeleteCmd.Flags().Bool("destroy-provider-resources", false,
		"Also destroy what Ankra created for this vault: empty and delete the bucket (every restore point in it is lost) "+
			"and remove the provider resource minted for it. Ankra-provisioned vaults only")

	registerStructuredOutputFlags(backupVaultsListCmd, backupVaultsGetCmd)

	backupVaultsCmd.AddCommand(backupVaultsListCmd)
	backupVaultsCmd.AddCommand(backupVaultsGetCmd)
	backupVaultsCmd.AddCommand(backupVaultsCreateCmd)
	backupVaultsCmd.AddCommand(backupVaultsProvisionCmd)
	backupVaultsCmd.AddCommand(backupVaultsVerifyCmd)
	backupVaultsCmd.AddCommand(backupVaultsDeleteCmd)

	backupCmd.AddCommand(backupVaultsCmd)
	rootCmd.AddCommand(backupCmd)
}
