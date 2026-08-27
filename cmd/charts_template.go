package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var chartsTemplateCmd = &cobra.Command{
	Use:   "template <chart_name>",
	Short: "Render a chart's Kubernetes manifests (helm template)",
	Long: `Render a chart version's Kubernetes manifests server-side, without
deploying anything - the 'helm template' equivalent for the Ankra catalog.

The manifests are printed to stdout as a '---' separated multi-doc YAML
stream with a '# Source:' header per document, so the output pipes straight
into kubectl diff, kubeconform, or a file. Chart NOTES.txt output goes to
stderr.

Values problems (bad YAML, schema violations, template errors) fail with the
exact Helm error a deploy would have produced - validate values before they
reach production:

  ankra charts template nginx --version 1.2.3
  ankra charts template nginx --version 1.2.3 -f values.yaml
  ankra charts template nginx --version 1.2.3 --release-name web --namespace edge`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		chartName := args[0]
		chartVersion, _ := cmd.Flags().GetString("version")
		repositoryName, _ := cmd.Flags().GetString("repository")
		valuesFile, _ := cmd.Flags().GetString("values")
		releaseName, _ := cmd.Flags().GetString("release-name")
		namespace, _ := cmd.Flags().GetString("namespace")

		if repositoryName == "" {
			resolved, err := resolveChartRepositoryName(chartName)
			if err != nil {
				return err
			}
			repositoryName = resolved
		}

		request := client.TemplateChartRequest{
			RepositoryName: repositoryName,
			ChartName:      chartName,
			ChartVersion:   chartVersion,
		}
		if valuesFile != "" {
			content, readErr := os.ReadFile(valuesFile)
			if readErr != nil {
				return fmt.Errorf("reading values file: %w", readErr)
			}
			encoded := base64.StdEncoding.EncodeToString(content)
			request.ValuesBase64 = &encoded
		}
		if releaseName != "" {
			request.ReleaseName = &releaseName
		}
		if namespace != "" {
			request.Namespace = &namespace
		}

		result, err := apiClient.TemplateChart(request)
		if err != nil {
			var templateErr *client.ChartTemplateError
			if errors.As(err, &templateErr) {
				return fmt.Errorf("chart rendering failed:\n%s", templateErr.Message)
			}
			if errors.Is(err, client.ErrChartNotFound) {
				return withExitCode(exitNotFound, err)
			}
			return fmt.Errorf("rendering chart: %w", err)
		}

		out := cmd.OutOrStdout()
		for _, manifest := range result.Rendered {
			decoded, decodeErr := base64.StdEncoding.DecodeString(manifest.ContentBase64)
			if decodeErr != nil {
				return fmt.Errorf("base64-decode manifest %s: %w", manifest.Path, decodeErr)
			}
			text := string(decoded)
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			_, _ = fmt.Fprintf(out, "---\n# Source: %s\n%s", manifest.Path, text)
		}
		if len(result.Rendered) == 0 {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "The chart rendered no manifests.")
		}
		if result.Notes != nil && strings.TrimSpace(*result.Notes) != "" {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nNOTES:\n%s", *result.Notes)
		}
		return nil
	},
}

func init() {
	chartsTemplateCmd.Flags().String("version", "", "Chart version (required)")
	chartsTemplateCmd.Flags().String("repository", "", "Repository name for the chart")
	chartsTemplateCmd.Flags().StringP("values", "f", "", "YAML file overriding the chart's default values")
	chartsTemplateCmd.Flags().String("release-name", "", "Release name for the render (default: the chart name)")
	chartsTemplateCmd.Flags().String("namespace", "", "Release namespace for the render (default: \"default\")")
	_ = chartsTemplateCmd.MarkFlagRequired("version")

	chartsCmd.AddCommand(chartsTemplateCmd)
}
