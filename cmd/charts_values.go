package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var chartsValuesCmd = &cobra.Command{
	Use:   "values <chart_name>",
	Short: "Print a chart's default values (helm show values)",
	Long: `Print the default values document shipped inside a chart version.

By default the decoded YAML is written to stdout, making it easy to pipe into
a file, edit, and feed back to 'ankra charts template -f':

  ankra charts values nginx --version 1.2.3 > values.yaml
  ankra charts values nginx --version 1.2.3 -o raw   # base64-encoded form

The repository is resolved automatically when the chart name is unambiguous;
pass --repository to pin it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		chartName := args[0]
		chartVersion, _ := cmd.Flags().GetString("version")
		repositoryName, _ := cmd.Flags().GetString("repository")
		outRaw, _ := cmd.Flags().GetString("output")

		if repositoryName == "" {
			resolved, err := resolveChartRepositoryName(chartName)
			if err != nil {
				return err
			}
			repositoryName = resolved
		}

		result, err := apiClient.GetChartDefaultValues(repositoryName, chartName, chartVersion)
		if err != nil {
			if errors.Is(err, client.ErrChartNotFound) {
				return withExitCode(exitNotFound, err)
			}
			return fmt.Errorf("fetching chart values: %w", err)
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(result.ValuesBase64)
		if decodeErr != nil {
			return fmt.Errorf("base64-decode chart values: %w", decodeErr)
		}
		return writeDecodedDoc(cmd.OutOrStdout(), string(decoded), outRaw)
	},
}

// resolveChartRepositoryName finds the repository carrying a chart via the
// catalog search, mirroring the lookup 'charts info' performs. Ambiguity or
// a miss asks the user to pass --repository explicitly.
func resolveChartRepositoryName(chartName string) (string, error) {
	charts, err := apiClient.SearchCharts(chartName)
	if err != nil {
		return "", fmt.Errorf("finding chart: %w", err)
	}
	repositoryNames := make([]string, 0, 1)
	for _, chart := range charts {
		if !strings.EqualFold(chart.Name, chartName) {
			continue
		}
		alreadyKnown := false
		for _, knownName := range repositoryNames {
			if knownName == chart.RepositoryName {
				alreadyKnown = true
				break
			}
		}
		if !alreadyKnown {
			repositoryNames = append(repositoryNames, chart.RepositoryName)
		}
	}
	switch len(repositoryNames) {
	case 0:
		return "", withExitCode(exitNotFound,
			fmt.Errorf("chart '%s' not found. Please specify --repository flag", chartName))
	case 1:
		return repositoryNames[0], nil
	default:
		return "", withExitCode(exitUsage, fmt.Errorf(
			"chart '%s' exists in several repositories (%s); specify --repository",
			chartName, strings.Join(repositoryNames, ", ")))
	}
}

func init() {
	chartsValuesCmd.Flags().String("version", "", "Chart version (required)")
	chartsValuesCmd.Flags().String("repository", "", "Repository name for the chart")
	chartsValuesCmd.Flags().StringP("output", "o", "", "Output format: yaml (decoded, default) or raw (base64)")
	_ = chartsValuesCmd.MarkFlagRequired("version")

	chartsCmd.AddCommand(chartsValuesCmd)
}
