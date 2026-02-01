package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterSopsCmd = &cobra.Command{
	Use:   "sops <secret>",
	Short: "Encrypt a secret value using SOPS",
	Long: `Encrypt a secret value using SOPS encryption for the active cluster.

The encrypted value can be safely committed to your Git repository. The cluster
will automatically decrypt it during deployment.

Example:
  ankra cluster sops "my-secret-password"
  ankra cluster sops "api-key-12345"`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		secret := args[0]

		_, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			os.Exit(1)
		}

		encrypted, err := client.EncryptSecret(apiToken, baseURL, secret)
		if err != nil {
			fmt.Printf("Error encrypting secret: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(encrypted)
	},
}

func init() {
	clusterCmd.AddCommand(clusterSopsCmd)
}
