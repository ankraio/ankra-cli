package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Setting a service up: prepare a review (the platform resolves and stores a
// plan with a digest, and changes nothing), show the plan, and confirm it by
// that digest only after a yes at the prompt or --yes.

// serviceNamePattern is the platform's rule for service names, regions and
// data boundaries: lower-case words joined by single hyphens.
var serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// serviceReviewPlanView is the part of a setup plan the CLI shows. The plan
// itself is kept and rendered exactly as the platform sent it under -o.
type serviceReviewPlanView struct {
	Name             string            `json:"name"`
	Mode             string            `json:"mode"`
	Region           string            `json:"region"`
	DataBoundary     string            `json:"data_boundary"`
	ServiceClusterID string            `json:"service_cluster_id"`
	ClusterID        string            `json:"cluster_id"`
	PackageDigest    string            `json:"package_digest"`
	ExpiresAt        string            `json:"expires_at"`
	Parameters       map[string]int64  `json:"parameters"`
	SecretReferences map[string]string `json:"secret_references"`
	Consumers        []struct {
		ID            string `json:"id"`
		ApplicationID string `json:"application_id"`
		ClusterID     string `json:"cluster_id"`
		Namespace     string `json:"namespace"`
		BindingKind   string `json:"binding_kind"`
		Region        string `json:"region"`
		DataBoundary  string `json:"data_boundary"`
	} `json:"consumers"`
}

func parseServiceReviewPlan(plan map[string]any) (serviceReviewPlanView, error) {
	var view serviceReviewPlanView
	encoded, marshalError := json.Marshal(plan)
	if marshalError != nil {
		return view, marshalError
	}
	return view, json.Unmarshal(encoded, &view)
}

func formatServiceParameters(parameters map[string]int64) string {
	if len(parameters) == 0 {
		return "none"
	}
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, fmt.Sprintf("%s=%d", name, parameters[name]))
	}
	return strings.Join(pairs, ", ")
}

func serviceSortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// printServiceReviewPlan shows a prepared setup review: what confirming it
// would set up, where, for whom, and the digest a confirmation carries.
func printServiceReviewPlan(out io.Writer, names *serviceNames, packageLabel string, review client.ServiceReview) {
	plan, parseError := parseServiceReviewPlan(review.Plan)
	_, _ = fmt.Fprintf(out, "Setup review %s\n", review.ID)
	if parseError != nil {
		_, _ = fmt.Fprintf(out, "  (the plan could not be read for display: %v; -o json shows it as sent)\n", parseError)
	} else {
		_, _ = fmt.Fprintf(out, "  Service:        %s\n", plan.Name)
		_, _ = fmt.Fprintf(out, "  Package:        %s\n", packageLabel)
		_, _ = fmt.Fprintf(out, "  Mode:           %s\n", plan.Mode)
		serviceCluster := plan.ServiceClusterID
		if serviceCluster == "" || serviceCluster == "00000000-0000-0000-0000-000000000000" {
			serviceCluster = plan.ClusterID
		}
		_, _ = fmt.Fprintf(out, "  Cluster:        %s\n", names.clusterWithID(serviceCluster))
		_, _ = fmt.Fprintf(out, "  Location:       region %s, data boundary %s\n", plan.Region, plan.DataBoundary)
		_, _ = fmt.Fprintf(out, "  Parameters:     %s\n", formatServiceParameters(plan.Parameters))
		if len(plan.SecretReferences) == 0 {
			_, _ = fmt.Fprintln(out, "  Secret inputs:  none")
		} else {
			_, _ = fmt.Fprintf(out, "  Secret inputs:  %s (credential references; no values)\n",
				strings.Join(serviceSortedKeys(plan.SecretReferences), ", "))
		}
		_, _ = fmt.Fprintln(out, "  Consumers:")
		for _, consumer := range plan.Consumers {
			binding := consumer.BindingKind
			if binding == "" {
				binding = "binding"
			}
			_, _ = fmt.Fprintf(out, "    %s  application %s, cluster %s, namespace %s (%s)\n", consumer.ID,
				consumer.ApplicationID, names.cluster(consumer.ClusterID), consumer.Namespace, binding)
		}
		if plan.ExpiresAt != "" {
			_, _ = fmt.Fprintf(out, "  Expires:        %s\n", plan.ExpiresAt)
		}
	}
	_, _ = fmt.Fprintf(out, "  Digest:         %s\n", review.Digest)
}

