package cmd

import (
	"context"
	"fmt"
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
	RunE: runClusterValidate,
}

func init() {
	clusterValidateCmd.Flags().StringP("file", "f", "", "Path to the ImportCluster YAML file to validate")
	clusterValidateCmd.Flags().Bool("strict-secrets", false, "Treat plaintext secrets as errors instead of warnings")
	clusterValidateCmd.Flags().String("cluster", "", "Validate against an existing cluster's resources (cluster ID)")
	registerStructuredOutputFlags(clusterValidateCmd)
	_ = clusterValidateCmd.MarkFlagRequired("file")
	clusterCmd.AddCommand(clusterValidateCmd)
}

func runClusterValidate(cmd *cobra.Command, _ []string) error {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return fmt.Errorf("reading --file: %w", err)
	}
	strictSecrets, _ := cmd.Flags().GetBool("strict-secrets")
	clusterID, _ := cmd.Flags().GetString("cluster")

	importRequest, err := buildImportRequest(filePath)
	if err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}
	if err := validateResourceGraph(importRequest); err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := apiClient.ValidateCluster(ctx, importRequest.Spec, strictSecrets, clusterID)
	if err != nil {
		return fmt.Errorf("validating cluster: %w", err)
	}

	rendered, err := renderStructured(cmd, result)
	if err != nil {
		return err
	}
	if rendered {
		if len(result.Errors) > 0 {
			return fmt.Errorf("validation failed for %q", filePath)
		}
		return nil
	}

	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s %q (%s): %s\n", warning.Kind, warning.Name, warning.Category, warning.Message)
	}

	if len(result.Errors) > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Validation failed for %q:\n", filePath)
		for _, resourceError := range result.Errors {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "- %s %q:\n", resourceError.Kind, resourceError.Name)
			for _, detail := range resourceError.Errors {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "    • %s: %s\n", detail.Key, detail.Message)
			}
		}
		return fmt.Errorf("validation failed for %q", filePath)
	}

	if len(result.Warnings) > 0 {
		fmt.Printf("Validation passed for %q with %d warning(s); no changes applied.\n", filePath, len(result.Warnings))
		return nil
	}
	fmt.Printf("Validation passed for %q; no changes applied.\n", filePath)
	return nil
}
