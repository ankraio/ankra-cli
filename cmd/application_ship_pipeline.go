package cmd

// The Ankra Pipelines build lane of `ankra application ship` (ankra-q9j68).
// An application whose build source is ankra_pipeline - the default for new
// applications, whose setup commits .ankra/pipeline.yaml - is never built by
// a GitHub Actions workflow run, so waiting on the workflow-runs read left
// ship printing "Waiting for a workflow run" until --timeout (or latching
// onto an unrelated workflow on the branch). This lane follows the pipeline
// run for the tracked branch's head commit instead.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"
)

// shipPipelineRunAppearGrace is how long ship waits for the platform's own
// trigger to start a run for the branch head before dispatching one. A push
// webhook lands seconds after the push, so a run that has not appeared after
// this long is one no event is going to start: the head commit predates the
// application, or nothing was pushed since. A var so the tests can shorten it.
var shipPipelineRunAppearGrace = time.Minute

// shipPipelineRunProbeLimit is how many runs of the head commit one poll
// reads: enough to step past the pull request runs of the same commit.
const shipPipelineRunProbeLimit = 5

// shipPipelineTriggerPullRequest is the trigger of a run built for a pull
// request (cluster enginekit/pipelinerun.TriggerPullRequest). A pull request
// run of the head commit - a fast-forwarded merge shares it - builds from the
// pull request's point of view and need not publish, so it is not the build
// ship waits for.
const shipPipelineTriggerPullRequest = "pull_request"

// shipPipelineStepKindPublish is the step kind that pushes the image
// (cluster enginekit/pipelinerun.StepKinds).
const shipPipelineStepKindPublish = "publish"

