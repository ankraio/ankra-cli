package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// `ankra application ship` composes the lanes that already exist as separate
// subcommands - add, get, workflow-runs, build, deploy, installations - into
// the one sentence they were built for: take this checkout to a live
// deployment.
//
// The command is a state machine over platform reads, not a script of writes:
// every step first reads where the flow actually is and only acts on what is
// missing. That is what makes re-running it safe - a ship interrupted at any
// point resumes from the current state instead of registering, building or
// deploying twice.

// shipPollInterval paces the setup, build and installation polls;
// shipMergePollInterval paces the human-speed merge gate. Variables so the
// tests can drive the loops without sleeping through them.
var (
	shipPollInterval      = 5 * time.Second
	shipMergePollInterval = 15 * time.Second
)

// shipResult is the -o json document: the identities a script needs to keep
// driving the application afterwards.
type shipResult struct {
	ApplicationID    string `json:"application_id" yaml:"application_id"`
	ApplicationName  string `json:"application_name" yaml:"application_name"`
	Repository       string `json:"repository" yaml:"repository"`
	Branch           string `json:"branch" yaml:"branch"`
	Registered       bool   `json:"registered" yaml:"registered"`
	SetupPRURL       string `json:"setup_pr_url,omitempty" yaml:"setup_pr_url,omitempty"`
	BuildRef         string `json:"build_ref,omitempty" yaml:"build_ref,omitempty"`
	OrganisationName string `json:"organisation_name" yaml:"organisation_name"`
	ClusterID        string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName      string `json:"cluster_name" yaml:"cluster_name"`
	Namespace        string `json:"namespace" yaml:"namespace"`
	InstallationID   string `json:"installation_id,omitempty" yaml:"installation_id,omitempty"`
	URL              string `json:"url,omitempty" yaml:"url,omitempty"`
	State            string `json:"state" yaml:"state"`
}

// shipApplicationView is the slice of the application detail (and of each
// listing row - the fields are shared) the ship flow steers by.
type shipApplicationView struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	State               string  `json:"state"`
	ErrorMessage        *string `json:"error_message"`
	AppRepoOwner        string  `json:"app_repo_owner"`
	AppRepoName         string  `json:"app_repo_name"`
	AppRepoBranch       string  `json:"app_repo_branch"`
	PullRequestURL      *string `json:"pull_request_url"`
	PullRequestMergedAt *string `json:"pull_request_merged_at"`
}

