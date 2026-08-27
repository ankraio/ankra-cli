package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Platform builds: ask Ankra to build the application's image on its own
// builders, and follow what the builder did.
//
// The lane exists because the alternative is the repository's own CI. That
// works, and it puts the first image behind a human merging a setup PR into
// a repository whose CI Ankra can neither see nor repair. A build asked for
// here needs neither.
//
// The queue deduplicates on the commit while a request is live, so asking
// twice for the same commit joins one build instead of racing two - 'start'
// reports which of those happened rather than hiding it.

// buildPollInterval paces --wait. The lane's phases are clone, resolve,
// build and push; seconds of granularity are enough to narrate that and
// gentle on an API that other things are also using. A variable so the
// tests can drive the poll loop without sleeping through it.
var buildPollInterval = 5 * time.Second

func newApplicationBuildCommand() *cobra.Command {
	buildCommand := &cobra.Command{
		Use:     "build",
		Aliases: []string{"builds"},
		Short:   "Build an application's image on Ankra's builders",
		Long: `Build an application's image on Ankra's builders.

Ankra clones the commit, resolves a recipe for it (the repository's own
Dockerfile, else a generated one, else buildpacks), builds it, and pushes the
image to the registry the application publishes to - without the repository's
CI running at all.

The routes answer 404 for organisations without the platform_builds feature
flag, which is off by default while the lane rolls out. Ask Ankra support to
enable it for your organisation.`,
	}
	buildCommand.AddCommand(
		newApplicationBuildStartCommand(),
		newApplicationBuildListCommand(),
		newApplicationBuildGetCommand(),
		newApplicationBuildRequestCommand(),
	)
	return buildCommand
}