// waitForPipelineBuild follows the Ankra Pipelines run for the tracked
// branch's head commit until the image is published, and answers the run's id
// as the build reference.
//
// The image exists once every publish step has succeeded, so ship moves on
// then rather than waiting for the steps after publishing (a verify, an
// approval gate) - the same moment the platform's deploy can use the image.
// A run without publish steps is ready when it concludes successfully. A run
// that concludes any other way before publishing fails ship, except one
// superseded by a newer run of the same commit, which ship then follows.
//
// Every poll re-reads the newest run for the commit rather than holding on to
// one id, so a run that a later push or dispatch superseded is left for the
// one that replaced it. A re-run of ship finds the run an earlier ship
// dispatched the same way, instead of dispatching another.
func waitForPipelineBuild(
	waitContext context.Context,
	progress io.Writer,
	applicationID string,
	trackedBranch string,
) (string, error) {
	headSHA, headError := shipTrackedBranchHead(waitContext, applicationID, trackedBranch)
	if headError != nil {
		return "", headError
	}
	selector := client.PipelineSelector{ApplicationID: applicationID}
	_, _ = fmt.Fprintf(progress,
		"Ankra Pipelines builds this application: waiting for the pipeline run of commit %s on branch %q.\n",
		shortShipSHA(headSHA), trackedBranch)

	appearDeadline := time.Now().Add(shipPipelineRunAppearGrace)
	dispatched := false
	announcedWaiting := false
	announcedState := ""
	for {
		page, listError := apiClient.ListPipelineRuns(waitContext, selector, client.ListPipelineRunsOptions{
			HeadSHA: headSHA,
			Limit:   shipPipelineRunProbeLimit,
		})
		if listError != nil {
			return "", shipReadError(waitContext, "reading the pipeline runs", listError)
		}
		run := newestShipPipelineRun(page)
		if run == nil {
			if !announcedWaiting {
				_, _ = fmt.Fprintf(progress,
					"No pipeline run for commit %s yet - follow them with 'ankra application pipeline list %s'.\n",
					shortShipSHA(headSHA), applicationID)
				announcedWaiting = true
			}
			if !dispatched && !time.Now().Before(appearDeadline) {
				dispatchedRun, dispatchError := apiClient.CreatePipelineRun(waitContext, selector, client.CreatePipelineRunRequest{
					Ref:     trackedBranch,
					HeadSHA: headSHA,
					Reason:  "ankra application ship: no run was started for the branch head",
				})
				if dispatchError != nil {
					return "", shipReadError(waitContext, "starting a pipeline run", dispatchError)
				}
				dispatched = true
				_, _ = fmt.Fprintf(progress,
					"Nothing started a run for commit %s, so ship dispatched run #%d.\n",
					shortShipSHA(headSHA), dispatchedRun.RunNumber)
			}
		} else {
			detail, readError := apiClient.GetPipelineRun(waitContext, selector, run.ID)
			if readError != nil {
				return "", shipReadError(waitContext, "reading the pipeline run", readError)
			}
			if detail == nil {
				return "", fmt.Errorf("reading the pipeline run: the platform answered no detail for run %s", run.ID)
			}
			state := fmt.Sprintf("#%d %s %s", detail.RunNumber, detail.Status, detail.QueueReasonMessage)
			if state != announcedState {
				line := fmt.Sprintf("Pipeline run #%d is %s", detail.RunNumber, detail.Status)
				if detail.QueueReasonMessage != "" {
					line += " - " + detail.QueueReasonMessage
				}
				_, _ = fmt.Fprintf(progress, "%s (follow it with 'ankra application pipeline get %s %s').\n",
					line, applicationID, detail.ID)
				announcedState = state
			}
			if shipPipelineImagePublished(detail) {
				_, _ = fmt.Fprintf(progress, "Ankra Pipelines built the image: run #%d (%s).\n", detail.RunNumber, detail.ID)
				return detail.ID, nil
			}
			if detail.Status == pipelineRunStatusConcluded && !pipelineRunIsSuperseded(detail.PipelineRun) {
				return "", fmt.Errorf("%w - see why with 'ankra application pipeline logs %s %s', "+
					"or re-run it with 'ankra application pipeline rerun %s %s'",
					pipelineRunConclusionError(detail.PipelineRun), applicationID, detail.ID, applicationID, detail.ID)
			}
		}
		if tickError := shipWaitTick(waitContext, shipPollInterval, "waiting for the pipeline to build the image"); tickError != nil {
			return "", tickError
		}
	}
}

// newestShipPipelineRun picks the run ship follows from a newest-first page:
// the newest one not built for a pull request.
func newestShipPipelineRun(page *client.PipelineRunList) *client.PipelineRun {
	if page == nil {
		return nil
	}
	for index := range page.Runs {
		if page.Runs[index].Trigger != shipPipelineTriggerPullRequest {
			return &page.Runs[index]
		}
	}
	return nil
}

// shipPipelineImagePublished reports whether the run has pushed its image:
// it concluded successfully, or every publish step's latest attempt
// succeeded while the run is still going. Only the latest attempt of a step
// counts, so a publish that failed once and succeeded on retry is published.
func shipPipelineImagePublished(detail *client.PipelineRunDetail) bool {
	if detail.Status == pipelineRunStatusConcluded && pipelineOptionalString(detail.Outcome) == pipelineOutcomeSuccess {
		return true
	}
	latestAttempts := map[string]client.PipelineStep{}
	for _, step := range detail.Steps {
		if step.Kind != shipPipelineStepKindPublish {
			continue
		}
		key := step.Stage + "\x00" + step.StepKey + "\x00" + string(step.Matrix)
		if current, seen := latestAttempts[key]; !seen || step.Attempt > current.Attempt {
			latestAttempts[key] = step
		}
	}
	if len(latestAttempts) == 0 {
		return false
	}
	for _, step := range latestAttempts {
		if pipelineOptionalString(step.Outcome) != pipelineOutcomeSuccess {
			return false
		}
	}
	return true
}

// shortShipSHA abbreviates a commit for progress lines.
func shortShipSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