func newApplicationShipCommand() *cobra.Command {
	shipCommand := &cobra.Command{
		Use:   "ship [path]",
		Short: "Take a local checkout to a live deployment in one command",
		Long: `Take a local checkout to a live deployment in one command.

Ship composes the existing application lanes: it registers the repository as
an application when the organisation does not have it yet (with exactly the
'application add' flags), waits for the setup analysis, walks you through the
setup pull request merge gate, waits for a green image build of the tracked
branch, deploys to the target cluster, and follows the installation until the
workload is running - then prints the published URL when one exists.

Every wait step says what it is waiting on and where to look. Re-running ship
is safe at any point: it re-reads where the flow actually is and continues
from there, so an interrupted or timed-out run is resumed, not repeated.

With --ankra-build the first image is built on Ankra's builders from the head
commit of the tracked branch, so ship does not wait for the setup pull
request to merge or for the repository's own CI. The platform-build routes
answer 404 for organisations without the platform_builds feature flag.

The image tag and ingress publication are the platform's defaults: the latest
green build is deployed, and ingress is derived server-side. Pass --set only
for deploy inputs you want to pin.`,
		Example: `  ankra application ship .
  ankra application ship . --cluster production
  ankra application ship ./services/payments --cluster staging --namespace payments
  ankra application ship . --cluster staging --ankra-build --set replicas=2 -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: runApplicationShip,
	}
	registerApplicationAddFlags(shipCommand)
	shipCommand.Flags().String("cluster", "",
		"Target cluster name or ID (defaults to the selected cluster, echoed before acting)")
	shipCommand.Flags().String("namespace", "",
		"Target namespace (defaults to the namespace the platform derives from the application name)")
	shipCommand.Flags().StringArray("set", nil, "Deploy input as key=value (repeatable)")
	shipCommand.Flags().Bool("ankra-build", false,
		"Build the first image on Ankra's builders instead of waiting for the setup PR merge and repository CI")
	shipCommand.Flags().Duration("timeout", time.Hour, "Overall budget for every wait step combined")
	registerStructuredOutputFlags(shipCommand)
	return shipCommand
}

func runApplicationShip(command *cobra.Command, arguments []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	setValues, _ := command.Flags().GetStringArray("set")
	deployInputs, parseError := parseKeyValueFlags(setValues)
	if parseError != nil {
		return withExitCode(exitUsage, parseError)
	}
	useAnkraBuild, _ := command.Flags().GetBool("ankra-build")
	namespaceFlag, _ := command.Flags().GetString("namespace")
	namespaceFlag = strings.TrimSpace(namespaceFlag)
	timeout, timeoutFlagError := command.Flags().GetDuration("timeout")
	if timeoutFlagError != nil {
		return fmt.Errorf("reading --timeout: %w", timeoutFlagError)
	}
	repositoryPath := "."
	if len(arguments) == 1 {
		repositoryPath = arguments[0]
	}

	// The organisation and cluster are resolved and echoed before anything
	// acts: a --org override or a stale `ankra cluster select` retargets every
	// write below, and naming both is the defence against shipping into the
	// wrong place.
	organisation, _, organisationError := resolveTargetOrganisation(command)
	if organisationError != nil {
		return organisationError
	}
	organisationName := organisation.OrganisationID
	if organisation.Name != nil && strings.TrimSpace(*organisation.Name) != "" {
		organisationName = *organisation.Name
	}
	cluster, clusterError := resolveActiveCluster(command)
	if clusterError != nil {
		return clusterError
	}
	if cluster.OrganisationID != "" && organisation.OrganisationID != "" &&
		cluster.OrganisationID != organisation.OrganisationID {
		return withExitCode(exitUsage, fmt.Errorf(
			"the selected cluster %q belongs to a different organisation than %q; "+
				"run 'ankra cluster select' or pass --cluster", cluster.Name, organisationName))
	}

	// Progress goes to stderr so -o json stdout stays parseable.
	progress := command.ErrOrStderr()
	_, _ = fmt.Fprintf(progress, "Shipping to organisation %q, cluster %q.\n", organisationName, cluster.Name)

	remoteName, _ := command.Flags().GetString("remote")
	branchOverride, _ := command.Flags().GetString("branch")
	branchOverride = strings.TrimSpace(branchOverride)
	if command.Flags().Changed("branch") && branchOverride == "" {
		return withExitCode(exitUsage, errors.New("branch cannot be empty"))
	}
	repository, repositoryError := inspectLocalApplicationRepository(
		command.Context(), repositoryPath, strings.TrimSpace(remoteName), branchOverride)
	if repositoryError != nil {
		return repositoryError
	}

	waitContext, cancelWait := context.WithTimeout(command.Context(), timeout)
	defer cancelWait()

	application, registered, resolveError := resolveOrRegisterApplication(
		waitContext, command, progress, repositoryPath, repository)
	if resolveError != nil {
		return resolveError
	}

	application, setupError := waitForApplicationSetup(waitContext, progress, application.ID)
	if setupError != nil {
		return setupError
	}
	trackedBranch := application.AppRepoBranch
	if trackedBranch == "" {
		trackedBranch = repository.Branch
	}
	setupPRURL := ""
	if application.PullRequestURL != nil {
		setupPRURL = *application.PullRequestURL
	}

	var buildReference string
	if useAnkraBuild {
		var buildError error
		buildReference, buildError = runShipPlatformBuild(waitContext, command, progress, application.ID, trackedBranch)
		if buildError != nil {
			return buildError
		}
	} else {
		if mergeError := waitForSetupPullRequestMerge(waitContext, progress, application.ID, application); mergeError != nil {
			return mergeError
		}
		var workflowError error
		buildReference, workflowError = waitForWorkflowSuccess(waitContext, progress, application.ID, trackedBranch)
		if workflowError != nil {
			return workflowError
		}
	}

	namespace := namespaceFlag
	if namespace == "" {
		namespace = defaultShipNamespace(application.Name)
	}
	installationID, deployMessage, deployError := ensureApplicationDeployed(
		waitContext, progress, application.ID, cluster.ID, namespace, deployInputs)
	if deployError != nil {
		return deployError
	}

	if verifyError := waitForInstallationHealthy(
		waitContext, progress, application.ID, installationID, cluster.ID, namespace); verifyError != nil {
		return verifyError
	}
	reportNamespacePods(progress, cluster.ID, namespace)

	liveURL := findPublishedApplicationURL(waitContext, progress, application.ID, cluster.ID, namespace)

	result := shipResult{
		ApplicationID:    application.ID,
		ApplicationName:  application.Name,
		Repository:       repository.Owner + "/" + repository.Name,
		Branch:           trackedBranch,
		Registered:       registered,
		SetupPRURL:       setupPRURL,
		BuildRef:         buildReference,
		OrganisationName: organisationName,
		ClusterID:        cluster.ID,
		ClusterName:      cluster.Name,
		Namespace:        namespace,
		InstallationID:   installationID,
		URL:              liveURL,
		State:            "healthy",
	}
	if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
		return renderError
	}

	output := command.OutOrStdout()
	if liveURL != "" {
		_, _ = fmt.Fprintf(output, "Live: %s\n", liveURL)
		return nil
	}
	_, _ = fmt.Fprintf(output,
		"Deployed to cluster %q, namespace %q - no published URL yet (no Ingress with a hostname was found in the namespace).\n",
		cluster.Name, namespace)
	if deployMessage != "" {
		_, _ = fmt.Fprintln(output, deployMessage)
	}
	return nil
}

// shipWaitTick sleeps one poll interval, turning an expired --timeout budget
// into the wait-timeout exit with the resumable hint - re-running ship is the
// designed recovery, so every expiry says so.
func shipWaitTick(waitContext context.Context, interval time.Duration, waitingOn string) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-waitContext.Done():
		return withExitCode(exitWaitTimeout, fmt.Errorf(
			"--timeout expired %s; nothing is lost - re-run 'ankra application ship' and it continues from the current state",
			waitingOn))
	case <-timer.C:
		return nil
	}
}

// shipReadError distinguishes an expired --timeout budget from a genuine API
// failure surfaced by a poll read.
func shipReadError(waitContext context.Context, what string, readError error) error {
	if waitContext.Err() != nil {
		return withExitCode(exitWaitTimeout, fmt.Errorf(
			"--timeout expired while %s; re-run 'ankra application ship' to continue from the current state", what))
	}
	return fmt.Errorf("%s: %w", what, readError)
}

// shipApplicationListingPage is the slice of the applications listing the
// repository lookup reads.
type shipApplicationListingPage struct {
	Result     []shipApplicationView `json:"result"`
	Pagination struct {
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

// findApplicationByRepository walks the applications listing for one whose
// repository matches the local checkout. The listing's `search` filter only
// matches names, so the walk is unfiltered; the same page bounds as
// resolveApplicationID keep it finite, and a walk that does not finish is an
// error rather than an answer from partial data.
func findApplicationByRepository(
	requestContext context.Context,
	repository localApplicationRepository,
) (*shipApplicationView, error) {
	matches := []shipApplicationView{}
	listingExhausted := false
	for page := 1; page <= maxApplicationLookupPages; page++ {
		payload, listError := apiClient.ListApplicationsRaw(
			requestContext, page, maxApplicationLookupPageSize, "")
		if listError != nil {
			return nil, fmt.Errorf("listing applications: %w", listError)
		}
		var listing shipApplicationListingPage
		if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
			return nil, fmt.Errorf("reading the applications listing: %w", unmarshalError)
		}
		for _, application := range listing.Result {
			if strings.EqualFold(application.AppRepoOwner, repository.Owner) &&
				strings.EqualFold(application.AppRepoName, repository.Name) {
				matches = append(matches, application)
			}
		}
		if listing.Pagination.TotalPages <= page || len(listing.Result) == 0 {
			listingExhausted = true
			break
		}
	}
	if !listingExhausted {
		return nil, fmt.Errorf(
			"the applications listing did not end within %d pages; cannot decide whether %s/%s is already registered",
			maxApplicationLookupPages, repository.Owner, repository.Name)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	// Several applications can track the same repository on different
	// branches; the one tracking this checkout's branch is the one meant.
	for index := range matches {
		if strings.EqualFold(matches[index].AppRepoBranch, repository.Branch) {
			return &matches[index], nil
		}
	}
	return &matches[0], nil
}

// resolveOrRegisterApplication finds the application already registered for
// the checkout's repository, or runs the add flow. It prints which happened.
func resolveOrRegisterApplication(
	requestContext context.Context,
	command *cobra.Command,
	progress io.Writer,
	repositoryPath string,
	repository localApplicationRepository,
) (shipApplicationView, bool, error) {
	existing, lookupError := findApplicationByRepository(requestContext, repository)
	if lookupError != nil {
		return shipApplicationView{}, false, lookupError
	}
	if existing != nil {
		_, _ = fmt.Fprintf(progress, "Using existing application %q (%s) for %s/%s.\n",
			existing.Name, existing.ID, repository.Owner, repository.Name)
		warnIgnoredRegistrationFlags(command, progress)
		if !strings.EqualFold(existing.AppRepoBranch, repository.Branch) && existing.AppRepoBranch != "" {
			_, _ = fmt.Fprintf(progress,
				"Note: the application tracks branch %q; the checkout's branch %q does not retarget it.\n",
				existing.AppRepoBranch, repository.Branch)
		}
		return *existing, false, nil
	}

	plan, planError := resolveApplicationAddPlan(command, repositoryPath)
	if planError != nil {
		return shipApplicationView{}, false, planError
	}
	created, createError := createApplicationFromPlan(requestContext, plan)
	if createError != nil {
		return shipApplicationView{}, false, createError
	}
	_, _ = fmt.Fprintf(progress, "Registered application %q (%s) for %s/%s@%s with credential %q.\n",
		created.Name, created.ID, plan.repository.Owner, plan.repository.Name,
		plan.repository.Branch, plan.credential.Name)
	return shipApplicationView{
		ID:            created.ID,
		Name:          created.Name,
		AppRepoOwner:  plan.repository.Owner,
		AppRepoName:   plan.repository.Name,
		AppRepoBranch: plan.repository.Branch,
	}, true, nil
}

// warnIgnoredRegistrationFlags says out loud which explicitly-passed add
// flags did nothing because the repository already has an application: they
// shape how an application is REGISTERED, and silently ignoring an explicit
// flag is how a caller comes to believe a re-run retargeted something it did
// not.
func warnIgnoredRegistrationFlags(command *cobra.Command, progress io.Writer) {
	registrationFlags := append([]string{"name", "credential", "registry-url"}, applicationAddRegistryFlags...)
	ignored := []string{}
	for _, flagName := range registrationFlags {
		if command.Flags().Changed(flagName) {
			ignored = append(ignored, "--"+flagName)
		}
	}
	if len(ignored) == 0 {
		return
	}
	_, _ = fmt.Fprintf(progress,
		"Note: %s only apply when ship registers the application, and this repository already has one - they were ignored. "+
			"Change the existing application with 'ankra application credential set' or 'ankra application registry set'.\n",
		strings.Join(ignored, ", "))
}

// shipSetupFailureStates are the application states that mean setup is not
// going to reach `up` without intervention.
var shipSetupFailureStates = map[string]bool{"error": true, "down": true, "failed": true}

// waitForApplicationSetup polls the application detail until the setup
// analysis lands the application in state `up`.
func waitForApplicationSetup(
	waitContext context.Context,
	progress io.Writer,
	applicationID string,
) (shipApplicationView, error) {
	announcedState := ""
	announcedPullRequest := false
	for {
		payload, readError := apiClient.GetApplicationRaw(waitContext, applicationID)
		if readError != nil {
			return shipApplicationView{}, shipReadError(waitContext, "reading the application", readError)
		}
		var application shipApplicationView
		if unmarshalError := json.Unmarshal(payload, &application); unmarshalError != nil {
			return shipApplicationView{}, fmt.Errorf("reading the application: %w", unmarshalError)
		}
		if application.PullRequestURL != nil && *application.PullRequestURL != "" && !announcedPullRequest {
			_, _ = fmt.Fprintf(progress, "Setup pull request: %s\n", *application.PullRequestURL)
			announcedPullRequest = true
		}
		if application.State == "up" {
			_, _ = fmt.Fprintf(progress, "Application setup is complete (state: up).\n")
			return application, nil
		}
		if shipSetupFailureStates[application.State] {
			message := "no error message was recorded"
			if application.ErrorMessage != nil && strings.TrimSpace(*application.ErrorMessage) != "" {
				message = *application.ErrorMessage
			}
			return shipApplicationView{}, fmt.Errorf(
				"application setup failed (state: %s): %s - fix the cause, then 'ankra application retry %s'",
				application.State, message, applicationID)
		}
		if application.State != announcedState {
			_, _ = fmt.Fprintf(progress,
				"Waiting for application setup (state: %s) - follow it with 'ankra application get %s'.\n",
				application.State, applicationID)
			announcedState = application.State
		}
		if tickError := shipWaitTick(waitContext, shipPollInterval, "waiting for application setup"); tickError != nil {
			return shipApplicationView{}, tickError
		}
	}
}

// waitForSetupPullRequestMerge is the merge gate: the generated build
// workflow only starts building images once the setup pull request lands on
// the tracked branch, and merging it is a human's decision - so the gate says
// exactly what is being waited on and polls at human speed.
func waitForSetupPullRequestMerge(
	waitContext context.Context,
	progress io.Writer,
	applicationID string,
	application shipApplicationView,
) error {
	if application.PullRequestURL == nil || *application.PullRequestURL == "" {
		return nil
	}
	if application.PullRequestMergedAt != nil && *application.PullRequestMergedAt != "" {
		return nil
	}
	_, _ = fmt.Fprintf(progress,
		"Waiting for the setup pull request to be merged:\n  %s\nMerge it to let the repository's CI build the first image (or re-run with --ankra-build to build on Ankra's builders instead).\n",
		*application.PullRequestURL)
	for {
		if tickError := shipWaitTick(waitContext, shipMergePollInterval,
			"waiting for the setup pull request to be merged"); tickError != nil {
			return tickError
		}
		payload, readError := apiClient.GetApplicationRaw(waitContext, applicationID)
		if readError != nil {
			return shipReadError(waitContext, "checking the setup pull request", readError)
		}
		var current shipApplicationView
		if unmarshalError := json.Unmarshal(payload, &current); unmarshalError != nil {
			return fmt.Errorf("checking the setup pull request: %w", unmarshalError)
		}
		if current.PullRequestMergedAt != nil && *current.PullRequestMergedAt != "" {
			_, _ = fmt.Fprintln(progress, "Setup pull request merged.")
			return nil
		}
	}
}

// shipWorkflowRun and shipWorkflowRunsPage are the slice of the
// workflow-runs read the image wait steers by.
type shipWorkflowRun struct {
	ID         int64   `json:"id"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	Branch     string  `json:"branch"`
	HTMLURL    string  `json:"html_url"`
}

type shipWorkflowRunsPage struct {
	Runs  []shipWorkflowRun `json:"runs"`
	Error *string           `json:"error"`
}

// waitForWorkflowSuccess polls the repository's workflow runs until the
// latest run on the tracked branch concludes. Success returns the run URL as
// the build reference; a failed conclusion is the command's failure, with the
// run URL as the place to look.
func waitForWorkflowSuccess(
	waitContext context.Context,
	progress io.Writer,
	applicationID string,
	trackedBranch string,
) (string, error) {
	announcedWaiting := false
	announcedRunning := ""
	announcedListingError := ""
	for {
		payload, readError := apiClient.GetApplicationWorkflowRuns(waitContext, applicationID, "", 1, 20)
		if readError != nil {
			return "", shipReadError(waitContext, "reading the workflow runs", readError)
		}
		var runs shipWorkflowRunsPage
		if unmarshalError := json.Unmarshal(payload, &runs); unmarshalError != nil {
			return "", fmt.Errorf("reading the workflow runs: %w", unmarshalError)
		}
		if runs.Error != nil && *runs.Error != "" && *runs.Error != announcedListingError {
			_, _ = fmt.Fprintf(progress, "Workflow runs could not be listed (%s); retrying.\n", *runs.Error)
			announcedListingError = *runs.Error
		}
		var latest *shipWorkflowRun
		for index := range runs.Runs {
			if strings.EqualFold(runs.Runs[index].Branch, trackedBranch) {
				latest = &runs.Runs[index]
				break
			}
		}
		switch {
		case latest == nil:
			if !announcedWaiting {
				_, _ = fmt.Fprintf(progress,
					"Waiting for a workflow run on branch %q - follow them with 'ankra application workflow-runs %s'.\n",
					trackedBranch, applicationID)
				announcedWaiting = true
			}
		case latest.Status != "completed":
			if latest.HTMLURL != announcedRunning {
				_, _ = fmt.Fprintf(progress, "CI is building the image: %s\n", latest.HTMLURL)
				announcedRunning = latest.HTMLURL
			}
		default:
			conclusion := ""
			if latest.Conclusion != nil {
				conclusion = *latest.Conclusion
			}
			if conclusion == "success" {
				_, _ = fmt.Fprintf(progress, "CI built the image: %s\n", latest.HTMLURL)
				return latest.HTMLURL, nil
			}
			return "", fmt.Errorf(
				"the workflow run on branch %q concluded %q: %s - fix the workflow, or re-run it with 'ankra application rerun-workflow %s %d'",
				trackedBranch, conclusion, latest.HTMLURL, applicationID, latest.ID)
		}
		if tickError := shipWaitTick(waitContext, shipPollInterval, "waiting for the image build"); tickError != nil {
			return "", tickError
		}
	}
}

// shipBranchListing is the slice of the branches read the platform-build lane
// uses to name the commit to build.
type shipBranchListing struct {
	Branches []struct {
		Name    string `json:"name"`
		HeadSHA string `json:"head_sha"`
	} `json:"branches"`
	Error *string `json:"error"`
}

// runShipPlatformBuild builds the tracked branch's head commit on Ankra's
// builders and follows the build to a pushed image, reusing the build lane's
// own polling. The routes answer 404 for organisations without the
// platform_builds feature flag, and that answer is translated rather than
// passed through as a bare not-found.
func runShipPlatformBuild(
	waitContext context.Context,
	command *cobra.Command,
	progress io.Writer,
	applicationID string,
	trackedBranch string,
) (string, error) {
	branchesPayload, branchesError := apiClient.GetApplicationBranches(waitContext, applicationID)
	if branchesError != nil {
		return "", fmt.Errorf("reading the repository branches: %w", branchesError)
	}
	var branches shipBranchListing
	if unmarshalError := json.Unmarshal(branchesPayload, &branches); unmarshalError != nil {
		return "", fmt.Errorf("reading the repository branches: %w", unmarshalError)
	}
	if branches.Error != nil && *branches.Error != "" {
		return "", fmt.Errorf("reading the repository branches: %s", *branches.Error)
	}
	headSHA := ""
	for _, branch := range branches.Branches {
		if strings.EqualFold(branch.Name, trackedBranch) {
			headSHA = branch.HeadSHA
			break
		}
	}
	if headSHA == "" {
		return "", fmt.Errorf("branch %q was not found in the repository, so there is no commit to build", trackedBranch)
	}

	startPayload, startError := apiClient.StartApplicationPlatformBuild(waitContext, applicationID,
		client.StartApplicationPlatformBuildRequest{HeadSHA: headSHA, Ref: trackedBranch})
	if startError != nil {
		var unexpected *client.UnexpectedResponseError
		if errors.As(startError, &unexpected) && unexpected.StatusCode == 404 {
			return "", errors.New(
				"platform builds are not enabled for this organisation (the platform_builds feature flag is off; ask Ankra support to enable it) - re-run without --ankra-build to use the repository's own CI")
		}
		return "", fmt.Errorf("starting the platform build: %w", startError)
	}
	var started startedBuild
	if unmarshalError := json.Unmarshal(startPayload, &started); unmarshalError != nil {
		return "", fmt.Errorf("reading the queued build: %w", unmarshalError)
	}
	if started.AlreadyRequested {
		_, _ = fmt.Fprintf(progress, "Joined the platform build already queued for commit %s (request %s).\n",
			headSHA, started.RequestID)
	} else {
		_, _ = fmt.Fprintf(progress, "Queued a platform build of commit %s (request %s).\n", headSHA, started.RequestID)
	}

	buildID, claimError := awaitClaimedBuild(waitContext, progress, applicationID, started)
	if claimError != nil {
		return "", claimError
	}
	finished, followError := awaitFinishedBuild(waitContext, progress, applicationID, buildID)
	if followError != nil {
		return "", followError
	}
	if finished.build.Status != "succeeded" {
		return "", describeFailedBuild(finished.build)
	}
	buildReference := finished.build.ID
	if finished.build.ImageRef != nil && *finished.build.ImageRef != "" {
		buildReference = *finished.build.ImageRef
	}
	_, _ = fmt.Fprintf(progress, "Platform build succeeded: %s\n", buildReference)
	return buildReference, nil
}

// defaultShipNamespace mirrors the platform's default_deploy_namespace
// derivation, so ship knows the namespace a deploy without --namespace lands
// in and can verify pods and read the ingress there.
func defaultShipNamespace(applicationName string) string {
	sanitized := strings.ToLower(strings.TrimSpace(applicationName))
	var builder strings.Builder
	for _, character := range sanitized {
		isSafe := (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '-'
		switch {
		case isSafe:
			builder.WriteRune(character)
		case character == '_' || character == '.' || character == ' ':
			builder.WriteByte('-')
		}
	}
	namespace := strings.Trim(builder.String(), "-")
	if namespace == "" {
		return "default"
	}
	if len(namespace) > 63 {
		namespace = strings.TrimRight(namespace[:63], "-")
	}
	return namespace
}

// shipInstallationsListing is the slice of the installations read the deploy
// and verify steps steer by.
type shipInstallationsListing struct {
	Installations []shipInstallation `json:"installations"`
}

type shipInstallation struct {
	ID           string  `json:"id"`
	ClusterID    string  `json:"cluster_id"`
	Namespace    string  `json:"namespace"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message"`
}

// shipDeployAnswer is the deploy result: the installation to follow and the
// platform's message, which carries the ingress derivation note when the
// platform provides one.
type shipDeployAnswer struct {
	InstallationID string `json:"installation_id"`
	Status         string `json:"status"`
	Message        string `json:"message"`
}

// ensureApplicationDeployed reads the installations first and only deploys
// when this cluster+namespace has no installation converging already: a
// healthy installation is left alone, a settled-failed or degraded one is
// deployed over (re-deploying is the designed recovery), and anything else -
// pending, deploying, and any status this CLI does not know - is followed
// rather than deployed on top of. Unknown statuses default to following
// because a platform that grows a new transitional state must not turn a
// re-run into a second deploy racing the first.
func ensureApplicationDeployed(
	waitContext context.Context,
	progress io.Writer,
	applicationID string,
	clusterID string,
	namespace string,
	deployInputs map[string]string,
) (string, string, error) {
	existing, readError := findShipInstallation(waitContext, applicationID, clusterID, namespace, "")
	if readError != nil {
		return "", "", readError
	}
	if existing != nil {
		switch existing.Status {
		case "healthy":
			_, _ = fmt.Fprintf(progress, "Already deployed and healthy in namespace %q (installation %s).\n",
				namespace, existing.ID)
			return existing.ID, "", nil
		case "failed", "degraded":
			_, _ = fmt.Fprintf(progress, "The existing installation is %s (installation %s); deploying again.\n",
				existing.Status, existing.ID)
		default:
			_, _ = fmt.Fprintf(progress, "A deploy is already in flight (installation %s, status %s); following it.\n",
				existing.ID, existing.Status)
			return existing.ID, "", nil
		}
	}

	payload, deployError := apiClient.DeployApplication(waitContext, applicationID, client.DeployApplicationRequest{
		ClusterID: clusterID,
		Namespace: namespace,
		Inputs:    deployInputs,
	})
	if deployError != nil {
		return "", "", fmt.Errorf("deploying the application: %w", deployError)
	}
	var answer shipDeployAnswer
	if unmarshalError := json.Unmarshal(payload, &answer); unmarshalError != nil {
		return "", "", fmt.Errorf("reading the deploy answer: %w", unmarshalError)
	}
	if answer.InstallationID == "" {
		return "", "", errors.New("the deploy answer did not name an installation to follow")
	}
	_, _ = fmt.Fprintf(progress, "Deploy started (installation %s, namespace %q).\n", answer.InstallationID, namespace)
	if answer.Message != "" {
		_, _ = fmt.Fprintf(progress, "%s\n", answer.Message)
	}
	return answer.InstallationID, answer.Message, nil
}

// findShipInstallation reads the installations and returns the one matching
// the installation id when given, else the cluster+namespace pair; nil when
// none matches.
func findShipInstallation(
	waitContext context.Context,
	applicationID string,
	clusterID string,
	namespace string,
	installationID string,
) (*shipInstallation, error) {
	payload, listError := apiClient.GetApplicationInstallations(waitContext, applicationID)
	if listError != nil {
		return nil, shipReadError(waitContext, "reading the installations", listError)
	}
	var listing shipInstallationsListing
	if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
		return nil, fmt.Errorf("reading the installations: %w", unmarshalError)
	}
	for index := range listing.Installations {
		installation := &listing.Installations[index]
		if installationID != "" {
			if installation.ID == installationID {
				return installation, nil
			}
			continue
		}
		if installation.ClusterID == clusterID && installation.Namespace == namespace {
			return installation, nil
		}
	}
	return nil, nil
}

// waitForInstallationHealthy follows the installation to `healthy`. A settled
// `failed` surfaces the platform's execution error plus the namespace's
// warning events - the two places the actual cause lives - and exits
// non-zero; `degraded` keeps waiting, because the health state machine
// reports it transiently while pods roll.
func waitForInstallationHealthy(
	waitContext context.Context,
	progress io.Writer,
	applicationID string,
	installationID string,
	clusterID string,
	namespace string,
) error {
	announcedStatus := ""
	for {
		installation, readError := findShipInstallation(waitContext, applicationID, clusterID, namespace, installationID)
		if readError != nil {
			return readError
		}
		if installation == nil {
			return fmt.Errorf("installation %s disappeared from the installations listing", installationID)
		}
		switch installation.Status {
		case "healthy":
			_, _ = fmt.Fprintln(progress, "Installation is healthy.")
			return nil
		case "failed":
			message := "no error message was recorded"
			if installation.ErrorMessage != nil && strings.TrimSpace(*installation.ErrorMessage) != "" {
				message = *installation.ErrorMessage
			}
			reportNamespaceWarningEvents(progress, clusterID, namespace)
			return fmt.Errorf(
				"the deploy failed: %s - inspect it with 'ankra cluster get events -n %s' and 'ankra cluster operations list'",
				message, namespace)
		}
		if installation.Status != announcedStatus {
			_, _ = fmt.Fprintf(progress,
				"Waiting for the workload to become healthy (installation status: %s) - watch pods with 'ankra cluster get pods -n %s'.\n",
				installation.Status, namespace)
			announcedStatus = installation.Status
		}
		if tickError := shipWaitTick(waitContext, shipPollInterval, "waiting for the workload to become healthy"); tickError != nil {
			return tickError
		}
	}
}

// reportNamespaceWarningEvents prints the namespace's recent Warning events
// on a failed deploy, best-effort: the events explain most workload failures,
// and reading them must never mask the deploy error itself.
func reportNamespaceWarningEvents(progress io.Writer, clusterID string, namespace string) {
	response, readError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{{
			Kind: "Event", Version: "v1", Namespace: namespace,
		}},
	})
	if readError != nil || response == nil || len(response.ResourceResponses) == 0 {
		return
	}
	printed := 0
	for _, item := range response.ResourceResponses[0].Items {
		event, isObject := item.(map[string]interface{})
		if !isObject || getNestedString(event, "type") != "Warning" {
			continue
		}
		if printed == 0 {
			_, _ = fmt.Fprintf(progress, "Recent warning events in namespace %q:\n", namespace)
		}
		_, _ = fmt.Fprintf(progress, "  %s %s/%s: %s\n",
			getNestedString(event, "reason"),
			strings.ToLower(getNestedString(event, "involvedObject", "kind")),
			getNestedString(event, "involvedObject", "name"),
			getNestedString(event, "message"))
		printed++
		if printed == 5 {
			break
		}
	}
}