// printServiceReviewSummary shows a stored review as the history read
// projects it: the selection and destinations, never secret references.
func printServiceReviewSummary(out io.Writer, names *serviceNames, review client.ServiceReviewSummary) {
	_, _ = fmt.Fprintf(out, "Setup review %s of service %q\n", review.ID, review.Name)
	state := review.State
	if review.State == "pending" || review.State == "expired" {
		state += " (window ends " + review.ExpiresAt + ")"
	}
	_, _ = fmt.Fprintf(out, "  State:          %s\n", state)
	_, _ = fmt.Fprintf(out, "  Package:        %s\n", names.packageVersion(review.PackageVersionID))
	_, _ = fmt.Fprintf(out, "  Mode:           %s\n", review.Mode)
	_, _ = fmt.Fprintf(out, "  Cluster:        %s\n", names.clusterWithID(review.ClusterID))
	_, _ = fmt.Fprintf(out, "  Location:       region %s, data boundary %s\n", review.Region, review.DataBoundary)
	_, _ = fmt.Fprintf(out, "  Parameters:     %s\n", formatServiceParameters(review.Parameters))
	if len(review.SecretInputs) == 0 {
		_, _ = fmt.Fprintln(out, "  Secret inputs:  none")
	} else {
		_, _ = fmt.Fprintf(out, "  Secret inputs:  %s\n", strings.Join(review.SecretInputs, ", "))
	}
	_, _ = fmt.Fprintln(out, "  Destinations:")
	for _, destination := range review.Destinations {
		_, _ = fmt.Fprintf(out, "    application %s, cluster %s, namespace %s\n",
			destination.ApplicationID, names.cluster(destination.ClusterID), destination.Namespace)
	}
	_, _ = fmt.Fprintf(out, "  Created:        %s\n", review.CreatedAt)
	_, _ = fmt.Fprintf(out, "  Digest:         %s\n", review.Digest)
	if review.ExecutionID != nil {
		_, _ = fmt.Fprintf(out, "  Execution:      %s (confirmed %s)\n", *review.ExecutionID, valueOr(review.ConfirmedAt, "-"))
	}
}

// parseServiceAssignments reads repeated name=value flags.
func parseServiceAssignments(flagName string, entries []string) (map[string]string, error) {
	assignments := map[string]string{}
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !found || name == "" || value == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--%s %q: expected name=value", flagName, entry))
		}
		if _, duplicate := assignments[name]; duplicate {
			return nil, withExitCode(exitUsage, fmt.Errorf("--%s names %q twice", flagName, name))
		}
		assignments[name] = value
	}
	return assignments, nil
}

// checkServiceSetupAgainstContract refuses, before any review is stored, a
// mode the package does not offer and parameters it does not declare or
// that fall outside its bounds. The platform refuses the same; saying so
// here names the bounds instead of answering a bare 422.
func checkServiceSetupAgainstContract(detail client.ServicePackageDetail, mode string, parameters map[string]int64) error {
	if _, offered := detail.Contract.Profiles[mode]; !offered {
		return withExitCode(exitUsage, fmt.Errorf("package %s %s does not run in %s mode (it offers: %s)",
			detail.Name, detail.Version, mode, strings.Join(servicePackageModes(detail.Contract), ", ")))
	}
	declared := map[string]client.ServicePackageParameter{}
	for _, parameter := range detail.Contract.Parameters {
		declared[parameter.Name] = parameter
	}
	for _, name := range serviceSortedKeys(parameters) {
		parameter, known := declared[name]
		if !known {
			return withExitCode(exitUsage, fmt.Errorf("package %s %s has no parameter %q ('ankra services packages get %s' lists them)",
				detail.Name, detail.Version, name, detail.ID))
		}
		if value := parameters[name]; value < parameter.Minimum || value > parameter.Maximum {
			return withExitCode(exitUsage, fmt.Errorf("--param %s=%d is outside %d-%d %s",
				name, value, parameter.Minimum, parameter.Maximum, parameter.Unit))
		}
	}
	return nil
}

