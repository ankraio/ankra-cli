package cmd

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

func newApplicationSecurityVersionsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "security-versions <application-id>",
		Short: "Every published image version with its bill of materials, findings and where it runs",
		Long: `List the tags the application has published, joined to the bill of
materials and vulnerability findings the platform holds for each, and the
clusters running it now. A tag with no report reads "not scanned", which
is not the same as clean, and a registry the platform could not list reads
unavailable rather than empty.

The licence verdict at the end says whether a private repository links a
component whose licence obliges publishing the source (AGPL, SSPL and the
other network copyleft licences) or a commercial licence to host as a
service.

--component narrows a monorepo to one image.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			component, _ := command.Flags().GetString("component")
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			versions, err := apiClient.GetApplicationSecurityVersions(command.Context(), applicationID, component)
			if err != nil {
				return fmt.Errorf("reading application security versions: %w", err)
			}
			if rendered, err := renderStructured(command, versions); rendered || err != nil {
				return err
			}
			renderApplicationSecurityVersions(command.OutOrStdout(), versions)
			return nil
		},
	}
	command.Flags().String("component", "", "Narrow a monorepo to one component (image)")
	registerStructuredOutputFlags(command)
	return command
}

func renderApplicationSecurityVersions(out io.Writer, versions *client.ApplicationSecurityVersions) {
	repository := versions.Repository
	_, _ = fmt.Fprintf(out, "%s/%s (%s, %s repository)\n", repository.Owner, repository.Name, repository.Provider, repository.Visibility)
	for _, component := range versions.Components {
		line := fmt.Sprintf("  %s: %s/%s registry %s", component.Name, component.Registry, component.Repository, component.RegistryStatus)
		if component.Message != nil && *component.Message != "" {
			line += " - " + *component.Message
		}
		_, _ = fmt.Fprintln(out, line)
	}
	summary := versions.Summary
	_, _ = fmt.Fprintf(out, "  %d versions · %d with a bill of materials · %d with findings · %d running\n",
		summary.Versions, summary.WithSBOM, summary.WithFindings, summary.Running)
	if len(versions.Versions) > 0 {
		_, _ = fmt.Fprintln(out)
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"Component", "Tag", "Pushed", "SBOM", "Findings", "Known exploited", "Licence exposure", "Running"})
		for _, version := range versions.Versions {
			writer.AppendRow(table.Row{
				version.Component,
				version.Tag,
				optionalTimeAgo(version.PushedAt),
				applicationVersionSBOMCell(version.SBOM),
				applicationVersionFindingsCell(version.Findings),
				applicationVersionKnownExploitedCell(version.Findings),
				licenseExposureCell(version.SBOM.LicenseExposure),
				applicationVersionRunningCell(version.Running),
			})
		}
		writer.Render()
	}
	renderApplicationLicenseExposure(out, versions.LicenseExposure, repository.Visibility)
}

func applicationVersionSBOMCell(sbom client.ApplicationImageVersionSBOM) string {
	switch sbom.Status {
	case "present":
		if sbom.ComponentCount != nil {
			return fmt.Sprintf("%d components", *sbom.ComponentCount)
		}
		return "present"
	case "absent":
		return text.FgYellow.Sprint("absent")
	case "":
		return "unknown"
	default:
		return sbom.Status
	}
}

// applicationVersionKnownExploitedCell keeps an unscanned tag from reading
// as "0 known exploited": absence of a report is not a negative answer.
func applicationVersionKnownExploitedCell(findings client.ApplicationImageVersionFindings) string {
	if !findings.Scanned {
		return "-"
	}
	return fmt.Sprintf("%d", findings.KnownExploited)
}

func applicationVersionFindingsCell(findings client.ApplicationImageVersionFindings) string {
	if !findings.Scanned {
		return text.FgYellow.Sprint("not scanned")
	}
	actionable := severityCountsCell(findings.Actionable)
	if actionable == "0" {
		return fmt.Sprintf("clean (%d observed)", findings.Observed)
	}
	return actionable + fmt.Sprintf(" · %d fixable severe", findings.FixableSevere)
}

func applicationVersionRunningCell(running client.ApplicationImageVersionRunning) string {
	if running.Workloads == 0 {
		return "-"
	}
	namespaces := ""
	if len(running.Namespaces) > 0 {
		namespaces = " in " + strings.Join(running.Namespaces, ", ")
	}
	return fmt.Sprintf("%d workloads on %d clusters%s", running.Workloads, running.Clusters, namespaces)
}

// renderApplicationLicenseExposure prints the source-disclosure verdict.
// An unassessed application says so; it never reads as clear.
func renderApplicationLicenseExposure(out io.Writer, exposure client.ApplicationLicenseExposure, visibility string) {
	_, _ = fmt.Fprintln(out)
	if exposure.Latest == nil {
		_, _ = fmt.Fprintf(out, "Licence exposure: %s - no bill of materials has been assessed yet\n", exposure.Status)
		return
	}
	_, _ = fmt.Fprintf(out, "Licence exposure (newest bill of materials per component): %s\n", licenseExposureCell(exposure.Latest))
	switch {
	case exposure.SourceDisclosureRequired:
		_, _ = fmt.Fprintln(out, text.FgRed.Sprintf("  The repository is %s and links network-copyleft components: shipping this service obliges publishing its source, or replacing the components.", visibility))
	case exposure.ServiceLicenceRequired:
		_, _ = fmt.Fprintln(out, text.FgRed.Sprint("  Source-available components are linked: hosting this as a service needs a commercial licence from their vendors."))
	default:
		_, _ = fmt.Fprintln(out, "  No licence obliges publishing the source or a service licence.")
	}
	if len(exposure.Components) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "  %d flagged components:\n", exposure.FlaggedComponents)
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Component", "Tag", "Package", "Version", "Type", "Licences", "Tier"})
	for _, component := range exposure.Components {
		writer.AppendRow(table.Row{component.Component, component.Tag, component.Name, component.Version, component.PackageType, strings.Join(component.Licenses, ", "), licenseRiskCell(component.LicenseRisk)})
	}
	writer.Render()
}
