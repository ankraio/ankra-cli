package cmd

// What ship does when the branch's push built nothing because the setup pull
// request is still open (ankra-oznf7k.4). That is the ordinary state of a new
// repository, not a failure: nothing can build the tracked branch until a
// person merges the pull request. Ship follows the pull request's own run, so
// the person learns whether it is ready to merge, and stops with a state and an
// exit code of their own. Running ship again after the merge continues from
// there: the merge moves the branch to a new head, and its push run publishes.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ankra/internal/client"
)

// The ship states of a stop at the setup pull request.
const (
	shipStateWaitingForSetupPullRequest = "waiting_for_setup_pull_request"
	shipStateSetupPullRequestBlocked    = "setup_pull_request_blocked"
	shipStateSetupPullRequestFailed     = "setup_pull_request_failed"
)

// shipPipelineStepKindGate is the kind of the step that passes or blocks on a
// run's findings (cluster enginekit/pipelinespec.KindGate).
const shipPipelineStepKindGate = "gate"

// shipPipelineCancelReasonPullRequestClosed is the cancel reason of a run whose
// pull request merged or closed while it ran.
const shipPipelineCancelReasonPullRequestClosed = "pull_request_closed"

// shipBlockingFindingLimit bounds how many findings a blocked stop names.
const shipBlockingFindingLimit = 5

// shipSetupRunAppearGrace is how long ship waits for the pull request's run to
// appear before stopping without one. A var so the tests can shorten it.
var shipSetupRunAppearGrace = time.Minute

// shipSetupPullRequestPath matches the path of a GitHub pull request URL. A
// GitLab or Bitbucket URL does not match, and ship then stops without
// following a run rather than following the wrong one.
var shipSetupPullRequestPath = regexp.MustCompile(`^/[^/]+/[^/]+/pull/(\d+)/?$`)

// shipSetupPullRequestRun is the pull request run a stop reports.
type shipSetupPullRequestRun struct {
	ID        string `json:"id" yaml:"id"`
	RunNumber int64  `json:"run_number" yaml:"run_number"`
	HeadSHA   string `json:"head_sha" yaml:"head_sha"`
	Status    string `json:"status" yaml:"status"`
	Outcome   string `json:"outcome,omitempty" yaml:"outcome,omitempty"`
}

// shipBlockingFinding is one finding a blocked stop names.
type shipBlockingFinding struct {
	Severity       string `json:"severity" yaml:"severity"`
	Tool           string `json:"tool" yaml:"tool"`
	Identifier     string `json:"identifier,omitempty" yaml:"identifier,omitempty"`
	PackageName    string `json:"package_name,omitempty" yaml:"package_name,omitempty"`
	PackageVersion string `json:"package_version,omitempty" yaml:"package_version,omitempty"`
	FixedVersion   string `json:"fixed_version,omitempty" yaml:"fixed_version,omitempty"`
	Path           string `json:"path,omitempty" yaml:"path,omitempty"`
	Title          string `json:"title,omitempty" yaml:"title,omitempty"`
}

// shipSetupPullRequestStop is the error of a ship that stopped at the setup
// pull request. It carries what -o json prints, because the document is the
// answer a script reads.
type shipSetupPullRequestStop struct {
	message          string
	state            string
	pullRequestURL   string
	run              *shipSetupPullRequestRun
	blockingFindings []shipBlockingFinding
}

func (stop *shipSetupPullRequestStop) Error() string { return stop.message }