func newApplicationBuildStartCommand() *cobra.Command {
	startCommand := &cobra.Command{
		Use:   "start <application-id>",
		Short: "Queue a build of the application's image",
		Long: `Queue a build of the application's image.

--commit is required and must be a full 40- or 64-character commit sha. An
abbreviation is refused rather than resolved: the queue deduplicates on the
string it is given, so a full sha and its own prefix would be two keys for one
commit and build it twice.

The queue converges on the commit while a request is live. Asking twice joins
one build, and the answer's already_requested reports that it did.

With --wait the command follows the request through to the finished build and
exits non-zero if the build failed, so a pipeline step can be exactly "build
this commit, and fail if it does not build".`,
		Example: `  ankra application build start <application-id> --commit 9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c
  ankra application build start <application-id> --commit <sha> --ref main --wait
  ankra application build start <application-id> --commit <sha> --component api -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			commit, _ := command.Flags().GetString("commit")
			if strings.TrimSpace(commit) == "" {
				return withExitCode(exitUsage,
					errors.New("--commit is required: name the full commit sha to build"))
			}
			reference, _ := command.Flags().GetString("ref")
			component, _ := command.Flags().GetString("component")
			reason, _ := command.Flags().GetString("reason")
			wait, _ := command.Flags().GetBool("wait")

			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, startError := apiClient.StartApplicationPlatformBuild(command.Context(), applicationID,
				client.StartApplicationPlatformBuildRequest{
					HeadSHA:   strings.TrimSpace(commit),
					Ref:       reference,
					Component: component,
					Reason:    reason,
				})
			if startError != nil {
				return startError
			}
			if !wait {
				return renderApplicationPayload(command, payload)
			}
			return waitForApplicationBuild(command, applicationID, payload)
		},
	}
	startCommand.Flags().String("commit", "", "Full commit sha to build (required)")
	startCommand.Flags().String("ref", "", "Git ref the commit came from, recorded on the build (e.g. main)")
	startCommand.Flags().String("component", "", "Component to build, for repositories that build more than one")
	startCommand.Flags().String("reason", "", "Why the build was asked for: first_onboard, push, retry (default) or demo")
	startCommand.Flags().Bool("wait", false, "Follow the build until it finishes; exit non-zero if it failed")
	startCommand.Flags().Duration("timeout", 30*time.Minute, "How long --wait waits before giving up on the build")
	registerStructuredOutputFlags(startCommand)
	return startCommand
}

func newApplicationBuildListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list <application-id>",
		Short:   "List the application's platform builds, newest first",
		Example: "  ankra application build list <application-id> -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, listError := apiClient.ListApplicationPlatformBuilds(command.Context(), applicationID)
			if listError != nil {
				return listError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationBuildGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application-id> <build-id>",
		Short: "Show one platform build",
		Long: `Show one platform build.

A failed build carries an error_class saying whose failure it was. build_failed
is the repository's - the recipe did not build - and is the one worth reading
the error_message for. clone_auth and recipe_missing are the application's
configuration. push_failed, timeout and capacity are Ankra's, and are already
visible to Ankra without anyone reporting them.`,
		Example: "  ankra application build get <application-id> <build-id>",
		Args:    cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments[:1])
			if resolveError != nil {
				return resolveError
			}
			payload, readError := apiClient.GetApplicationPlatformBuild(command.Context(), applicationID, arguments[1])
			if readError != nil {
				return readError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationBuildRequestCommand() *cobra.Command {
	requestCommand := &cobra.Command{
		Use:   "request <application-id> <request-id>",
		Short: "Show a queued build request and the build it became",
		Long: `Show a queued build request and the build it became.

'start' answers with a request id before there is a build to read: the build
row is created when the scheduler claims the request. This is how a caller
follows the gap - status is pending until it is claimed, and build_id names
the build from that point on.`,
		Example: "  ankra application build request <application-id> <request-id>",
		Args:    cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments[:1])
			if resolveError != nil {
				return resolveError
			}
			payload, readError := apiClient.GetApplicationPlatformBuildRequest(command.Context(), applicationID, arguments[1])
			if readError != nil {
				return readError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(requestCommand)
	return requestCommand
}

// startedBuild is the shape of the start answer this command follows.
type startedBuild struct {
	RequestID        string  `json:"request_id"`
	BuildID          *string `json:"build_id"`
	AlreadyRequested bool    `json:"already_requested"`
}

// queuedBuildRequest is the request read --wait polls across the gap between
// a queued request and the build it becomes.
type queuedBuildRequest struct {
	Status  string  `json:"status"`
	BuildID *string `json:"build_id"`
}

// followedBuild is the build read --wait polls until the lane concludes.
type followedBuild struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	Recipe       *string `json:"recipe"`
	ImageRef     *string `json:"image_ref"`
	ErrorClass   *string `json:"error_class"`
	ErrorMessage *string `json:"error_message"`
}

// waitForApplicationBuild follows a queued request to a finished build.
//
// Two polls, not one, because a request and a build are different rows: the
// request exists from the moment it is queued, the build only once the
// scheduler claims it. Waiting on the listing instead would be wrong in a way
// that is easy to miss - a previous failed build of the same commit is still
// the newest one for that commit, so a naive watcher would report the old
// failure as this ask's result.
//
// A build that fails exits 1: for the pipeline step this flag exists for,
// "the build failed" is a failure of the command, not news it reports and
// then succeeds anyway.
func waitForApplicationBuild(command *cobra.Command, applicationID string, startPayload json.RawMessage) error {
	var started startedBuild
	if unmarshalError := json.Unmarshal(startPayload, &started); unmarshalError != nil {
		return fmt.Errorf("reading the queued build: %w", unmarshalError)
	}
	timeout, timeoutFlagError := command.Flags().GetDuration("timeout")
	if timeoutFlagError != nil {
		return fmt.Errorf("reading --timeout: %w", timeoutFlagError)
	}
	// Progress goes to stderr so -o json stdout stays parseable.
	errorOutput := command.ErrOrStderr()
	if started.AlreadyRequested {
		_, _ = fmt.Fprintf(errorOutput, "Joined the build already queued for this commit (request %s).\n", started.RequestID)
	} else {
		_, _ = fmt.Fprintf(errorOutput, "Queued build request %s.\n", started.RequestID)
	}

	waitContext, cancelWait := context.WithTimeout(command.Context(), timeout)
	defer cancelWait()

	buildID, resolveError := awaitClaimedBuild(waitContext, errorOutput, applicationID, started)
	if resolveError != nil {
		return resolveError
	}
	finished, followError := awaitFinishedBuild(waitContext, errorOutput, applicationID, buildID)
	if followError != nil {
		return followError
	}
	if renderError := renderApplicationPayload(command, finished.payload); renderError != nil {
		return renderError
	}
	if finished.build.Status != "succeeded" {
		return withExitCode(exitError, describeFailedBuild(finished.build))
	}
	return nil
}

// awaitClaimedBuild polls the request until the scheduler claims it and names
// a build, or the request is cancelled.
func awaitClaimedBuild(waitContext context.Context, errorOutput interface{ Write([]byte) (int, error) },
	applicationID string, started startedBuild) (string, error) {
	if started.BuildID != nil && *started.BuildID != "" {
		return *started.BuildID, nil
	}
	announced := false
	for {
		payload, readError := apiClient.GetApplicationPlatformBuildRequest(waitContext, applicationID, started.RequestID)
		if readError != nil {
			return "", waitReadError(waitContext, "following the build request", readError)
		}
		var queued queuedBuildRequest
		if unmarshalError := json.Unmarshal(payload, &queued); unmarshalError != nil {
			return "", fmt.Errorf("reading the build request: %w", unmarshalError)
		}
		if queued.BuildID != nil && *queued.BuildID != "" {
			_, _ = fmt.Fprintf(errorOutput, "Builder claimed the request (build %s).\n", *queued.BuildID)
			return *queued.BuildID, nil
		}
		if queued.Status == "cancelled" {
			return "", withExitCode(exitError,
				fmt.Errorf("the build request was cancelled before a builder claimed it (request %s)", started.RequestID))
		}
		if !announced {
			_, _ = fmt.Fprintln(errorOutput, "Waiting for a builder to claim the request...")
			announced = true
		}
		if sleepError := waitTick(waitContext, "waiting for a builder to claim the request"); sleepError != nil {
			return "", sleepError
		}
	}
}

type finishedBuild struct {
	build   followedBuild
	payload json.RawMessage
}

// awaitFinishedBuild polls one build until it succeeds or fails.
func awaitFinishedBuild(waitContext context.Context, errorOutput interface{ Write([]byte) (int, error) },
	applicationID string, buildID string) (finishedBuild, error) {
	announcedRunning := false
	for {
		payload, readError := apiClient.GetApplicationPlatformBuild(waitContext, applicationID, buildID)
		if readError != nil {
			return finishedBuild{}, waitReadError(waitContext, "following the build", readError)
		}
		var build followedBuild
		if unmarshalError := json.Unmarshal(payload, &build); unmarshalError != nil {
			return finishedBuild{}, fmt.Errorf("reading the build: %w", unmarshalError)
		}
		if build.Status == "succeeded" || build.Status == "failed" {
			return finishedBuild{build: build, payload: payload}, nil
		}
		if build.Status == "running" && !announcedRunning {
			recipe := "resolving the recipe"
			if build.Recipe != nil && *build.Recipe != "" {
				recipe = "recipe: " + *build.Recipe
			}
			_, _ = fmt.Fprintf(errorOutput, "Building (%s)...\n", recipe)
			announcedRunning = true
		}
		if sleepError := waitTick(waitContext, "waiting for the build to finish"); sleepError != nil {
			return finishedBuild{}, sleepError
		}
	}
}

// waitTick sleeps one poll interval, turning an expired budget into the
// scripting contract's wait-timeout exit rather than a bare context error.
func waitTick(waitContext context.Context, what string) error {
	timer := time.NewTimer(buildPollInterval)
	defer timer.Stop()
	select {
	case <-waitContext.Done():
		return withExitCode(exitWaitTimeout,
			fmt.Errorf("--timeout expired %s; the build keeps running - follow it with 'ankra application build get'", what))
	case <-timer.C:
		return nil
	}
}

// waitReadError distinguishes an expired --timeout budget from a genuine API
// failure: both surface as an error from the poll, and only one of them means
// the platform said no.
func waitReadError(waitContext context.Context, what string, readError error) error {
	if waitContext.Err() != nil {
		return withExitCode(exitWaitTimeout,
			fmt.Errorf("--timeout expired while %s; the build keeps running - follow it with 'ankra application build get'", what))
	}
	return fmt.Errorf("%s: %w", what, readError)
}

// describeFailedBuild names whose failure it was, because the error classes
// split cleanly and the split is the useful part: build_failed is the
// repository's to fix, the platform classes are Ankra's and are already
// visible to Ankra.
func describeFailedBuild(build followedBuild) error {
	errorClass := ""
	if build.ErrorClass != nil {
		errorClass = *build.ErrorClass
	}
	message := ""
	if build.ErrorMessage != nil {
		message = strings.TrimSpace(*build.ErrorMessage)
	}
	switch errorClass {
	case "build_failed":
		if message != "" {
			return fmt.Errorf("the build failed: %s", message)
		}
		return errors.New("the build failed: the recipe did not build")
	case "clone_auth":
		return fmt.Errorf("the builder could not clone the repository: %s", fallbackMessage(message,
			"check the application's repository credential"))
	case "recipe_missing":
		return fmt.Errorf("no recipe could be resolved for this commit: %s", fallbackMessage(message,
			"the repository has no Dockerfile and no buildpack matched"))
	case "push_failed", "timeout", "capacity":
		return fmt.Errorf("the build did not finish (%s): %s", errorClass, fallbackMessage(message,
			"this is a platform-side failure and Ankra can see it"))
	}
	return fmt.Errorf("the build failed: %s", fallbackMessage(message, "no reason was recorded"))
}

func fallbackMessage(message string, fallback string) string {
	if message == "" {
		return fallback
	}
	return message
}
