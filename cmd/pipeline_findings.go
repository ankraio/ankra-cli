package cmd

// The findings verb: a run's persisted scan findings
// (go/internal/pipelineapi/findings.go, enginekit/pipelinefindings) - the
// deduplicated Semgrep, Checkov and Trivy results (and the informational
// SBOM summary) recorded for the run's own commit, the same rows the
// application's Security tab reads once a pipeline run exists for it.
// Read-only: nothing here decides or reruns a gate verdict, which is a
// separate concern (the run's own "gate" step, see 'ankra pipeline get').

import (
	"fmt"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func newPipelineFindingsCommand() *cobra.Command {
	findingsCommand := &cobra.Command{
		Use:   "findings <run>",
		Short: "List a pipeline run's persisted scan findings",
		Long: `List a pipeline run's persisted scan findings.

Findings are recorded once a "scan" step has concluded for the run's own
commit; a run with no scan step, or one still running, answers an empty
list rather than an error. The default table sorts worst severity first and
groups by tool, so the findings most likely to block a "gate" step are
always at the top; -o json/yaml prints every field the server carries,
including each finding's tool-specific detail.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineFindingsList(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(findingsCommand)
	registerStructuredOutputFlags(findingsCommand)
	return findingsCommand
}

// pipelineFindingSeverityRank orders the platform's shared severity
// vocabulary from most to least severe, mirroring
// enginekit/pipelinegate's own severityRank so the table's default sort
// reads worst-first regardless of what order the server answered in. A
// severity this map does not recognise sorts as UNKNOWN (0), never assumed
// worse than something the platform did classify.
var pipelineFindingSeverityRank = map[string]int{
	client.PipelineFindingSeverityCritical: 4,
	client.PipelineFindingSeverityHigh:     3,
	client.PipelineFindingSeverityMedium:   2,
	client.PipelineFindingSeverityLow:      1,
	client.PipelineFindingSeverityUnknown:  0,
}

func runPipelineFindingsList(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	list, listError := apiClient.ListPipelineFindings(command.Context(), selector, strings.TrimSpace(runID))
	if listError != nil {
		return listError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, list)
	}
	if len(list.Findings) == 0 {
		_, _ = fmt.Fprintln(command.OutOrStdout(), "No findings recorded for this run.")
		return nil
	}

	findings := append([]client.PipelineFinding(nil), list.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		rankI, rankJ := pipelineFindingSeverityRank[findings[i].Severity], pipelineFindingSeverityRank[findings[j].Severity]
		if rankI != rankJ {
			return rankI > rankJ
		}
		if findings[i].Tool != findings[j].Tool {
			return findings[i].Tool < findings[j].Tool
		}
		return findings[i].Title < findings[j].Title
	})

	writer := table.NewWriter()
	writer.SetOutputMirror(command.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"TOOL", "SEVERITY", "RULE / CVE", "TITLE", "PACKAGE", "PATH", "FIXED IN"})
	for _, finding := range findings {
		writer.AppendRow(table.Row{
			finding.Tool,
			finding.Severity,
			pipelineFindingRuleOrCVE(finding),
			pipelineStringOrDash(finding.Title),
			pipelineFindingPackage(finding),
			pipelineStringOrDash(finding.Path),
			pipelineStringOrDash(finding.FixedVersion),
		})
	}
	writer.Render()
	return nil
}

// pipelineFindingRuleOrCVE is the finding's own identity for the table: the
// CVE when the tool named one (Trivy), otherwise the scanner's own rule id
// (Semgrep, Checkov).
func pipelineFindingRuleOrCVE(finding client.PipelineFinding) string {
	if finding.CVEID != nil && *finding.CVEID != "" {
		return *finding.CVEID
	}
	return pipelineStringOrDash(finding.RuleID)
}

// pipelineFindingPackage renders a Trivy finding's affected dependency as
// "name@version"; a finding with no package name (Semgrep, Checkov, most
// SBOM rows) has nothing to show here.
func pipelineFindingPackage(finding client.PipelineFinding) string {
	if finding.PackageName == "" {
		return "-"
	}
	if finding.PackageVersion == "" {
		return finding.PackageName
	}
	return finding.PackageName + "@" + finding.PackageVersion
}

func pipelineStringOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
