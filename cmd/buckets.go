package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// bucketCmd is the parent of the object storage bucket verbs: buckets Ankra
// creates on the organisation's own provider credentials, for anything that
// is not a backup vault - a registry, a custody bucket, application storage.
var bucketCmd = &cobra.Command{
	Use:     "bucket",
	Aliases: []string{"buckets"},
	Short:   "Manage object storage buckets Ankra creates on your provider credentials",
	Long: "Create, list, inspect and delete object storage buckets on the organisation's " +
		"Hetzner, Scaleway, DigitalOcean or UpCloud credentials. Ankra creates the bucket, " +
		"verifies it and keeps its access keys; the keys are never printed.",
}

// bucketPollInterval paces the --wait poll of a provisioning bucket.
var bucketPollInterval = 5 * time.Second

// bucketProvisionWaitTimeout is the default --timeout for create --wait. It
// matches the backup vault lane it shares its provider code with: UpCloud's
// Managed Object Storage service alone may take 15 minutes to come up.
const bucketProvisionWaitTimeout = 25 * time.Minute

// resolveBucketID accepts a bucket id or a bucket name. `bucket list` prints
// a NAME column, so a name is the obvious thing to pass to the next command.
func resolveBucketID(buckets APIClient, reference string) (string, error) {
	if backupVaultIDPattern.MatchString(reference) {
		return reference, nil
	}
	listing, listError := buckets.ListObjectStorageBuckets()
	if listError != nil {
		return "", fmt.Errorf("looking up bucket %s: %w", reference, listError)
	}
	matched := []client.ObjectStorageBucket{}
	for _, bucket := range listing.Items {
		if bucket.Name == reference {
			matched = append(matched, bucket)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0].ID, nil
	case 0:
		return "", fmt.Errorf("no bucket named %q - run 'ankra bucket list' to see the organisation's buckets", reference)
	default:
		identifiers := make([]string, 0, len(matched))
		for _, bucket := range matched {
			identifiers = append(identifiers, bucket.ID)
		}
		return "", fmt.Errorf("%d buckets are named %q - pass the id instead (%s)",
			len(matched), reference, strings.Join(identifiers, ", "))
	}
}

// bucketRecoveryHint says what to do with a bucket in error. Nothing retries a
// failed provisioning run, and deleting with the teardown clears whatever the
// run had already created, so that is the one way out that always works.
func bucketRecoveryHint(bucket *client.ObjectStorageBucket) string {
	return fmt.Sprintf("read the error above, then run 'ankra bucket delete %s --destroy-provider-resources' "+
		"and create it again", bucket.Name)
}

func printBucket(bucket *client.ObjectStorageBucket) {
	fmt.Println("Bucket:")
	fmt.Printf("  Name:        %s\n", bucket.Name)
	fmt.Printf("  ID:          %s\n", bucket.ID)
	fmt.Printf("  Provider:    %s\n", bucket.Provider)
	fmt.Printf("  Region:      %s\n", bucket.Region)
	fmt.Printf("  Bucket:      %s\n", bucket.Bucket)
	endpoint := bucket.Endpoint
	if endpoint == "" {
		endpoint = "- (known once provisioning finishes)"
	}
	fmt.Printf("  Endpoint:    %s\n", endpoint)
	pathStyle := "no"
	if bucket.PathStyle {
		pathStyle = "yes"
	}
	fmt.Printf("  Path style:  %s\n", pathStyle)
	fmt.Printf("  Credential:  %s\n", bucket.CredentialID)
	fmt.Printf("  Status:      %s\n", bucket.Status)
	fmt.Printf("  Created:     %s\n", formatTimeAgo(bucket.CreatedAt))
	if bucket.Status == "error" {
		fmt.Println("\nError:")
		if bucket.ErrorExcerpt != nil && *bucket.ErrorExcerpt != "" {
			fmt.Printf("  %s\n", *bucket.ErrorExcerpt)
		} else {
			fmt.Println("  (no error detail recorded)")
		}
		fmt.Printf("\nTo recover: %s\n", bucketRecoveryHint(bucket))
	}
}

var bucketListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's object storage buckets",
	RunE: func(cmd *cobra.Command, args []string) error {
		listing, listError := apiClient.ListObjectStorageBuckets()
		if listError != nil {
			return fmt.Errorf("listing buckets: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, listing); rendered || renderError != nil {
			return renderError
		}
		if len(listing.Items) == 0 {
			fmt.Println("No buckets found. Create one with 'ankra bucket create <name> --credential <credential>'.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(os.Stdout)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Name", "Provider", "Region", "Bucket", "Status", "Created"})
		for _, bucket := range listing.Items {
			writer.AppendRow(table.Row{
				bucket.Name, bucket.Provider, bucket.Region, bucket.Bucket, bucket.Status, formatTimeAgo(bucket.CreatedAt),
			})
		}
		writer.Render()
		return nil
	},
}