func newServicesSetupCommand() *cobra.Command {
	setupCommand := &cobra.Command{
		Use:     "setup <name>",
		Aliases: []string{"create"},
		Short:   "Set up a managed service: review the plan, then confirm it",
		Long: `Set up a managed service on a cluster, in two steps.

First a review is prepared: the platform resolves the package, the cluster's
placement policy and every consumer, and stores the plan with a digest for ten
minutes. Preparing changes nothing. The plan is shown, and the service is set
up only when you confirm it: at the prompt, or with --yes. The confirmation
carries the digest of the plan you saw, so a plan that changed in between is
refused rather than deployed.

--review-only stops after the review and prints the command that confirms it
('ankra services reviews confirm <id> --digest <digest>'), for a person or a
pipeline that approves separately.

The name becomes the service's namespace on the cluster: lower-case letters,
digits and single hyphens, at most 36 characters for Ankra's engines. Every
setup needs at least one consumer (--consumer, from 'ankra services consumers
bind'). The region and data boundary default to the cluster's placement
policy. Parameters (--param) default to the package's defaults; 'ankra
services packages get <package>' lists them with their bounds.

Confirming starts the deployment; it is not proof that the service is ready.
Follow it with 'ankra services get <name>'.`,
		Example: `  ankra services setup orders-db --package postgresql --cluster prod --consumer 5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11
  ankra services setup orders-db --package postgresql@1.0.0 --consumer <id> --param storage_gib=20 --param instances=3
  ankra services setup orders-db --package postgresql --consumer <id> --review-only
  ankra services setup orders-db --package postgresql --consumer <id> --yes -o json`,
		Args: cobra.ExactArgs(1),
		RunE: runServicesSetup,
	}
	setupCommand.Flags().String("package", "", "The package to set up: name, name@version or id ('ankra services packages list')")
	setupCommand.Flags().String("cluster", "", "The cluster to run the service on (name or id; default: the selected cluster)")
	setupCommand.Flags().StringSlice("consumer", nil, "Consumer binding id the service is for (repeatable; 'ankra services consumers bind')")
	setupCommand.Flags().String("mode", "customer", "Operating mode: customer (runs on your cluster) or existing")
	setupCommand.Flags().String("region", "", "Physical region (default: the cluster's placement policy)")
	setupCommand.Flags().String("data-boundary", "", "Data boundary (default: the cluster's placement policy)")
	setupCommand.Flags().StringArray("param", nil, "Package parameter as name=value (repeatable)")
	setupCommand.Flags().StringArray("secret-reference", nil, "Secret input as name=<credential grant id> (repeatable); values are never accepted")
	setupCommand.Flags().Bool("review-only", false, "Prepare and show the review, but do not confirm it")
	setupCommand.Flags().BoolP("yes", "y", false, "Confirm the reviewed plan without asking")
	_ = setupCommand.MarkFlagRequired("package")
	registerStructuredOutputFlags(setupCommand)
	return setupCommand
}