// reportNamespacePods reports the namespace's pods after the installation
// settles healthy, best-effort, so the final output names what is running.
func reportNamespacePods(progress io.Writer, clusterID string, namespace string) {
	response, readError := apiClient.ListPods(clusterID, &client.ListPodsOptions{
		Page: 1, PageSize: 100, Namespace: namespace,
	})
	if readError != nil || response == nil {
		return
	}
	runningCount := 0
	for _, pod := range response.Pods {
		if strings.EqualFold(pod.Phase, "Running") {
			runningCount++
		}
	}
	_, _ = fmt.Fprintf(progress, "Pods in namespace %q: %d running of %d.\n",
		namespace, runningCount, len(response.Pods))
}

// shipDeploymentsListing is the slice of the deployments read the URL
// resolution steers by: the per-deployment ingress_host the platform derives,
// paired with the publication verdict that says whether anything will ever
// publish DNS for it.
type shipDeploymentsListing struct {
	Deployments []shipDeploymentRow `json:"deployments"`
}

type shipDeploymentRow struct {
	ClusterID              string  `json:"cluster_id"`
	Namespace              *string `json:"namespace"`
	IngressHost            *string `json:"ingress_host"`
	IngressHostPublication *string `json:"ingress_host_publication"`
}

// findPublishedApplicationURL resolves the deployment's published hostname.
//
// Primary source is the deployments surface: each deployment row carries the
// ingress_host its installation declares, with ingress_host_publication as
// the verdict on whether anything publishes DNS for it - a host whose
// verdict is publisher_absent is reported with that caveat rather than
// presented as reachable. Rows without an ingress_host (hand-installed
// add-ons, older platforms) fall back to reading the namespace's Ingress
// resources directly. Both reads are best-effort and empty-tolerant: an
// application deployed without ingress simply has no URL.
func findPublishedApplicationURL(
	requestContext context.Context,
	progress io.Writer,
	applicationID string,
	clusterID string,
	namespace string,
) string {
	payload, readError := apiClient.GetApplicationDeployments(requestContext, applicationID)
	if readError != nil {
		_, _ = fmt.Fprintf(progress, "Could not read the deployments surface for the published URL: %v\n", readError)
	} else {
		var listing shipDeploymentsListing
		if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
			_, _ = fmt.Fprintf(progress, "Could not read the deployments surface for the published URL: %v\n", unmarshalError)
		} else {
			for _, deployment := range listing.Deployments {
				if deployment.ClusterID != clusterID ||
					deployment.Namespace == nil || *deployment.Namespace != namespace ||
					deployment.IngressHost == nil || *deployment.IngressHost == "" {
					continue
				}
				if deployment.IngressHostPublication != nil && *deployment.IngressHostPublication == "publisher_absent" {
					_, _ = fmt.Fprintf(progress,
						"The deployment declares hostname %q, but nothing on the cluster publishes DNS for it - the URL will not resolve until a DNS publisher (external-dns or the cluster domain) covers it.\n",
						*deployment.IngressHost)
				}
				return "https://" + *deployment.IngressHost
			}
		}
	}
	return findNamespaceIngressURL(progress, clusterID, namespace)
}

// findNamespaceIngressURL reads the namespace's Ingress resources and returns
// the first published hostname as an https URL: the fallback for deployment
// rows that carry no ingress_host.
func findNamespaceIngressURL(progress io.Writer, clusterID string, namespace string) string {
	response, readError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{{
			Kind: "Ingress", Group: "networking.k8s.io", Version: "v1", Namespace: namespace,
		}},
	})
	if readError != nil {
		_, _ = fmt.Fprintf(progress, "Could not read the namespace's ingresses for a published URL: %v\n", readError)
		return ""
	}
	if response == nil || len(response.ResourceResponses) == 0 {
		return ""
	}
	for _, item := range response.ResourceResponses[0].Items {
		ingress, isObject := item.(map[string]interface{})
		if !isObject {
			continue
		}
		specification, hasSpecification := getNestedMap(ingress, "spec")
		if !hasSpecification {
			continue
		}
		rules, hasRules := specification["rules"].([]interface{})
		if !hasRules {
			continue
		}
		for _, rule := range rules {
			ruleObject, isRuleObject := rule.(map[string]interface{})
			if !isRuleObject {
				continue
			}
			if host := getNestedString(ruleObject, "host"); host != "" {
				return "https://" + host
			}
		}
	}
	return ""
}