var bucketGetCmd = &cobra.Command{
	Use:   "get [bucket-name|bucket-id]",
	Short: "Show a bucket's details and status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucketID, resolveError := resolveBucketID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		bucket, getError := apiClient.GetObjectStorageBucket(bucketID)
		if getError != nil {
			return fmt.Errorf("getting bucket: %w", getError)
		}
		if rendered, renderError := renderStructured(cmd, bucket); rendered || renderError != nil {
			return renderError
		}
		printBucket(bucket)
		return nil
	},
}

// selectBucketCredential resolves --credential, or picks the only credential
// Ankra can create a bucket with when the flag was omitted. It refuses to
// guess between several.
func selectBucketCredential(reference string) (client.Credential, error) {
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
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return client.Credential{}, errors.New(
			"this organisation has no Hetzner, Scaleway, DigitalOcean or UpCloud credential to create a bucket with; " +
				"add one with 'ankra credentials'")
	}
	described := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		described = append(described, fmt.Sprintf("%s (%s)", candidate.Name, candidate.Provider))
	}
	return client.Credential{}, fmt.Errorf(
		"this organisation has %d credentials Ankra can create buckets with, so pass --credential: %s",
		len(candidates), strings.Join(described, ", "))
}

// waitForBucketProvisioning polls the bucket until it leaves "provisioning"
// or the context expires.
func waitForBucketProvisioning(ctx context.Context, buckets APIClient, bucketID string) (*client.ObjectStorageBucket, error) {
	for {
		bucket, getError := buckets.GetObjectStorageBucket(bucketID)
		if getError != nil {
			return nil, getError
		}
		if bucket.Status != "provisioning" {
			return bucket, nil
		}
		timer := time.NewTimer(bucketPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return bucket, ctx.Err()
		case <-timer.C:
		}
	}
}

var bucketCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an object storage bucket on a provider credential",
	Long: `Create an object storage bucket on one of the organisation's provider
credentials (Hetzner, Scaleway, DigitalOcean or UpCloud). Ankra creates the
bucket, verifies it and keeps its access keys; the bucket shows "provisioning"
until that finishes.

The credential defaults to the only one Ankra can create buckets with, the
region to that provider's usual one (fsn1, fr-par, fra1, europe-1) and the
provider-side bucket name to a unique one derived from <name>.

Hetzner's Cloud API cannot mint Object Storage keys. Store the key pair on the
Hetzner credential once and every bucket after that needs nothing more, or
pass --access-key-id here: the secret key is then prompted for, hidden, so it
stays out of your shell history.

Examples:
  ankra bucket create registry-storage --credential hetzner-main --region fsn1 --wait
  ankra bucket create custody --credential upcloud-main --region europe-1 --bucket acme-custody`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialReference, _ := cmd.Flags().GetString("credential")
		region, _ := cmd.Flags().GetString("region")
		bucketName, _ := cmd.Flags().GetString("bucket")
		accessKeyID, _ := cmd.Flags().GetString("access-key-id")
		secretAccessKey, _ := cmd.Flags().GetString("secret-access-key")

		credential, resolveError := selectBucketCredential(credentialReference)
		if resolveError != nil {
			return resolveError
		}
		if !provisionableBackupVaultProviders[credential.Provider] {
			return fmt.Errorf("credential %q is a %s credential; Ankra can create buckets with hetzner, scaleway, "+
				"digitalocean and upcloud credentials", credential.Name, credential.Provider)
		}
		if region == "" {
			region = backupVaultDefaultRegions[credential.Provider]
		}
		if credential.Provider != "hetzner" && (accessKeyID != "" || secretAccessKey != "") {
			return fmt.Errorf("--access-key-id/--secret-access-key are only used for hetzner credentials; "+
				"%s keys are minted from the credential", credential.Provider)
		}
		if credential.Provider == "hetzner" && accessKeyID != "" && secretAccessKey == "" {
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
		if credential.Provider == "hetzner" && accessKeyID == "" && secretAccessKey != "" {
			return errors.New("--secret-access-key needs --access-key-id: pass both halves of the pair, or neither " +
				"to use the pair stored on the Hetzner credential")
		}

		name := strings.TrimSpace(args[0])
		fmt.Printf("Creating bucket '%s' with credential '%s' (%s) in %s.\n", name, credential.Name, credential.Provider, region)
		bucket, createError := apiClient.CreateObjectStorageBucket(client.CreateObjectStorageBucketRequest{
			Name: name, CredentialID: credential.ID, Region: region, Bucket: bucketName,
			AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey,
		})
		if createError != nil {
			return fmt.Errorf("creating bucket: %w", createError)
		}

		wait, waitFlagError := asyncWriteWaitFlag(cmd)
		if waitFlagError != nil {
			return waitFlagError
		}
		if !wait {
			printBucket(bucket)
			fmt.Printf("\nBucket '%s' is being created on %s. Check on it with 'ankra bucket get %s', "+
				"or re-run with --wait to block until it is ready.\n", bucket.Name, bucket.Provider, bucket.Name)
			return nil
		}
		ctx, cancel, contextError := asyncWriteRequestContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancel()
		fmt.Printf("Creating bucket '%s' on %s...\n", bucket.Name, bucket.Provider)
		final, waitError := waitForBucketProvisioning(ctx, apiClient, bucket.ID)
		if waitError != nil {
			// Giving up waiting is not the run failing; say so, or the user
			// deletes a bucket that is still on its way up.
			if errors.Is(waitError, context.DeadlineExceeded) {
				fmt.Printf("\nStopped waiting. Creating the bucket is still running on %s - it has not failed.\n"+
					"Check on it with 'ankra bucket get %s'.\n", bucket.Provider, bucket.Name)
			}
			return asyncWriteError("creating bucket", true, waitError)
		}
		printBucket(final)
		if final.Status == "error" {
			message := fmt.Sprintf("bucket %q could not be created", final.Name)
			if final.ErrorExcerpt != nil && *final.ErrorExcerpt != "" {
				message += ": " + *final.ErrorExcerpt
			}
			return errors.New(message)
		}
		fmt.Printf("\nBucket '%s' is ready.\n", final.Name)
		return nil
	},
}