func runServicesSetup(command *cobra.Command, arguments []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	ctx := command.Context()
	serviceName := strings.TrimSpace(arguments[0])
	if len(serviceName) > 63 || !serviceNamePattern.MatchString(serviceName) {
		return withExitCode(exitUsage, fmt.Errorf("service name %q: use lower-case letters, digits and single hyphens, "+
			"starting with a letter (it becomes the service's namespace)", serviceName))
	}
	mode, _ := command.Flags().GetString("mode")
	mode = strings.TrimSpace(mode)
	if mode == "hosted" {
		return withExitCode(exitUsage, errors.New("hosted services cannot be set up yet: the platform refuses hosted plans; use --mode customer"))
	}
	if mode != "customer" && mode != "existing" {
		return withExitCode(exitUsage, fmt.Errorf("--mode %q: expected customer or existing", mode))
	}
	consumerIDs, _ := command.Flags().GetStringSlice("consumer")
	consumers := make([]string, 0, len(consumerIDs))
	for _, consumerID := range consumerIDs {
		if consumerID = strings.TrimSpace(consumerID); consumerID != "" {
			consumers = append(consumers, consumerID)
		}
	}
	if len(consumers) == 0 {
		return withExitCode(exitUsage, errors.New("at least one --consumer is required: bind the application namespace that will use "+
			"the service with 'ankra services consumers bind' and pass the id it prints"))
	}
	parameterEntries, _ := command.Flags().GetStringArray("param")
	rawParameters, parameterError := parseServiceAssignments("param", parameterEntries)
	if parameterError != nil {
		return parameterError
	}
	parameters := make(map[string]int64, len(rawParameters))
	for name, raw := range rawParameters {
		value, parseError := strconv.ParseInt(raw, 10, 64)
		if parseError != nil {
			return withExitCode(exitUsage, fmt.Errorf("--param %s=%s: the value must be a whole number", name, raw))
		}
		parameters[name] = value
	}
	secretEntries, _ := command.Flags().GetStringArray("secret-reference")
	secretReferences, secretError := parseServiceAssignments("secret-reference", secretEntries)
	if secretError != nil {
		return secretError
	}
	for name, grantID := range secretReferences {
		if !looksLikeUUID(grantID) {
			return withExitCode(exitUsage, fmt.Errorf("--secret-reference %s: expected a credential grant id, not a value", name))
		}
	}

	packageReference, _ := command.Flags().GetString("package")
	versionID, packageError := resolveServicePackageID(ctx, packageReference)
	if packageError != nil {
		return packageError
	}
	detail, detailError := apiClient.GetServicePackage(ctx, versionID)
	if detailError != nil {
		return detailError
	}
	if contractError := checkServiceSetupAgainstContract(*detail, mode, parameters); contractError != nil {
		return contractError
	}

	clusterFlag, _ := command.Flags().GetString("cluster")
	clusterID, clusterName, clusterError := resolveClusterForCmd(strings.TrimSpace(clusterFlag))
	if clusterError != nil {
		return clusterError
	}
	region, _ := command.Flags().GetString("region")
	dataBoundary, _ := command.Flags().GetString("data-boundary")
	region, dataBoundary = strings.TrimSpace(region), strings.TrimSpace(dataBoundary)
	if region == "" || dataBoundary == "" {
		policy, policyError := apiClient.GetServiceClusterPolicy(ctx, clusterID)
		if policyError != nil {
			if isServicePolicyMissing(policyError) {
				return fmt.Errorf("cluster %s has no service placement policy, and setup needs one: declare it with "+
					"'ankra services policy set --cluster %s --region <region> --data-boundary <boundary>'", clusterName, clusterName)
			}
			return policyError
		}
		if region == "" {
			region = policy.Region
		}
		if dataBoundary == "" {
			dataBoundary = policy.DataBoundary
		}
	}

	review, prepareError := apiClient.PrepareServiceReview(ctx, client.ServiceReviewRequest{
		PackageVersionID: versionID,
		Name:             serviceName,
		Mode:             mode,
		Region:           region,
		DataBoundary:     dataBoundary,
		ClusterID:        clusterID,
		ConsumerIDs:      consumers,
		Parameters:       parameters,
		SecretReferences: secretReferences,
	})
	if prepareError != nil {
		return prepareError
	}

	names := newServiceNames(ctx)
	human := serviceHumanWriter(command)
	printServiceReviewPlan(human, names, detail.Name+" "+detail.Version, *review)
	confirmHint := fmt.Sprintf("ankra services reviews confirm %s --digest %s", review.ID, review.Digest)

	reviewOnly, _ := command.Flags().GetBool("review-only")
	if reviewOnly {
		if rendered, renderError := renderStructured(command, review); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(human, "\nNothing was set up. Confirm this plan within ten minutes with:\n  %s\n", confirmHint)
		return nil
	}

	yes, _ := command.Flags().GetBool("yes")
	if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
		fmt.Sprintf("\nSet up service %q on cluster %s as reviewed above? [y/N]: ", serviceName, clusterName), yes); confirmError != nil {
		if errors.Is(confirmError, errCancelled) {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Nothing was set up. The review stays open for ten minutes; confirm it with:\n  %s\n", confirmHint)
		}
		return confirmError
	}
	confirmed, confirmError := apiClient.ConfirmServiceReview(ctx, review.ID, review.Digest)
	if confirmError != nil {
		return confirmError
	}
	if rendered, renderError := renderStructured(command, confirmed); rendered || renderError != nil {
		return renderError
	}
	printServiceSetupReceipt(command.OutOrStdout(), serviceName, clusterName, *confirmed)
	return nil
}

