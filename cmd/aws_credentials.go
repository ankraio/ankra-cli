package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// The AWS credential is the platform's existing aws credential - an access
// key pair or an assumable role - shared by cost reporting, EKS and the
// self-managed EC2 clusters. Its onboarding (external id, launch-stack URL),
// role and keys writes are mounted on the session surface only
// (/org/credentials/aws/onboarding|role|keys); the bearer /api/v1 surface the
// CLI uses carries no twin of them yet, so this group lists and stops there.
// SSH key credentials are provider-agnostic: create one with any provider's
// ssh-key group (for example `ankra credentials hetzner ssh-key create`) and
// pass its id to `ankra cluster aws create --ssh-key-credential-id`.
var awsCredCmd = &cobra.Command{
	Use:   "aws",
	Short: "Manage AWS credentials",
	Long: `List the AWS credentials (access key pairs and assumable roles) the
organisation has connected.

Connecting a new AWS account - the external id, the CloudFormation
launch-stack URL and the role or access-key registration - is done in the
dashboard under Credentials; the platform serves those steps on the session
surface only, which the CLI's token cannot reach. Once connected, the
credential's id is what 'ankra cluster aws create --credential-id' takes.

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
		t.AppendHeader(table.Row{"ID", "Name", "State", "Available", "Created"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 15},
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
				state,
				available,
				formatTimeAgo(credential.CreatedAt),
			})
		}
		t.Render()
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(awsCredListCmd)
	awsCredCmd.AddCommand(awsCredListCmd)
	credentialsCmd.AddCommand(awsCredCmd)
}