var bucketDeleteCmd = &cobra.Command{
	Use:   "delete [bucket-name|bucket-id]",
	Short: "Stop managing a bucket, optionally destroying it at the provider",
	Long: `Stop managing an object storage bucket in Ankra.

By default this removes only Ankra's record of the bucket and the access keys
it kept: the bucket and every object in it stay in your cloud account.

--destroy-provider-resources also empties and deletes the bucket and removes
the UpCloud object storage service or DigitalOcean Spaces key Ankra created
for it. Everything in the bucket is gone for good. A bucket that is still
being created is always torn down, because the half-finished run may already
have made something billable.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		destroyProviderResources, _ := cmd.Flags().GetBool("destroy-provider-resources")
		bucketID, resolveError := resolveBucketID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		prompt := fmt.Sprintf("Stop managing bucket %q? The bucket and its objects stay on the provider. [y/N]: ", args[0])
		if destroyProviderResources {
			// Name the provider-side bucket that is about to be emptied: the
			// Ankra name alone does not tell an operator what data is at stake.
			bucket, getError := apiClient.GetObjectStorageBucket(bucketID)
			if getError != nil {
				return fmt.Errorf("reading bucket: %w", getError)
			}
			prompt = fmt.Sprintf("Delete bucket %q AND destroy bucket %q on %s, including every object in it? [y/N]: ",
				args[0], bucket.Bucket, bucket.Provider)
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), prompt, yes); confirmError != nil {
			return confirmError
		}
		if deleteError := apiClient.DeleteObjectStorageBucket(bucketID, destroyProviderResources); deleteError != nil {
			return fmt.Errorf("deleting bucket: %w", deleteError)
		}
		if destroyProviderResources {
			fmt.Printf("Bucket '%s' deleted; it is being emptied and destroyed at the provider.\n", args[0])
			return nil
		}
		fmt.Printf("Bucket '%s' is no longer managed by Ankra. The bucket and its objects are untouched.\n", args[0])
		return nil
	},
}

func init() {
	bucketCreateCmd.Flags().String("credential", "",
		"Provider credential (name or id) the bucket is created with (default: the only one Ankra can use)")
	bucketCreateCmd.Flags().String("region", "",
		"Provider region (default: that provider's usual one - fsn1, fr-par, fra1, europe-1)")
	bucketCreateCmd.Flags().String("bucket", "", "Provider-side bucket name (default: a unique name derived from <name>)")
	bucketCreateCmd.Flags().String("access-key-id", "",
		"Hetzner Object Storage access key (Hetzner only; omit to use the pair stored on the credential)")
	bucketCreateCmd.Flags().String("secret-access-key", "",
		"Hetzner Object Storage secret key (Hetzner only; prompted for hidden when --access-key-id is given)")
	registerAsyncWriteFlagsWithTimeout(bucketCreateCmd, bucketProvisionWaitTimeout)

	bucketDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	bucketDeleteCmd.Flags().Bool("destroy-provider-resources", false,
		"Also empty and delete the bucket at the provider (everything in it is lost) and remove what Ankra created for it")

	registerStructuredOutputFlags(bucketListCmd, bucketGetCmd)

	bucketCmd.AddCommand(bucketListCmd)
	bucketCmd.AddCommand(bucketGetCmd)
	bucketCmd.AddCommand(bucketCreateCmd)
	bucketCmd.AddCommand(bucketDeleteCmd)
	rootCmd.AddCommand(bucketCmd)
}
