package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/chzyer/readline"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// The AWS credential is the platform's existing aws credential - an access
// key pair or an assumable role - shared by cost reporting, EKS and the
// self-managed EC2 clusters. Connecting one is three steps the platform
// serves on /api/v1/credentials/aws: `onboarding` hands out the external id
// and the CloudFormation launch-stack URL for a role, `create-role`
// registers the role that stack created, and `create-keys` registers an
// access key pair instead. SSH key credentials are provider-agnostic:
// create one with any provider's ssh-key group (for example `ankra
// credentials hetzner ssh-key create`) and pass its id to `ankra cluster aws
// create --ssh-key-credential-id`.
var awsCredCmd = &cobra.Command{
	Use:   "aws",
	Short: "Manage AWS credentials",
	Long: `Connect and list the AWS credentials (access key pairs and assumable roles)
the organisation uses for cost reporting, EKS and self-managed EC2 clusters.

Connecting a role:
  1. 'onboarding --scope self_managed' prints the external id the platform
     generated and the CloudFormation launch-stack URL that creates a role
     trusting the platform with that id. Open the URL, launch the stack, and
     copy the role ARN from its outputs.
  2. 'create-role --name <n> --role-arn <arn> --external-id <id> --scope
     self_managed' registers the role. The scope must be the one the stack
     was launched for; a cost-scoped role cannot build clusters.

Connecting an access key pair instead: 'create-keys --name <n>
--access-key-id <id>' prompts for the secret access key (masked; pipe it on
stdin in scripts) - it is never taken on the command line.

Once connected, the credential's id is what 'ankra cluster aws create
--credential-id' takes; 'list' shows the ids.

SSH key credentials are shared across providers: create one with any
provider's ssh-key group (for example 'ankra credentials hetzner ssh-key
create --name ops-key --generate') and pass its id as
--ssh-key-credential-id.`,
}

var awsCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List AWS credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListAwsCredentials()
		if listError != nil {
			return fmt.Errorf("listing AWS credentials: %w", listError)
		}
		if credentials == nil {
			credentials = []client.Credential{}
		}
		if handled, renderError := renderStructured(cmd, credentials); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(credentials) == 0 {
			fmt.Println("No AWS credentials found.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Auth", "Scope", "State", "Available", "Created"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 6},
			{Number: 4, WidthMin: 12},
			{Number: 5, WidthMin: 10},
			{Number: 6, WidthMin: 10},
			{Number: 7, WidthMin: 15},
		})
		for _, credential := range credentials {
			available := "yes"
			if !credential.Available {
				available = "no"
			}
			state := ""
			if credential.State != nil {
				state = *credential.State
			}
			t.AppendRow(table.Row{
				credential.ID,
				credential.Name,
				optionalCell(credential.AuthMethod),
				optionalCell(credential.Scope),
				state,
				available,
				formatTimeAgo(credential.CreatedAt),
			})
		}
		t.Render()
		return nil
	},
}

// validateAwsCredentialScope refuses a scope the platform does not know
// before the request is sent, naming the accepted set. An empty scope is
// fine: the server defaults it to cost.
func validateAwsCredentialScope(scope string) error {
	if scope == "" {
		return nil
	}
	for _, known := range client.AwsCredentialScopes {
		if scope == known {
			return nil
		}
	}
	return withExitCode(exitUsage, fmt.Errorf("invalid --scope %q: must be one of %s", scope, strings.Join(client.AwsCredentialScopes, ", ")))
}