// shipSetupPullRequestNumber answers the pull request number in a GitHub URL.
func shipSetupPullRequestNumber(pullRequestURL string) (int64, bool) {
	parsed, parseError := url.Parse(strings.TrimSpace(pullRequestURL))
	if parseError != nil {
		return 0, false
	}
	match := shipSetupPullRequestPath.FindStringSubmatch(parsed.Path)
	if match == nil {
		return 0, false
	}
	number, numberError := strconv.ParseInt(match[1], 10, 64)
	if numberError != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

// findShipSetupPullRequestRun answers the newest run of the pull request.
func findShipSetupPullRequestRun(waitContext context.Context, selector client.PipelineSelector,
	pullRequestNumber int64) (*client.PipelineRun, error) {
	options := client.ListPipelineRunsOptions{Trigger: shipPipelineTriggerPullRequest, Limit: shipPipelineRunProbeLimit}
	for pageNumber := 0; pageNumber < shipPipelineRunMaxPages; pageNumber++ {
		page, listError := apiClient.ListPipelineRuns(waitContext, selector, options)
		if listError != nil {
			return nil, listError
		}
		if page == nil {
			return nil, nil
		}
		for index := range page.Runs {
			run := &page.Runs[index]
			if run.Trigger == shipPipelineTriggerPullRequest && run.PullRequestNumber != nil &&
				*run.PullRequestNumber == pullRequestNumber {
				return run, nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return nil, nil
		}
		options.Cursor = *page.NextCursor
	}
	return nil, nil
}

// followSetupPullRequest follows the setup pull request's run to its end and
// answers the stop that describes it. notRun is the sentence saying why the
// branch's own run built nothing.
func followSetupPullRequest(waitContext context.Context, progress io.Writer, application shipApplicationView,
	pullRequestURL string, notRun string) error {
	pullRequestNumber, isGitHub := shipSetupPullRequestNumber(pullRequestURL)
	_, _ = fmt.Fprintf(progress, "Waiting for the setup pull request: %s\n", pullRequestURL)
	selector := client.PipelineSelector{ApplicationID: application.ID}
	waiting := func(run *shipSetupPullRequestRun, sentence string) error {
		return withExitCode(exitWaiting, &shipSetupPullRequestStop{
			message: fmt.Sprintf("waiting for the setup pull request: %s. %s. %s", pullRequestURL, sentence,
				setupPullRequestNextStep(notRun)),
			state:          shipStateWaitingForSetupPullRequest,
			pullRequestURL: pullRequestURL,
			run:            run,
		})
	}
	if !isGitHub {
		return waiting(nil, "Ship cannot follow this pull request's run")
	}

	appearDeadline := time.Now().Add(shipSetupRunAppearGrace)
	announcedState := ""
	for {
		run, listError := findShipSetupPullRequestRun(waitContext, selector, pullRequestNumber)
		if listError != nil {
			return shipReadError(waitContext, "reading the setup pull request's run", listError)
		}
		if run == nil {
			if !time.Now().Before(appearDeadline) {
				return waiting(nil, "It has no pipeline run yet")
			}
		} else {
			detail, readError := apiClient.GetPipelineRun(waitContext, selector, run.ID)
			if readError != nil {
				return shipReadError(waitContext, "reading the setup pull request's run", readError)
			}
			if detail == nil {
				return fmt.Errorf("reading the setup pull request's run: the platform answered no detail for run %s", run.ID)
			}
			state := fmt.Sprintf("#%d %s", detail.RunNumber, detail.Status)
			if state != announcedState {
				_, _ = fmt.Fprintf(progress, "The setup pull request's run #%d is %s (follow it with 'ankra application pipeline get %s %s').\n",
					detail.RunNumber, detail.Status, application.ID, detail.ID)
				announcedState = state
			}
			if detail.Status == pipelineRunStatusConcluded && !pipelineRunIsSuperseded(detail.PipelineRun) {
				return setupPullRequestRunStop(waitContext, application.ID, pullRequestURL, notRun, detail, waiting)
			}
		}
		if tickError := shipWaitTick(waitContext, shipPollInterval, "waiting for the setup pull request's run"); tickError != nil {
			return tickError
		}
	}
}

// setupPullRequestRunStop turns the pull request's concluded run into a stop.
func setupPullRequestRunStop(waitContext context.Context, applicationID string, pullRequestURL string, notRun string,
	detail *client.PipelineRunDetail, waiting func(*shipSetupPullRequestRun, string) error) error {
	reported := &shipSetupPullRequestRun{
		ID:        detail.ID,
		RunNumber: detail.RunNumber,
		HeadSHA:   detail.HeadSHA,
		Status:    detail.Status,
		Outcome:   pipelineOptionalString(detail.Outcome),
	}
	if reported.Outcome == pipelineOutcomeSuccess {
		return waiting(reported, fmt.Sprintf("Its run #%d passed, so it is ready to merge", detail.RunNumber))
	}
	if pipelineOptionalString(detail.CancelReason) == shipPipelineCancelReasonPullRequestClosed {
		return withExitCode(exitWaiting, &shipSetupPullRequestStop{
			message: fmt.Sprintf("the setup pull request was merged or closed while ship waited, so its run #%d was cancelled: %s. "+
				"If it was merged, run ship again to follow the branch's own run", detail.RunNumber, pullRequestURL),
			state:          shipStateWaitingForSetupPullRequest,
			pullRequestURL: pullRequestURL,
			run:            reported,
		})
	}
	if shipPipelineGateFailed(detail) {
		findings, isRead := readShipBlockingFindings(waitContext, applicationID, detail.ID)
		findingsClause := describeShipBlockingFindings(findings)
		if !isRead {
			findingsClause = " (its findings could not be read)"
		}
		return withExitCode(exitBlocked, &shipSetupPullRequestStop{
			message: fmt.Sprintf("the setup pull request's run #%d was blocked by the gate%s - fix it on the pull request's branch "+
				"(%s), or read every finding with 'ankra application pipeline findings %s %s'",
				detail.RunNumber, findingsClause, pullRequestURL, applicationID, detail.ID),
			state:            shipStateSetupPullRequestBlocked,
			pullRequestURL:   pullRequestURL,
			run:              reported,
			blockingFindings: findings,
		})
	}
	return &shipSetupPullRequestStop{
		message: fmt.Sprintf("the setup pull request's run #%d did not pass (%v) - see why with 'ankra application pipeline logs %s %s', "+
			"and fix it on the pull request's branch (%s)",
			detail.RunNumber, pipelineRunConclusionError(detail.PipelineRun), applicationID, detail.ID, pullRequestURL),
		state:          shipStateSetupPullRequestFailed,
		pullRequestURL: pullRequestURL,
		run:            reported,
	}
}

// setupPullRequestNextStep says what a person does at the stop.
func setupPullRequestNextStep(notRun string) string {
	return fmt.Sprintf("Merge it, which pushes the branch with the pipeline's push trigger in place, then run ship again; "+
		"or re-run ship with --ankra-build to build on Ankra's builders now. Until then: %s", notRun)
}

// shipPipelineGateFailed reports whether a gate step's latest attempt failed.
func shipPipelineGateFailed(detail *client.PipelineRunDetail) bool {
	latestAttempts := map[string]client.PipelineStep{}
	for _, step := range detail.Steps {
		if step.Kind != shipPipelineStepKindGate {
			continue
		}
		key := step.Stage + "\x00" + step.StepKey + "\x00" + string(step.Matrix)
		if current, seen := latestAttempts[key]; !seen || step.Attempt > current.Attempt {
			latestAttempts[key] = step
		}
	}
	for _, step := range latestAttempts {
		if pipelineOptionalString(step.Outcome) == pipelineOutcomeFailure {
			return true
		}
	}
	return false
}

// readShipBlockingFindings reads the run's most severe findings, and whether
// they could be read at all: a failed read answers none and false, so the stop
// says the findings are unknown rather than absent.
func readShipBlockingFindings(waitContext context.Context, applicationID string, runID string) ([]shipBlockingFinding, bool) {
	list, listError := apiClient.ListPipelineFindings(waitContext, client.PipelineSelector{ApplicationID: applicationID}, runID)
	if listError != nil || list == nil {
		return nil, false
	}
	severityRank := map[string]int{client.PipelineFindingSeverityCritical: 0, client.PipelineFindingSeverityHigh: 1,
		client.PipelineFindingSeverityMedium: 2, client.PipelineFindingSeverityLow: 3}
	rankOf := func(severity string) int {
		if rank, isKnown := severityRank[severity]; isKnown {
			return rank
		}
		return len(severityRank)
	}
	vulnerabilities := make([]client.PipelineFinding, 0, len(list.Findings))
	for _, finding := range list.Findings {
		if finding.Tool != client.PipelineFindingToolSBOM {
			vulnerabilities = append(vulnerabilities, finding)
		}
	}
	sort.SliceStable(vulnerabilities, func(first int, second int) bool {
		return rankOf(vulnerabilities[first].Severity) < rankOf(vulnerabilities[second].Severity)
	})
	findings := make([]shipBlockingFinding, 0, shipBlockingFindingLimit)
	for _, finding := range vulnerabilities {
		if len(findings) == shipBlockingFindingLimit {
			break
		}
		identifier := finding.RuleID
		if finding.CVEID != nil && *finding.CVEID != "" {
			identifier = *finding.CVEID
		}
		findings = append(findings, shipBlockingFinding{
			Severity:       finding.Severity,
			Tool:           finding.Tool,
			Identifier:     identifier,
			PackageName:    finding.PackageName,
			PackageVersion: finding.PackageVersion,
			FixedVersion:   finding.FixedVersion,
			Path:           finding.Path,
			Title:          finding.Title,
		})
	}
	return findings, true
}

// describeShipBlockingFindings renders the findings as the clause of a stop.
func describeShipBlockingFindings(findings []shipBlockingFinding) string {
	if len(findings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		part := finding.Severity + " " + finding.Identifier
		if finding.PackageName != "" {
			part += " in " + strings.TrimSpace(finding.PackageName+" "+finding.PackageVersion)
		} else if finding.Path != "" {
			part += " in " + finding.Path
		}
		if finding.FixedVersion != "" {
			part += " (fixed in " + finding.FixedVersion + ")"
		}
		parts = append(parts, part)
	}
	return ": " + strings.Join(parts, "; ")
}

// renderShipStop prints the document of a ship that stopped at the setup pull
// request, for -o json and -o yaml. Without them it prints nothing.
func renderShipStop(buildError error, render func(shipResult) error, base shipResult) error {
	var stop *shipSetupPullRequestStop
	if !errors.As(buildError, &stop) {
		return nil
	}
	base.State = stop.state
	base.SetupPRURL = stop.pullRequestURL
	base.SetupPullRequestRun = stop.run
	base.BlockingFindings = stop.blockingFindings
	return render(base)
}