func printServiceSetupReceipt(out io.Writer, serviceName string, clusterName string, confirmed client.ServiceReview) {
	_, _ = fmt.Fprintf(out, "\nService %q is being set up on cluster %s.\n", serviceName, clusterName)
	if confirmed.ExecutionID != nil {
		_, _ = fmt.Fprintf(out, "  Execution:  %s\n", *confirmed.ExecutionID)
	}
	if confirmed.ConfirmedAt != nil {
		_, _ = fmt.Fprintf(out, "  Confirmed:  %s\n", *confirmed.ConfirmedAt)
	}
	_, _ = fmt.Fprintf(out, "Confirming starts the deployment; it does not mean the service is ready. Follow its health and readiness with:\n"+
		"  ankra services get %s --cluster %s\n", serviceName, clusterName)
}

func newServicesReviewsCommand() *cobra.Command {
	reviewsCommand := &cobra.Command{
		Use:     "reviews",
		Aliases: []string{"review"},
		Short:   "List, read and confirm your setup reviews",
		Long: `List, read and confirm your own setup reviews.

'ankra services setup' prepares a review and confirms it. A review that was
prepared with --review-only, declined at the prompt, or whose answer was lost
stays pending for ten minutes: find it here and confirm it instead of preparing
another (each person may hold 20 open reviews). An expired review needs a new
setup. Reviews are personal: nobody else sees or confirms yours.`,
	}
	reviewsCommand.AddCommand(newServicesReviewsListCommand())
	reviewsCommand.AddCommand(newServicesReviewsGetCommand())
	reviewsCommand.AddCommand(newServicesReviewsConfirmCommand())
	return reviewsCommand
}

func newServicesReviewsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your setup reviews, newest first",
		Example: "  ankra services reviews list\n  ankra services reviews list --state pending",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			state, _ := command.Flags().GetString("state")
			state = strings.TrimSpace(state)
			if state != "" && state != "pending" && state != "expired" && state != "confirmed" {
				return withExitCode(exitUsage, fmt.Errorf("--state %q: expected pending, expired or confirmed", state))
			}
			reviews, listError := collectServicePages(func(after string) ([]client.ServiceReviewSummary, *string, error) {
				page, pageError := apiClient.ListServiceReviews(command.Context(), client.ServicePageOptions{Limit: servicePageLimit, After: after})
				if pageError != nil {
					return nil, nil, pageError
				}
				return page.Items, page.NextCursor, nil
			})
			if listError != nil {
				return listError
			}
			shown := make([]client.ServiceReviewSummary, 0, len(reviews))
			for _, review := range reviews {
				if state == "" || review.State == state {
					shown = append(shown, review)
				}
			}
			if rendered, renderError := renderStructured(command, shown); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(shown) == 0 {
				_, _ = fmt.Fprintln(out, "No setup reviews.")
				return nil
			}
			names := newServiceNames(command.Context())
			reviewTable := table.NewWriter()
			reviewTable.SetOutputMirror(out)
			reviewTable.SetStyle(table.StyleRounded)
			reviewTable.AppendHeader(table.Row{"ID", "Service", "Cluster", "Package", "State", "Created", "Expires"})
			for _, review := range shown {
				reviewTable.AppendRow(table.Row{review.ID, review.Name, names.cluster(review.ClusterID),
					names.packageVersion(review.PackageVersionID), review.State, review.CreatedAt, review.ExpiresAt})
			}
			reviewTable.Render()
			return nil
		},
	}
	listCommand.Flags().String("state", "", "Only reviews in this state: pending, expired or confirmed")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newServicesReviewsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:     "get <review-id>",
		Short:   "Show one of your setup reviews",
		Example: "  ankra services reviews get 0b6f2a8e-3a1c-4b8e-9a51-2c7d4f1e9a20",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			review, getError := apiClient.GetServiceReview(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			if rendered, renderError := renderStructured(command, review); rendered || renderError != nil {
				return renderError
			}
			printServiceReviewSummary(command.OutOrStdout(), newServiceNames(command.Context()), *review)
			return nil
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newServicesReviewsConfirmCommand() *cobra.Command {
	confirmCommand := &cobra.Command{
		Use:   "confirm <review-id>",
		Short: "Confirm one of your pending setup reviews, which sets the service up",
		Long: `Confirm one of your pending setup reviews by its digest. This is the step that
sets the service up.

The review is shown and confirmed after a yes at the prompt, or with --yes.
--digest names the digest you reviewed (setup --review-only prints it); a
review whose digest differs is refused, because it is not the plan you saw.
Without --digest the digest of the review shown is the one confirmed. A
review that already expired needs a new 'ankra services setup'.`,
		Example: "  ankra services reviews confirm 0b6f2a8e-3a1c-4b8e-9a51-2c7d4f1e9a20 --digest sha256:4e86...\n" +
			"  ankra services reviews confirm 0b6f2a8e-3a1c-4b8e-9a51-2c7d4f1e9a20 --digest sha256:4e86... --yes",
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			ctx := command.Context()
			reviewID := strings.TrimSpace(arguments[0])
			review, getError := apiClient.GetServiceReview(ctx, reviewID)
			if getError != nil {
				return getError
			}
			names := newServiceNames(ctx)
			human := serviceHumanWriter(command)
			printServiceReviewSummary(human, names, *review)
			switch review.State {
			case "expired":
				return fmt.Errorf("review %s expired at %s and can no longer be confirmed: prepare a new one with 'ankra services setup %s'",
					review.ID, review.ExpiresAt, review.Name)
			case "confirmed":
				if rendered, renderError := renderStructured(command, review); rendered || renderError != nil {
					return renderError
				}
				_, _ = fmt.Fprintf(human, "\nReview %s was already confirmed; nothing more to do.\n", review.ID)
				return nil
			}
			digest, _ := command.Flags().GetString("digest")
			digest = strings.TrimSpace(digest)
			if digest != "" && digest != review.Digest {
				return fmt.Errorf("review %s has digest %s, not %s: it is not the plan you reviewed, so it was not confirmed",
					review.ID, review.Digest, digest)
			}
			yes, _ := command.Flags().GetBool("yes")
			clusterLabel := names.cluster(review.ClusterID)
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("\nSet up service %q on cluster %s as reviewed above? [y/N]: ", review.Name, clusterLabel), yes); confirmError != nil {
				return confirmError
			}
			confirmed, confirmError := apiClient.ConfirmServiceReview(ctx, review.ID, review.Digest)
			if confirmError != nil {
				return confirmError
			}
			if rendered, renderError := renderStructured(command, confirmed); rendered || renderError != nil {
				return renderError
			}
			printServiceSetupReceipt(command.OutOrStdout(), review.Name, clusterLabel, *confirmed)
			return nil
		},
	}
	confirmCommand.Flags().String("digest", "", "The digest you reviewed (sha256:...); refused if the review's differs")
	confirmCommand.Flags().BoolP("yes", "y", false, "Confirm without asking")
	registerStructuredOutputFlags(confirmCommand)
	return confirmCommand
}
