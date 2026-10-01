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

// shipPipelineRunProbeLimit is how many runs of the head commit one page
// reads, and shipPipelineRunMaxPages how many pages one poll walks looking
// past pull request runs of the same commit. A poll that walks them all
// without an answer has not proved there is no run, so it never dispatches.
const (
	shipPipelineRunProbeLimit = 20
	shipPipelineRunMaxPages   = 5
)

// shipPipelineTriggerPullRequest is the trigger of a run built for a pull
// request (cluster enginekit/pipelinerun.TriggerPullRequest). A pull request
// run of the head commit - a fast-forwarded merge shares it - builds from the
// pull request's point of view and need not publish, so it is not the build
// ship waits for.
const shipPipelineTriggerPullRequest = "pull_request"

// shipPipelineTriggerPush is the trigger of a push run (cluster
// enginekit/pipelinerun.TriggerPush), which is what ship's own dispatch is
// recorded as on a platform that runs a branch's push on request.
const shipPipelineTriggerPush = "push"

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
// whose publish step was skipped never pushes an image, however it ends, and
// fails ship rather than deploying an image that was never built.
//
// When nothing starts a run for the head commit - the application was
// registered after the branch's last push, so no push event will ever come -
// ship asks the platform for the branch's push itself (ankra-z3h6u): the run
// the push webhook would have started, which publishes the image and makes it
// the application's deployment candidate. A platform from before that
// records a manual run instead, whose publish the generated pipeline skips,
// and ship then says to push a commit. A run that concludes any other way before publishing fails ship,
// except a superseded one: ship then follows the run that replaced it, and
// when the replacement is for a newer push it moves to the branch's new head.
//
// Every poll re-reads the newest run for the commit rather than holding on to
// one id, so a run that a later dispatch superseded is left for the one that
// replaced it. A re-run of ship finds the run an earlier ship dispatched the
// same way, instead of dispatching another.
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
		run, listedAll, listError := findShipPipelineRun(waitContext, selector, headSHA)
		if listError != nil {
			return "", shipReadError(waitContext, "reading the pipeline runs", listError)
		}
		if run == nil {
			if !announcedWaiting {
				_, _ = fmt.Fprintf(progress,
					"No pipeline run for commit %s yet - follow them with 'ankra application pipeline list %s'.\n",
					shortShipSHA(headSHA), applicationID)
				announcedWaiting = true
			}
			if !dispatched && listedAll && !time.Now().Before(appearDeadline) {
				// The run asked for is the branch's push, not a manual run:
				// the generated pipeline publishes only on a push, and the
				// image's deployment candidate follows push runs alone, so a
				// manual run would build an image nothing deploys (ankra-z3h6u).
				dispatchedRun, dispatchError := apiClient.CreatePipelineRun(waitContext, selector, client.CreatePipelineRunRequest{
					Ref:     trackedBranch,
					HeadSHA: headSHA,
					Reason:  "ankra application ship: no run was started for the branch head",
					Event:   client.PipelineDispatchEventPush,
				})
				if dispatchError != nil {
					// The branch can move between reading its head and
					// asking for its push, which the platform refuses
					// because a push names the current head. Follow the
					// branch then, exactly as for a superseded run. The
					// refusal is not matched by status or text, so it is
					// printed: a different failure that coincided with a
					// push stays visible, and recurs on the new head.
					currentHead, headError := shipTrackedBranchHead(waitContext, applicationID, trackedBranch)
					if headError == nil && currentHead != "" && !strings.EqualFold(currentHead, headSHA) {
						_, _ = fmt.Fprintf(progress,
							"Starting the push run of commit %s failed (%v), and branch %q has moved to commit %s, so ship follows that commit.\n",
							shortShipSHA(headSHA), dispatchError, trackedBranch, shortShipSHA(currentHead))
						headSHA = currentHead
						appearDeadline = time.Now().Add(shipPipelineRunAppearGrace)
						announcedWaiting = false
						announcedState = ""
						continue
					}
					return "", shipReadError(waitContext, "starting a pipeline run", dispatchError)
				}
				dispatched = true
				_, _ = fmt.Fprintf(progress,
					"Nothing started a run for commit %s, so ship started run #%d as the push of branch %q.\n",
					shortShipSHA(headSHA), dispatchedRun.RunNumber, trackedBranch)
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
			if detail.Status == pipelineRunStatusConcluded && pipelineRunIsSuperseded(detail.PipelineRun) {
				// A newer push cancels the older commit's run. Its replacement
				// is a run of the new head, which the listing for this commit
				// never returns, so follow the branch rather than wait on a
				// run that is over.
				currentHead, headError := shipTrackedBranchHead(waitContext, applicationID, trackedBranch)
				if headError != nil {
					return "", headError
				}
				if currentHead != "" && !strings.EqualFold(currentHead, headSHA) {
					_, _ = fmt.Fprintf(progress,
						"Pipeline run #%d was superseded %s: branch %q moved to commit %s, so ship follows that commit.\n",
						detail.RunNumber, pipelineRunSupersessionPhrase(detail.PipelineRun), trackedBranch,
						shortShipSHA(currentHead))
					headSHA = currentHead
					appearDeadline = time.Now().Add(shipPipelineRunAppearGrace)
					dispatched = false
					announcedWaiting = false
					announcedState = ""
				}
			} else if detail.Status == pipelineRunStatusConcluded &&
				pipelineOptionalString(detail.Outcome) != pipelineOutcomeSuccess {
				return "", fmt.Errorf("%w - see why with 'ankra application pipeline logs %s %s', "+
					"or re-run it with 'ankra application pipeline rerun %s %s'",
					pipelineRunConclusionError(detail.PipelineRun), applicationID, detail.ID, applicationID, detail.ID)
			} else if skipped := shipPipelineSkippedPublish(detail); skipped != nil {
				reason := "its publish step was skipped"
				if skipped.ErrorMessage != nil && strings.TrimSpace(*skipped.ErrorMessage) != "" {
					reason = strings.TrimSpace(*skipped.ErrorMessage)
				}
				if dispatched && detail.Trigger != shipPipelineTriggerPush {
					// Ship asked for the branch's push and the platform
					// recorded something else: it predates running a push
					// on request, and its manual run cannot publish.
					return "", fmt.Errorf("pipeline run #%d does not publish the image (%s): this platform "+
						"cannot yet run the branch's push on request, so push a commit to %q - the platform "+
						"then starts a run that publishes - and run ship again, or re-run ship with --ankra-build; "+
						"follow the run with 'ankra application pipeline get %s %s'",
						detail.RunNumber, reason, trackedBranch, applicationID, detail.ID)
				}
				return "", fmt.Errorf("pipeline run #%d does not publish the image (%s) - push a commit to %q "+
					"so the platform starts a run that publishes, then run ship again; "+
					"follow the run with 'ankra application pipeline get %s %s'",
					detail.RunNumber, reason, trackedBranch, applicationID, detail.ID)
			} else if detail.Status == pipelineRunStatusConcluded {
				return "", fmt.Errorf("pipeline run #%d concluded without publishing the image - see why with "+
					"'ankra application pipeline logs %s %s'", detail.RunNumber, applicationID, detail.ID)
			}
		}
		if tickError := shipWaitTick(waitContext, shipPollInterval, "waiting for the pipeline to build the image"); tickError != nil {
			return "", tickError
		}
	}
}

