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

var scalewayCredentialsCmd = &cobra.Command{
	Use:   "scaleway",
	Short: "Manage Scaleway credentials",
	Long: `Create and inspect project-scoped Scaleway credentials.

The same Scaleway credential can provision Scaleway Instances clusters and
Scaleway Kapsule clusters. For self-hosted clusters, prefer a separate
least-privilege runtime credential for CCM/CSI and pass its ID with
--runtime-credential-id.`,
}

func requiredMaskedValue(cmd *cobra.Command, flagName, label string) (string, error) {
	value, _ := cmd.Flags().GetString(flagName)
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	prompt := promptui.Prompt{
		Label: label,
		Mask:  '*',
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("%s cannot be empty", strings.ToLower(label))
			}
			return nil
		},
	}
	value, err := prompt.Run()
	if err != nil {
		return "", errors.New("prompt cancelled")
	}
	return strings.TrimSpace(value), nil
}

type privateKeyOutputFile interface {
	Write([]byte) (int, error)
	Chmod(os.FileMode) error
	Stat() (os.FileInfo, error)
	Sync() error
	Close() error
}

var openPrivateKeyOutput = func(path string, flag int, permission os.FileMode) (privateKeyOutputFile, error) {
	return os.OpenFile(path, flag, permission)
}

// writeGeneratedPrivateKey creates a new regular file without following an
// existing symlink. O_EXCL closes the Lstat/open race; every failure after
// creation removes the partial file so a retry is safe.
func writeGeneratedPrivateKey(path string, privateKey []byte) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("private key output path is empty")
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing private key path %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking private key output path %s: %w", path, err)
	}

	file, err := openPrivateKeyOutput(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing to overwrite existing private key path %s", path)
		}
		return fmt.Errorf("creating private key output %s: %w", path, err)
	}

	cleanup := func(cause error) error {
		closeErr := file.Close()
		removeErr := os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return errors.Join(cause, closeErr, removeErr)
	}

	info, err := file.Stat()
	if err != nil {
		return cleanup(fmt.Errorf("verifying private key output %s: %w", path, err))
	}
	if !info.Mode().IsRegular() {
		return cleanup(fmt.Errorf("private key output %s is not a regular file", path))
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanup(fmt.Errorf("setting private key output permissions on %s: %w", path, err))
	}
	info, err = file.Stat()
	if err != nil {
		return cleanup(fmt.Errorf("verifying private key output permissions on %s: %w", path, err))
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		return cleanup(fmt.Errorf("private key output %s has permissions %#o, expected 0600", path, permission))
	}

	for written := 0; written < len(privateKey); {
		count, writeErr := file.Write(privateKey[written:])
		written += count
		if writeErr != nil {
			return cleanup(fmt.Errorf("writing private key output %s: %w", path, writeErr))
		}
		if count == 0 {
			return cleanup(fmt.Errorf("writing private key output %s: short write", path))
		}
	}
	if err := file.Sync(); err != nil {
		return cleanup(fmt.Errorf("syncing private key output %s: %w", path, err))
	}
	if err := file.Close(); err != nil {
		removeErr := os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return errors.Join(fmt.Errorf("closing private key output %s: %w", path, err), removeErr)
	}

	info, err = os.Lstat(path)
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("verifying completed private key output %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		_ = os.Remove(path)
		return fmt.Errorf("completed private key output %s failed regular-file permission verification", path)
	}
	return nil
}

var scalewayCredentialsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Scaleway API credential",
	Long: `Store a Scaleway API key scoped to the supplied project.

Access and secret keys are masked when prompted and are never printed. Flags
are supported for automation, but environment/secret-store expansion is
recommended so values do not appear in shell history.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		name, _ := cmd.Flags().GetString("name")
		projectID, _ := cmd.Flags().GetString("project-id")
		accessKey, err := requiredMaskedValue(cmd, "access-key", "Scaleway Access Key")
		if err != nil {
			return err
		}
		secretKey, err := requiredMaskedValue(cmd, "secret-key", "Scaleway Secret Key")
		if err != nil {
			return err
		}
		result, err := activeScalewayAPI().CreateScalewayCredential(client.CreateScalewayCredentialRequest{
			Name: name, AccessKey: accessKey, SecretKey: secretKey, ProjectID: projectID,
		})
		if err != nil {
			return fmt.Errorf("creating Scaleway credential: %w", err)
		}
		if !result.Success {
			message := "failed to create Scaleway credential"
			for _, item := range result.Errors {
				message += fmt.Sprintf("\n  - %s: %s", item.Key, item.Message)
			}
			return errors.New(message)
		}
		fmt.Printf("Scaleway credential %q created successfully.\n", name)
		fmt.Println("Use its credential ID for Kapsule or Instances provisioning; use a dedicated runtime credential for CCM/CSI where possible.")
		return nil
	},
}

var scalewayCredentialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Scaleway credentials",
	RunE: func(cmd *cobra.Command, _ []string) error {
		items, err := activeScalewayAPI().ListScalewayCredentials()
		if err != nil {
			return fmt.Errorf("listing Scaleway credentials: %w", err)
		}
		if items == nil {
			items = []client.ScalewayCredentialListItem{}
		}
		if handled, err := renderStructured(cmd, items); handled || err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No Scaleway credentials found.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(os.Stdout)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"ID", "NAME", "AVAILABLE", "STATE", "CREATED"})
		for _, item := range items {
			state := "-"
			if item.State != nil {
				state = *item.State
			}
			writer.AppendRow(table.Row{item.ID, item.Name, item.Available, state, formatTimeAgo(item.CreatedAt)})
		}
		writer.Render()
		return nil
	},
}

func resolveScalewayCredential(value string) (*client.CredentialDetail, error) {
	id, err := resolveCredentialID(value)
	if err != nil {
		return nil, err
	}
	detail, err := apiClient.GetCredential(id)
	if err != nil {
		return nil, err
	}
	if detail.Provider != "scaleway" {
		return nil, fmt.Errorf("credential %q is provider %q, not scaleway", value, detail.Provider)
	}
	return detail, nil
}

var scalewayCredentialsGetCmd = &cobra.Command{
	Use:   "get <credential_id|name>",
	Short: "Get Scaleway credential metadata",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := resolveScalewayCredential(args[0])
		if err != nil {
			return fmt.Errorf("fetching Scaleway credential: %w", err)
		}
		if handled, err := renderStructured(cmd, detail); handled || err != nil {
			return err
		}
		fmt.Printf("ID: %s\nName: %s\nProvider: %s\n", detail.ID, detail.Name, detail.Provider)
		fmt.Println("Secret material is intentionally not returned by the platform.")
		return nil
	},
}

var scalewayCredentialsValidateCmd = &cobra.Command{
	Use:   "validate <name>",
	Short: "Validate a Scaleway credential name",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		result, err := apiClient.ValidateCredentialName(args[0])
		if err != nil {
			return fmt.Errorf("validating credential name: %w", err)
		}
		if !result.Valid {
			message := "unavailable"
			if result.Message != nil {
				message = *result.Message
			}
			return fmt.Errorf("credential name %q is invalid: %s", args[0], message)
		}
		fmt.Printf("Credential name %q is valid and available.\n", args[0])
		return nil
	},
}

var scalewayCredentialsDeleteCmd = &cobra.Command{
	Use:   "delete <credential_id|name>",
	Short: "Delete a Scaleway credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := resolveScalewayCredential(args[0])
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete Scaleway credential %q (%s)? Referencing Instances and Kapsule clusters will stop reconciling. [y/N]: ", detail.Name, detail.ID), yes); err != nil {
			return err
		}
		orgID := apiClient.OrganisationOverride()
		if orgID == "" {
			selected, selectedErr := loadSelectedOrganisation()
			if selectedErr == nil {
				orgID = selected.OrganisationID
			}
		}
		if orgID == "" {
			return errors.New("no organisation selected: run `ankra org switch <org_id>` first")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		result, err := apiClient.DeleteCredential(ctx, detail.ID, orgID)
		if err != nil {
			return fmt.Errorf("deleting Scaleway credential: %w", err)
		}
		if !result.Success {
			return errors.New("delete request did not report success")
		}
		fmt.Println("Scaleway credential deleted.")
		return nil
	},
}

var scalewayCredentialSSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage shared SSH-key credentials for Scaleway Instances",
	Long:    "SSH-key credentials are provider-neutral and can be attached to Scaleway Instances clusters.",
}

var scalewayCredentialSSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH-key credentials",
	RunE: func(cmd *cobra.Command, _ []string) error {
		items, err := apiClient.ListSSHKeyCredentials()
		if err != nil {
			return fmt.Errorf("listing SSH-key credentials: %w", err)
		}
		if handled, err := renderStructured(cmd, items); handled || err != nil {
			return err
		}
		for _, item := range items {
			fmt.Printf("%s  %s\n", item.ID, item.Name)
		}
		return nil
	},
}

var scalewayCredentialSSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a shared SSH-key credential",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		name, _ := cmd.Flags().GetString("name")
		publicKey, _ := cmd.Flags().GetString("public-key")
		generate, _ := cmd.Flags().GetBool("generate")
		privateKeyOutput, _ := cmd.Flags().GetString("private-key-output")
		if !generate && strings.TrimSpace(publicKey) == "" {
			return errors.New("either --public-key or --generate must be provided")
		}
		if generate && strings.TrimSpace(privateKeyOutput) == "" {
			return errors.New("--private-key-output is required with --generate so the private key is never printed")
		}
		request := client.CreateSSHKeyCredentialRequest{Name: name, Generate: generate}
		if publicKey != "" {
			request.SSHPublicKey = &publicKey
		}
		result, err := apiClient.CreateSSHKeyCredential(request)
		if err != nil {
			return fmt.Errorf("creating SSH-key credential: %w", err)
		}
		if !result.Success {
			return errors.New("failed to create SSH-key credential")
		}
		fmt.Printf("SSH-key credential %q created.\n", name)
		if result.PrivateKey != nil {
			if err := writeGeneratedPrivateKey(privateKeyOutput, []byte(*result.PrivateKey)); err != nil {
				return fmt.Errorf("writing generated private key to %s: %w", privateKeyOutput, err)
			}
			fmt.Printf("Generated private key saved with mode 0600 to %s (not printed).\n", privateKeyOutput)
		}
		return nil
	},
}

func init() {
	scalewayCredentialsCreateCmd.Flags().String("name", "", "Credential name (required)")
	scalewayCredentialsCreateCmd.Flags().String("access-key", "", "Scaleway access key (sensitive; prompted when omitted)")
	scalewayCredentialsCreateCmd.Flags().String("secret-key", "", "Scaleway secret key (sensitive; prompted when omitted)")
	scalewayCredentialsCreateCmd.Flags().String("project-id", "", "Scaleway project ID (required)")
	_ = scalewayCredentialsCreateCmd.MarkFlagRequired("name")
	_ = scalewayCredentialsCreateCmd.MarkFlagRequired("project-id")

	scalewayCredentialsDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation")
	scalewayCredentialSSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	scalewayCredentialSSHKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	scalewayCredentialSSHKeyCreateCmd.Flags().Bool("generate", false, "Generate a new key pair")
	scalewayCredentialSSHKeyCreateCmd.Flags().String("private-key-output", "", "Secure file for generated private key (required with --generate)")
	_ = scalewayCredentialSSHKeyCreateCmd.MarkFlagRequired("name")

	registerStructuredOutputFlags(scalewayCredentialsListCmd, scalewayCredentialsGetCmd, scalewayCredentialSSHKeyListCmd)
	scalewayCredentialSSHKeyCmd.AddCommand(scalewayCredentialSSHKeyListCmd, scalewayCredentialSSHKeyCreateCmd)
	scalewayCredentialsCmd.AddCommand(
		scalewayCredentialsCreateCmd,
		scalewayCredentialsListCmd,
		scalewayCredentialsGetCmd,
		scalewayCredentialsValidateCmd,
		scalewayCredentialsDeleteCmd,
		scalewayCredentialSSHKeyCmd,
	)
	credentialsCmd.AddCommand(scalewayCredentialsCmd)
}