var awsCredOnboardingCmd = &cobra.Command{
	Use:   "onboarding",
	Short: "Get the external id and launch-stack URL for connecting an AWS role",
	Long: `Print what connecting an AWS account through an assumable role needs: the
external id the platform generated for the role's trust policy, the
principal the role has to trust, and the CloudFormation launch-stack URL
that creates the role with exactly that trust and the permissions the scope
needs. Each call generates a fresh external id; the one you launch the
stack with is the one 'create-role --external-id' has to be given.

Scopes: 'cost' (read-only billing access, the default), 'provisioning'
(EKS) and 'self_managed' (EC2 clusters built by 'ankra cluster aws').

The launch-stack URL and trust principal are null when the platform is not
configured for the scope ("configured: no"); the role then has to be
created by hand.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scope, _ := cmd.Flags().GetString("scope")
		if scopeError := validateAwsCredentialScope(scope); scopeError != nil {
			return scopeError
		}
		result, getError := apiClient.GetAwsOnboarding(scope)
		if getError != nil {
			return fmt.Errorf("reading AWS onboarding: %w", getError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		configured := "yes"
		if !result.Configured {
			configured = "no (the platform has no template or trust principal for this scope; create the role by hand)"
		}
		fmt.Printf("Scope: %s\n", result.Scope)
		fmt.Printf("Configured: %s\n", configured)
		fmt.Printf("External ID: %s\n", result.ExternalID)
		fmt.Printf("Region: %s\n", result.Region)
		fmt.Printf("Trust principal: %s\n", nullableString(result.TrustPrincipalARN))
		fmt.Printf("Template URL: %s\n", nullableString(result.TemplateURL))
		fmt.Printf("Launch stack URL: %s\n", nullableString(result.LaunchStackURL))
		if result.LaunchStackURL != nil && *result.LaunchStackURL != "" {
			fmt.Printf("\nOpen the launch-stack URL, create the stack, then register the role it created:\n"+
				"  ankra credentials aws create-role --name <name> --role-arn <arn> --external-id %s --scope %s\n",
				result.ExternalID, result.Scope)
		}
		return nil
	},
}

func nullableString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

var awsCredCreateRoleCmd = &cobra.Command{
	Use:   "create-role",
	Short: "Register an assumable IAM role as an AWS credential",
	Long: `Register an IAM role the platform can assume. The role has to trust the
platform's principal with the external id 'onboarding' handed out, which
the launch stack sets up; pass that same external id here. The scope must
match the stack the role was created by: a 'cost' role cannot build
clusters, 'ankra cluster aws create' needs 'provisioning' or 'self_managed'.

Example:
  ankra credentials aws create-role --name aws-prod \
    --role-arn arn:aws:iam::123456789012:role/AnkraSelfManaged \
    --external-id <id from onboarding> --region eu-north-1 --scope self_managed`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		roleARN, _ := cmd.Flags().GetString("role-arn")
		externalID, _ := cmd.Flags().GetString("external-id")
		region, _ := cmd.Flags().GetString("region")
		scope, _ := cmd.Flags().GetString("scope")
		if scopeError := validateAwsCredentialScope(scope); scopeError != nil {
			return scopeError
		}

		result, createError := apiClient.CreateAwsRoleCredential(client.AwsRoleCredentialCreateRequest{
			Name:       name,
			RoleARN:    roleARN,
			ExternalID: externalID,
			Region:     region,
			Scope:      scope,
		})
		if createError != nil {
			return fmt.Errorf("creating AWS role credential: %w", createError)
		}
		return renderAwsCredentialCreated(cmd, result, "role")
	},
}

var awsCredCreateKeysCmd = &cobra.Command{
	Use:   "create-keys",
	Short: "Register an access key pair as an AWS credential",
	Long: `Register an IAM access key pair. The access key id is a flag; the secret
access key is collected via a masked prompt (or read from stdin when piped)
and never taken on the command line, so it stays out of shell history.

Example:
  ankra credentials aws create-keys --name aws-prod --access-key-id AKIA... --region eu-north-1
  printf '%s' "$AWS_SECRET_ACCESS_KEY" | ankra credentials aws create-keys --name aws-prod --access-key-id AKIA...`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		accessKeyID, _ := cmd.Flags().GetString("access-key-id")
		region, _ := cmd.Flags().GetString("region")

		secretAccessKey, secretError := readAwsSecretAccessKey(cmd)
		if secretError != nil {
			return secretError
		}

		result, createError := apiClient.CreateAwsKeysCredential(client.AwsKeysCredentialCreateRequest{
			Name:            name,
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			Region:          region,
		})
		if createError != nil {
			return fmt.Errorf("creating AWS keys credential: %w", createError)
		}
		return renderAwsCredentialCreated(cmd, result, "keys")
	},
}

// readAwsSecretAccessKey collects the secret access key: a masked prompt on
// an interactive terminal, a single stdin line otherwise. The value is
// never echoed or accepted as a flag.
func readAwsSecretAccessKey(cmd *cobra.Command) (string, error) {
	const label = "AWS Secret Access Key"
	in := cmd.InOrStdin()
	if file, ok := in.(*os.File); ok && readline.IsTerminal(int(file.Fd())) {
		prompt := promptui.Prompt{
			Label: label,
			Mask:  '*',
			Stdin: file,
			Validate: func(input string) error {
				if strings.TrimSpace(input) == "" {
					return fmt.Errorf("secret access key cannot be empty")
				}
				return nil
			},
		}
		value, promptError := prompt.Run()
		if promptError != nil {
			if isPromptCancellation(promptError) {
				return "", errCancelled
			}
			return "", errors.New("prompt cancelled")
		}
		return strings.TrimSpace(value), nil
	}
	line, readError := bufio.NewReader(in).ReadString('\n')
	if readError != nil && !errors.Is(readError, io.EOF) {
		return "", fmt.Errorf("reading %s from stdin: %w", label, readError)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("%s is required: pipe it on stdin, or run interactively to be prompted", label))
	}
	return value, nil
}

func renderAwsCredentialCreated(cmd *cobra.Command, result *client.AwsCredentialCreateResponse, kind string) error {
	if handled, renderError := renderStructured(cmd, result); renderError != nil {
		return renderError
	} else if handled {
		return nil
	}
	fmt.Printf("AWS %s credential '%s' created successfully!\n", kind, result.Name)
	fmt.Printf("  Credential ID: %s\n", result.ID)
	if !result.Available {
		fmt.Println("  Available: no (the platform could not validate it yet)")
	}
	return nil
}

func init() {
	awsCredOnboardingCmd.Flags().String("scope", "", "Credential scope: cost, provisioning or self_managed (server default: cost)")

	awsCredCreateRoleCmd.Flags().String("name", "", "Credential name (required)")
	awsCredCreateRoleCmd.Flags().String("role-arn", "", "ARN of the IAM role the platform assumes (required)")
	awsCredCreateRoleCmd.Flags().String("external-id", "", "External id the role's trust policy requires - the one 'onboarding' printed (required)")
	awsCredCreateRoleCmd.Flags().String("region", "", "Default AWS region for the credential (server default: us-east-1)")
	awsCredCreateRoleCmd.Flags().String("scope", "", "Credential scope the role was onboarded under: cost, provisioning or self_managed (server default: cost)")
	_ = awsCredCreateRoleCmd.MarkFlagRequired("name")
	_ = awsCredCreateRoleCmd.MarkFlagRequired("role-arn")
	_ = awsCredCreateRoleCmd.MarkFlagRequired("external-id")

	awsCredCreateKeysCmd.Flags().String("name", "", "Credential name (required)")
	awsCredCreateKeysCmd.Flags().String("access-key-id", "", "IAM access key id (required); the secret is prompted for")
	awsCredCreateKeysCmd.Flags().String("region", "", "Default AWS region for the credential (server default: us-east-1)")
	_ = awsCredCreateKeysCmd.MarkFlagRequired("name")
	_ = awsCredCreateKeysCmd.MarkFlagRequired("access-key-id")

	registerStructuredOutputFlags(awsCredListCmd, awsCredOnboardingCmd, awsCredCreateRoleCmd, awsCredCreateKeysCmd)
	awsCredCmd.AddCommand(awsCredListCmd)
	awsCredCmd.AddCommand(awsCredOnboardingCmd)
	awsCredCmd.AddCommand(awsCredCreateRoleCmd)
	awsCredCmd.AddCommand(awsCredCreateKeysCmd)
	credentialsCmd.AddCommand(awsCredCmd)
}

// optionalCell renders a value the platform may not have answered as "-"
// rather than an empty cell, so an unknown scope reads as unknown.
func optionalCell(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}
