package cmd

// `ankra application provenance checks` and `verify --environment` (bead
// ankra-5uowtk.35.3.1): the newest kept provenance check of each installation
// of an application, by environment, over GET .../provenance/checks, and
// "Check now" of one environment, the environment form of POST
// .../provenance/verify. An installation with no kept check is "never
// checked"; a check that could not reach a verdict and a check whose images
// did not pass are said as two different answers.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newApplicationProvenanceChecksCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "checks <application>",
		Short: "Show the last provenance check of each environment's installations",
		Long: `Show the newest provenance check Ankra kept for each installation of the
application, by environment: when it was made, who asked for it, the tag it
checked and its answer.

A check is kept when Ankra deploys an installation and checks its images, and
when someone checks an environment now ('ankra application provenance verify
--environment'). ANSWER is one of:

  verified                  the images carry signed provenance that passed
  did not pass: <why>       the check ran and the images did not pass
  could not be checked: <why>
                            the check could not reach a verdict, or found no
                            image of the application's registry to check
  never checked             no check of the installation was kept

TAG is marked "not the tag stored now" when the installation has stored
another tag for its next release since the check.`,
		Example: `  ankra application provenance checks shop
  ankra application provenance checks shop -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationReference := strings.TrimSpace(arguments[0])
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			checks, readError := apiClient.GetApplicationProvenanceChecks(requestContext, applicationID)
			if readError != nil {
				return provenanceError(readError, "reading the provenance checks of the application", false)
			}
			var unreadable error
			if checks.ReadState == client.ProvenanceChecksReadUnknown {
				unreadable = withExitCode(exitError, fmt.Errorf(
					"the platform could not read the environments or the kept provenance checks of application %s; "+
						"this does not mean nothing was checked. Try again shortly", applicationReference))
			}
			if rendered, renderError := renderStructured(command, checks); rendered || renderError != nil {
				if renderError != nil {
					return renderError
				}
				return unreadable
			}
			if unreadable != nil {
				return unreadable
			}
			names, namesError := provenanceEnvironmentNames(requestContext, applicationID)
			if namesError != nil {
				_, _ = fmt.Fprintf(command.ErrOrStderr(),
					"The environment names could not be read (%v); ENVIRONMENT shows their ids.\n", namesError)
			}
			return writeApplicationProvenanceChecks(command.OutOrStdout(), checks, names)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

// runApplicationProvenanceVerifyEnvironment is `verify --environment`: Check
// now of one environment, named by name, role or id among the application's
// environments.
func runApplicationProvenanceVerifyEnvironment(command *cobra.Command, arguments []string) error {
	if images, _ := command.Flags().GetStringArray("image"); len(images) > 0 {
		return withExitCode(exitUsage, errors.New("pass --image or --environment, not both"))
	}
	reference, _ := command.Flags().GetString("environment")
	if strings.TrimSpace(reference) == "" {
		return withExitCode(exitUsage, errors.New("--environment is empty: pass the environment's name or id"))
	}
	applicationID, resolveError := resolveApplicationArgument(command, arguments)
	if resolveError != nil {
		return resolveError
	}
	requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
	defer cancel()
	environments, environmentsError := readProvenanceEnvironments(requestContext, applicationID)
	if environmentsError != nil {
		return fmt.Errorf("reading the application's environments to find %q: %w", strings.TrimSpace(reference), environmentsError)
	}
	environment, matchError := resolvePromotionEnvironment(environments, reference)
	if matchError != nil {
		return matchError
	}
	check, checkError := apiClient.CheckApplicationProvenanceEnvironment(requestContext, applicationID, environment.ID)
	if checkError != nil {
		return provenanceError(checkError, "checking "+environment.Name, false)
	}
	var refusal error
	if check.Decision == "refuse" {
		refusal = withExitCode(exitError, fmt.Errorf("a deploy of what %s runs would be refused", environment.Name))
	}
	if rendered, renderError := renderStructured(command, check); rendered || renderError != nil {
		if renderError != nil {
			return renderError
		}
		return refusal
	}
	writeProvenanceEnvironmentCheck(command.OutOrStdout(), strings.TrimSpace(arguments[0]), environment.Name, check)
	return refusal
}

// readProvenanceEnvironments reads the application's environments: the ids
// the provenance routes name environments by.
func readProvenanceEnvironments(requestContext context.Context, applicationID string) ([]promotionEnvironment, error) {
	payload, readError := apiClient.GetApplicationEnvironments(requestContext, applicationID)
	if readError != nil {
		return nil, readError
	}
	var listing struct {
		Environments []promotionEnvironment `json:"environments"`
	}
	if decodeError := json.Unmarshal(payload, &listing); decodeError != nil {
		return nil, fmt.Errorf("parsing the environments: %w", decodeError)
	}
	return listing.Environments, nil
}

// provenanceEnvironmentNames maps each environment id to its name.
func provenanceEnvironmentNames(requestContext context.Context, applicationID string) (map[string]string, error) {
	environments, readError := readProvenanceEnvironments(requestContext, applicationID)
	if readError != nil {
		return map[string]string{}, readError
	}
	names := make(map[string]string, len(environments))
	for _, environment := range environments {
		if strings.TrimSpace(environment.Name) != "" {
			names[environment.ID] = environment.Name
		}
	}
	return names, nil
}

// writeApplicationProvenanceChecks prints one row for each installation of
// each environment, an environment with no installation as one row of its
// own, and what the table leaves out.
func writeApplicationProvenanceChecks(output io.Writer, checks *client.ApplicationProvenanceChecks, names map[string]string) error {
	if len(checks.Environments) == 0 {
		_, _ = fmt.Fprintln(output, "The application has no environment you can see.")
		return nil
	}
	var notes []string
	hasNeverChecked := false
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ENVIRONMENT\tCLUSTER\tNAMESPACE\tCHECKED\tBY\tTAG\tANSWER")
	for _, environment := range checks.Environments {
		name := names[environment.EnvironmentID]
		if name == "" {
			name = environment.EnvironmentID
		}
		if len(environment.Placements) == 0 {
			_, _ = fmt.Fprintf(writer, "%s\t-\t-\t-\t-\t-\tno installation to check\n", name)
		}
		for _, placement := range environment.Placements {
			namespace := valueOrDashString(strings.TrimSpace(stringValue(placement.Namespace)))
			lastCheck := placement.LastCheck
			if lastCheck == nil {
				hasNeverChecked = true
				_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t-\t-\t-\tnever checked\n", name, placement.ClusterID, namespace)
				continue
			}
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, placement.ClusterID, namespace,
				provenanceCheckMoment(lastCheck.CheckedAt), provenanceCheckBy(lastCheck), provenanceCheckTag(lastCheck),
				provenanceCheckAnswer(lastCheck.State, lastCheck.Message))
		}
		if environment.PlacementsCapped {
			notes = append(notes, fmt.Sprintf("%s holds more installations than these %d; the rest are not listed.",
				name, len(environment.Placements)))
		}
		if environment.PlacementsNotCheckable == 1 {
			notes = append(notes, name+" also runs 1 deployment with no installation record, which no check covers.")
		} else if environment.PlacementsNotCheckable > 1 {
			notes = append(notes, fmt.Sprintf("%s also runs %d deployments with no installation record, which no check covers.",
				name, environment.PlacementsNotCheckable))
		}
	}
	if flushError := writer.Flush(); flushError != nil {
		return flushError
	}
	if hasNeverChecked {
		notes = append(notes, "Checks are kept from "+provenanceCheckMoment(checks.KeptFrom)+
			": never checked means no check of that installation since then.")
	}
	for _, note := range notes {
		_, _ = fmt.Fprintln(output, note)
	}
	return nil
}

// writeProvenanceEnvironmentCheck prints a Check now: the decision and the
// images' verdict as `verify` prints them, then one row for each
// installation checked.
func writeProvenanceEnvironmentCheck(output io.Writer, applicationReference string, environmentName string,
	check *client.ProvenanceEnvironmentCheck) {
	_, _ = fmt.Fprintf(output, "Checked what %s runs. Each check is kept as the installation's last one.\n", environmentName)
	printProvenanceVerification(output, applicationReference, &client.ProvenanceVerification{
		ApplicationID: check.ApplicationID, Mode: check.Mode, Decision: check.Decision, Verdict: check.Verdict,
	})
	if check.ImagesCapped {
		_, _ = fmt.Fprintln(output, "The image lists were cut: refused and unknown images are listed first. -o json prints the whole answer.")
	}
	_, _ = fmt.Fprintln(output)
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "CLUSTER\tNAMESPACE\tTAG\tANSWER")
	for _, installationCheck := range check.Checks {
		tag := valueOrDashString(strings.TrimSpace(stringValue(installationCheck.ImageTag)))
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", installationCheck.ClusterID,
			valueOrDashString(installationCheck.Namespace), tag, provenanceCheckAnswer(installationCheck.State, installationCheck.Message))
	}
	_ = writer.Flush()
	if check.ChecksCapped {
		_, _ = fmt.Fprintf(output, "%s holds more than %d installations; only these were checked.\n", environmentName, len(check.Checks))
	}
}

// provenanceCheckAnswer is the ANSWER cell: a check that ran and passed, ran
// and did not pass, or could not reach a verdict, each said as itself.
func provenanceCheckAnswer(state string, message *string) string {
	reason := strings.TrimSpace(stringValue(message))
	withReason := func(answer string) string {
		if reason == "" {
			return answer
		}
		return answer + ": " + reason
	}
	switch state {
	case "verified":
		return "verified"
	case "refused":
		return withReason("did not pass")
	case "unknown":
		return withReason("could not be checked")
	case "":
		return "-"
	default:
		return withReason(state)
	}
}

// provenanceCheckBy says who asked for a kept check.
func provenanceCheckBy(check *client.ProvenanceKeptCheck) string {
	asked := "check now"
	if check.Source == "release" {
		asked = "at release"
	} else if check.Source != "check_now" && check.Source != "" {
		asked = check.Source
	}
	if check.By == nil {
		return asked
	}
	who := strings.TrimSpace(stringValue(check.By.Name))
	if who == "" {
		switch check.By.Kind {
		case "ankra":
			who = "Ankra"
		case "former_member":
			who = "a former member"
		case "not_recorded":
			who = "not recorded"
		case "service_account":
			who = "a service account"
		default:
			who = valueOrDashString(check.By.Kind)
		}
	}
	return who + ", " + asked
}

// provenanceCheckTag is the TAG cell, marked when the installation stores
// another tag for its next release now.
func provenanceCheckTag(check *client.ProvenanceKeptCheck) string {
	tag := valueOrDashString(strings.TrimSpace(stringValue(check.ImageTag)))
	if check.IsCurrent != nil && !*check.IsCurrent {
		tag += " (not the tag stored now)"
	}
	return tag
}

// provenanceCheckMoment prints a platform timestamp as its UTC minute, or as
// sent when it does not parse.
func provenanceCheckMoment(timestamp string) string {
	moment, parseError := time.Parse(time.RFC3339, timestamp)
	if parseError != nil {
		return valueOrDashString(timestamp)
	}
	return moment.UTC().Format("2006-01-02 15:04 UTC")
}
