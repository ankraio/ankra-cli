package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var clusterValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate an ImportCluster YAML against the Ankra API",
	Long: `Validate an ImportCluster YAML file. The local structural, dependency,
and YAML checks run first (the same ones as 'ankra cluster apply --dry-run'),
then the file is sent to the Ankra API for server-side validation that the
offline checks cannot perform:

  - chart existence in the Helm registries connected to your organisation
  - plaintext Kubernetes Secret / unencrypted addon value detection
  - parent references resolved against a cluster's existing resources

Nothing is applied. Use --cluster <id> to validate the spec against an
existing cluster's deployed resources, and --strict-secrets to treat
plaintext secrets as errors instead of warnings.`,
	Args: cobra.NoArgs,
	Run:  runClusterValidate,
}

func init() {
	clusterValidateCmd.Flags().StringP("file", "f", "", "Path to the ImportCluster YAML file to validate")
	clusterValidateCmd.Flags().Bool("strict-secrets", false, "Treat plaintext secrets as errors instead of warnings")
	clusterValidateCmd.Flags().String("cluster", "", "Validate against an existing cluster's resources (cluster ID)")
	registerStructuredOutputFlags(clusterValidateCmd)
	if err := clusterValidateCmd.MarkFlagRequired("file"); err != nil {
		fmt.Fprintf(os.Stderr, "Error marking flag as required: %s\n", err)
		os.Exit(1)
	}
	clusterCmd.AddCommand(clusterValidateCmd)
}

func runClusterValidate(cmd *cobra.Command, _ []string) {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading --file: %s\n", err)
		os.Exit(1)
	}
	strictSecrets, _ := cmd.Flags().GetBool("strict-secrets")
	clusterID, _ := cmd.Flags().GetString("cluster")

	importRequest, err := buildImportRequest(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid ImportCluster in %q:\n  %s\n", filePath, err)
		os.Exit(1)
	}
	if err := validateResourceGraph(importRequest); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid ImportCluster in %q:\n  %s\n", filePath, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := apiClient.ValidateCluster(ctx, importRequest.Spec, strictSecrets, clusterID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error validating cluster: %s\n", err)
		os.Exit(1)
	}

	if renderStructuredOrExit(cmd, result) {
		if len(result.Errors) > 0 {
			os.Exit(1)
		}
		return
	}

	for _, warning := range result.Warnings {
		fmt.Fprintf(os.Stderr, "Warning: %s %q (%s): %s\n", warning.Kind, warning.Name, warning.Category, warning.Message)
	}

	if len(result.Errors) > 0 {
		fmt.Fprintf(os.Stderr, "Validation failed for %q:\n", filePath)
		for _, resourceError := range result.Errors {
			fmt.Fprintf(os.Stderr, "- %s %q:\n", resourceError.Kind, resourceError.Name)
			for _, detail := range resourceError.Errors {
				fmt.Fprintf(os.Stderr, "    • %s: %s\n", detail.Key, detail.Message)
			}
		}
		os.Exit(1)
	}

	if len(result.Warnings) > 0 {
		fmt.Printf("Validation passed for %q with %d warning(s); no changes applied.\n", filePath, len(result.Warnings))
		return
	}
	fmt.Printf("Validation passed for %q; no changes applied.\n", filePath)
}