// findShipPipelineRun answers the newest run of the head commit that was not
// built for a pull request, walking the newest-first listing a page at a
// time. listedAll reports whether the walk reached the end of the listing,
// which is what "there is no such run" needs: a walk cut short by the page
// bound has only seen pull request runs so far.
func findShipPipelineRun(waitContext context.Context, selector client.PipelineSelector,
	headSHA string) (*client.PipelineRun, bool, error) {
	options := client.ListPipelineRunsOptions{HeadSHA: headSHA, Limit: shipPipelineRunProbeLimit}
	for pageNumber := 0; pageNumber < shipPipelineRunMaxPages; pageNumber++ {
		page, listError := apiClient.ListPipelineRuns(waitContext, selector, options)
		if listError != nil {
			return nil, false, listError
		}
		if run := newestShipPipelineRun(page); run != nil {
			return run, true, nil
		}
		if page == nil || page.NextCursor == nil || *page.NextCursor == "" {
			return nil, true, nil
		}
		options.Cursor = *page.NextCursor
	}
	return nil, false, nil
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
// every publish step's latest attempt succeeded, whether or not the run is
// still going, or - for a run with no publish step at all - the run concluded
// successfully. A run whose publish was skipped also concludes successfully
// when everything else passed, so the run's outcome alone is never taken as a
// published image while it has publish steps. Only the latest attempt of a
// step counts, so a publish that failed once and succeeded on retry is
// published.
func shipPipelineImagePublished(detail *client.PipelineRunDetail) bool {
	latestAttempts := shipPipelinePublishAttempts(detail)
	if len(latestAttempts) == 0 {
		return detail.Status == pipelineRunStatusConcluded &&
			pipelineOptionalString(detail.Outcome) == pipelineOutcomeSuccess
	}
	for _, step := range latestAttempts {
		if pipelineOptionalString(step.Outcome) != pipelineOutcomeSuccess {
			return false
		}
	}
	return true
}

// shipPipelineSkippedPublish answers a publish step whose latest attempt was
// skipped, or nil. The planner skips a stage whose event, branch or path
// filter does not match before the run starts, so such a run never pushes an
// image however long ship waits.
func shipPipelineSkippedPublish(detail *client.PipelineRunDetail) *client.PipelineStep {
	for _, step := range shipPipelinePublishAttempts(detail) {
		if pipelineOptionalString(step.Outcome) == pipelineOutcomeSkipped {
			skipped := step
			return &skipped
		}
	}
	return nil
}

// shipPipelinePublishAttempts answers the latest attempt of every publish
// step in the run, keyed by stage, step key and matrix leg.
func shipPipelinePublishAttempts(detail *client.PipelineRunDetail) map[string]client.PipelineStep {
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
	return latestAttempts
}

// shortShipSHA abbreviates a commit for progress lines.
func shortShipSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
