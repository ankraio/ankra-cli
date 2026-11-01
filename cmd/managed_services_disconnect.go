package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Disconnecting one application environment from a managed service that
// keeps running for its other applications (cluster#5595). Like a
// retirement it is two steps on the platform: prepare a disconnection review
// (the platform resolves what disconnecting removes, keeps and cannot do and
// stores it with a digest; nothing changes), show every statement of it in
// the platform's own words, and confirm it by that digest only after a yes at
// the prompt or --yes. Nothing is deleted: the service, its data and its
// shared credential stay.

// serviceNamesFromNone is the --names-from value that chooses no service to
// give the conventional connection names. It always means nobody: a service
// that happens to be named "none" is chosen by its id.
const serviceNamesFromNone = "none"

// serviceDisconnectSelection is what the flags say about which application
// environment to disconnect and who gives the conventional names next.
type serviceDisconnectSelection struct {
	application string
	environment string
	consumer    string
	namesFrom   string
}

func newServicesDisconnectCommand() *cobra.Command {
	disconnectCommand := &cobra.Command{
		Use:   "disconnect <service>",
		Short: "Disconnect one application from a managed service that keeps running: review it, then confirm",
		Long: `Disconnect one application environment from a managed service. The service
keeps running for its other applications, and nothing is deleted: the service,
its data and its credentials stay.

It is two steps on the platform. First a disconnection review is prepared: the
platform works out what disconnecting does and stores that plan with a digest
for ten minutes. Preparing changes nothing. The review is shown in the
platform's own words:

  Variables         the variables the application stops receiving (DATABASE_URL,
                    ...) and, when another service of the same kind is
                    connected, who gives the conventional names from now on
  Credential        every application of a service shares one credential
                    today, so it is not revoked: the application keeps the
                    password it has. What that cannot guarantee, and how to
                    make sure the application can no longer authenticate
                    (rotate the password, for every application)
  Secret            the connection Secret in the application's namespace,
                    which Ankra keeps until no pod reads it
  Workloads         the deployed workloads that read it: those Ankra attached
                    it to roll on the next deploy, those that refer to it by
                    hand must be changed by a person
  Network           whether the namespace loses access to the service's port
                    (--keep-network-access keeps it)
  Timing            the application keeps working until it is deployed again
  What stays        what is left in place afterwards

The application is disconnected only when you confirm the review: at the
prompt, or with --yes. Without a terminal to ask on, --yes is required.
--review-only shows the review and confirms nothing.

--application names the application, and --environment the environment when
the application uses the service from more than one. --consumer names one
consumer by its id instead ('ankra services get <service>' lists them), for
example a namespace no environment is deployed to.

When the service gives the application its conventional connection names
(DATABASE_URL, REDIS_URL, ...) and another service of the same kind is
connected too, the review asks who gives them from now on. --names-from
<service> chooses that service and --names-from none chooses nobody; without it
the review is shown and nothing is confirmed.

'ankra services get <service>' shows the disconnection until it has finished.`,
		Example: `  ankra services disconnect orders-db --application storefront
  ankra services disconnect orders-db --application storefront --environment staging --review-only
  ankra services disconnect orders-db --application storefront --names-from orders-db-v2 --yes
  ankra services disconnect orders-db --consumer 5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11 --keep-network-access -o json --yes`,
		Args: cobra.ExactArgs(1),
		RunE: runServicesDisconnect,
	}
	disconnectCommand.Flags().String("application", "", "The application to disconnect (name or id)")
	disconnectCommand.Flags().String("environment", "", "The application's environment, when it uses the service from more than one (name or id)")
	disconnectCommand.Flags().String("consumer", "", "The consumer to disconnect, by id ('ankra services get' lists them), instead of --application")
	disconnectCommand.Flags().String("cluster", "", "The cluster the service runs on, when its name is not unique (name or id)")
	disconnectCommand.Flags().String("names-from", "",
		"When the review asks: the connected service (name or id) that gives the conventional connection names (DATABASE_URL, ...) from now on, or none")
	disconnectCommand.Flags().Bool("keep-network-access", false, "Keep the application's namespace allowed on the service's port")
	disconnectCommand.Flags().Bool("review-only", false, "Prepare and show the disconnection review, but do not confirm it")
	disconnectCommand.Flags().BoolP("yes", "y", false, "Confirm the reviewed disconnection without asking")
	registerStructuredOutputFlags(disconnectCommand)
	return disconnectCommand
}

// serviceDisconnectSelectionFromFlags reads and checks the selection flags
// before anything is read from or prepared on the platform.
func serviceDisconnectSelectionFromFlags(command *cobra.Command) (serviceDisconnectSelection, error) {
	read := func(name string) string {
		value, _ := command.Flags().GetString(name)
		return strings.TrimSpace(value)
	}
	selection := serviceDisconnectSelection{application: read("application"), environment: read("environment"),
		consumer: read("consumer"), namesFrom: read("names-from")}
	for _, name := range []string{"application", "environment", "consumer", "names-from"} {
		if command.Flags().Changed(name) && read(name) == "" {
			return selection, withExitCode(exitUsage, fmt.Errorf("--%s is empty", name))
		}
	}
	switch {
	case selection.application == "" && selection.consumer == "":
		return selection, withExitCode(exitUsage, errors.New(
			"name the application to disconnect with --application (or one consumer by its id with --consumer)"))
	case selection.consumer != "" && !looksLikeUUID(selection.consumer):
		return selection, withExitCode(exitUsage, fmt.Errorf(
			"--consumer %q is not a consumer id: 'ankra services get <service>' lists the service's consumers", selection.consumer))
	case selection.environment != "" && selection.application == "":
		return selection, withExitCode(exitUsage, errors.New(
			"--environment chooses among the environments of the application --application names; --consumer already names one"))
	}
	reviewOnly, _ := command.Flags().GetBool("review-only")
	if yes, _ := command.Flags().GetBool("yes"); yes && reviewOnly {
		return selection, withExitCode(exitUsage, errors.New("--review-only confirms nothing, so it cannot be combined with --yes"))
	}
	return selection, nil
}

func runServicesDisconnect(command *cobra.Command, arguments []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	selection, selectionError := serviceDisconnectSelectionFromFlags(command)
	if selectionError != nil {
		return selectionError
	}
	ctx := command.Context()
	resolved, resolveError := resolveServiceInstance(command, arguments[0])
	if resolveError != nil {
		return resolveError
	}
	// A service found by name comes from the listing, which carries no
	// disconnections in progress; the single read does, and has the
	// generation as it is now.
	instance := resolved
	if strings.TrimSpace(arguments[0]) != resolved.ID {
		detail, readError := apiClient.GetServiceInstance(ctx, resolved.ID)
		if readError != nil {
			return readError
		}
		instance = detail
	}
	if instance.ReleasedAt != nil {
		return fmt.Errorf("service %q was retired at %s, so there is nothing to disconnect", instance.Name, *instance.ReleasedAt)
	}
	names := newServiceNames(command)
	consumer, consumerError := selectServiceDisconnectionConsumer(ctx, names, *instance, selection)
	if consumerError != nil {
		return consumerError
	}

	keepNetworkAccess, _ := command.Flags().GetBool("keep-network-access")
	request := client.ServiceDisconnectionRequest{ConsumerID: consumer.ID, ExpectedGeneration: instance.Generation,
		Timing: client.ServiceDisconnectAfterNextDeploy, KeepNetworkAccess: keepNetworkAccess}
	disconnection, prepareError := apiClient.PrepareServiceDisconnection(ctx, instance.ID, request)
	if prepareError != nil {
		return prepareError
	}

	human := serviceHumanWriter(command)
	reviewOnly, _ := command.Flags().GetBool("review-only")
	label := serviceConsumerLabel(names, *consumer)
	connectionNames := disconnection.Plan.ConnectionNames
	switch {
	case connectionNames.ChoiceRequired && connectionNames.Choice == nil && selection.namesFrom == "":
		printServiceDisconnectionPlan(human, names, *consumer, *disconnection)
		if reviewOnly {
			if rendered, renderError := renderStructured(command, disconnection); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(human, "\nNothing changed. Confirming needs a choice of who gives %s from now on: "+
				"run the command again with --names-from %s.\n", serviceConventionalNames(connectionNames), serviceNamesFromChoices(connectionNames.Choices))
			return nil
		}
		return withExitCode(exitUsage, fmt.Errorf("nothing changed: service %q gives application %s %s and another service of the same kind is "+
			"connected, so the disconnection needs a choice of who gives them from now on; run the command again with --names-from %s",
			disconnection.Plan.ServiceName, label, serviceConventionalNames(connectionNames), serviceNamesFromChoices(connectionNames.Choices)))
	case connectionNames.ChoiceRequired && connectionNames.Choice == nil:
		chosen, choiceError := serviceNamesChoiceFor(connectionNames.Choices, selection.namesFrom)
		if choiceError != nil {
			return choiceError
		}
		if chosen.None {
			request.ConnectionNamesNone = true
		} else {
			request.ConnectionNamesInstanceID = chosen.InstanceID
		}
		// The platform takes the choice only in a prepare, and only once a
		// review has asked for it, so the review is prepared again with it.
		// The first one confirms nothing and expires on its own.
		disconnection, prepareError = apiClient.PrepareServiceDisconnection(ctx, instance.ID, request)
		if prepareError != nil {
			return prepareError
		}
		if !serviceNamesChoiceRecorded(disconnection.Plan.ConnectionNames, request) {
			return fmt.Errorf("nothing changed: the platform prepared the review again without the choice --names-from %s made for the "+
				"conventional connection names; run the command again to review it again", selection.namesFrom)
		}
	case selection.namesFrom != "":
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "The review does not ask who gives the conventional connection names, so --names-from %s was not used.\n",
			selection.namesFrom)
	}

	printServiceDisconnectionPlan(human, names, *consumer, *disconnection)
	if reviewOnly {
		if rendered, renderError := renderStructured(command, disconnection); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(human, "\nNothing changed. Run the command again without --review-only to disconnect the application; "+
			"each run prepares a new review (this one is open until %s).\n", disconnection.ExpiresAt)
		return nil
	}
	if confirmError := confirmServiceDisconnection(command, disconnection.Plan.ServiceName, label); confirmError != nil {
		return confirmError
	}
	confirmed, confirmError := apiClient.ConfirmServiceDisconnection(ctx, instance.ID, disconnection.ID, disconnection.Digest)
	if confirmError != nil {
		var unexpected *client.UnexpectedResponseError
		if errors.As(confirmError, &unexpected) && unexpected.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w - nothing changed: the service or the application changed since the disconnection was reviewed, "+
				"or the review expired; run the command again to review it again", confirmError)
		}
		return confirmError
	}
	if confirmed.ConfirmedAt == nil {
		// A 200 that does not record the confirmation is not read as one.
		return fmt.Errorf("the platform answered the confirmation of disconnection %s without recording it as confirmed (state %s); "+
			"check with 'ankra services get %s' before trying again", confirmed.ID, confirmed.State, instance.Name)
	}
	if rendered, renderError := renderStructured(command, confirmed); rendered || renderError != nil {
		return renderError
	}
	printServiceDisconnectionReceipt(command.OutOrStdout(), instance.Name, label, *confirmed)
	return nil
}

// confirmServiceDisconnection asks for a yes before the disconnection is
// confirmed. --yes skips the question. Without a terminal to ask on, --yes is
// required: the refusal exits 4 like a declined prompt, and says how to go on.
// The question and its answer go to stderr, so -o json|yaml stays parseable.
func confirmServiceDisconnection(command *cobra.Command, serviceName string, label string) error {
	if yes, _ := command.Flags().GetBool("yes"); yes {
		return nil
	}
	errOut := command.ErrOrStderr()
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitCancelled, fmt.Errorf("nothing changed: disconnecting application %s from service %q needs a yes, "+
			"and there is no terminal to ask on; read the review and run the command again with --yes to disconnect it "+
			"(--review-only shows the review without asking)", label, serviceName))
	}
	prompt := fmt.Sprintf("\nDisconnect application %s from service %q as reviewed above? [y/N]: ", label, serviceName)
	if promptError := confirmPrompt(command.InOrStdin(), errOut, prompt, false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			_, _ = fmt.Fprintln(errOut, "Nothing changed: the application stays connected to the service.")
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// selectServiceDisconnectionConsumer finds the one consumer of the service
// the flags name: by its id, or by the application and, when it uses the
// service from more than one environment, the environment. An application
// environment already being disconnected is said to be, rather than
// reported as not served.
func selectServiceDisconnectionConsumer(ctx context.Context, names *serviceNames, instance client.ServiceInstance,
	selection serviceDisconnectSelection) (*client.ServiceInstanceConsumer, error) {
	applicationID := ""
	if selection.application != "" {
		resolvedID, resolveError := resolveApplicationID(ctx, apiClient, selection.application)
		if resolveError != nil {
			return nil, resolveError
		}
		applicationID = resolvedID
	}
	if selection.consumer != "" {
		for index := range instance.Consumers {
			consumer := instance.Consumers[index]
			if consumer.ID != selection.consumer {
				continue
			}
			if applicationID != "" && consumer.ApplicationID != applicationID {
				return nil, withExitCode(exitUsage, fmt.Errorf("consumer %s is application %s, not the application --application names (%s)",
					consumer.ID, names.consumerApplication(consumer.ApplicationID, consumer.ApplicationName), selection.application))
			}
			return &consumer, nil
		}
		if draining := serviceDisconnectionsInProgress(instance, func(item client.ServiceInstanceDisconnection) bool {
			return item.ConsumerID == selection.consumer
		}); len(draining) > 0 {
			return nil, serviceAlreadyDisconnectingError(names, instance, draining)
		}
		return nil, withExitCode(exitNotFound, fmt.Errorf("service %q has no consumer %s%s; 'ankra services get %s' lists its consumers",
			instance.Name, selection.consumer, serviceDisconnectionsUnreadableNote(instance), instance.Name))
	}

	var candidates []client.ServiceInstanceConsumer
	for _, consumer := range instance.Consumers {
		if consumer.ApplicationID == applicationID {
			candidates = append(candidates, consumer)
		}
	}
	if len(candidates) == 0 {
		if draining := serviceDisconnectionsInProgress(instance, func(item client.ServiceInstanceDisconnection) bool {
			return item.ApplicationID == applicationID
		}); len(draining) > 0 {
			return nil, serviceAlreadyDisconnectingError(names, instance, draining)
		}
		return nil, withExitCode(exitNotFound, fmt.Errorf("service %q does not serve application %s%s; "+
			"'ankra services list --application %s' shows the services it uses",
			instance.Name, selection.application, serviceDisconnectionsUnreadableNote(instance), selection.application))
	}
	if selection.environment != "" {
		served := make([]string, 0, len(candidates))
		for _, consumer := range candidates {
			served = append(served, serviceConsumerEnvironmentLabel(consumer))
		}
		candidates = serviceConsumersInEnvironment(candidates, selection.environment)
		if len(candidates) == 0 {
			return nil, withExitCode(exitNotFound, fmt.Errorf("service %q does not serve application %s in environment %s; it serves it in %s",
				instance.Name, selection.application, selection.environment, strings.Join(served, ", ")))
		}
	}
	if len(candidates) > 1 {
		described := make([]string, 0, len(candidates))
		for _, consumer := range candidates {
			clusterName := consumer.ClusterName
			if clusterName == "" {
				clusterName = names.cluster(consumer.ClusterID)
			}
			described = append(described, fmt.Sprintf("consumer %s: %s, namespace %s on cluster %s",
				consumer.ID, serviceConsumerEnvironmentLabel(consumer), consumer.Namespace, clusterName))
		}
		return nil, withExitCode(exitUsage, fmt.Errorf("service %q is connected to application %s in %d places (%s); choose one with --environment <name> or --consumer <id>",
			instance.Name, selection.application, len(candidates), strings.Join(described, "; ")))
	}
	return &candidates[0], nil
}

// serviceConsumersInEnvironment keeps the consumers whose application
// environment the reference names: by id or exact name, else by name in any
// case.
func serviceConsumersInEnvironment(consumers []client.ServiceInstanceConsumer, reference string) []client.ServiceInstanceConsumer {
	var exact, folded []client.ServiceInstanceConsumer
	for _, consumer := range consumers {
		environment := consumer.Environment
		if environment == nil {
			continue
		}
		switch {
		case environment.ID == reference || environment.Name == reference:
			exact = append(exact, consumer)
		case strings.EqualFold(environment.Name, reference):
			folded = append(folded, consumer)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return folded
}

// serviceDisconnectionsInProgress are the instance's disconnections in
// progress that match; none when the platform sent none or could not read
// them.
func serviceDisconnectionsInProgress(instance client.ServiceInstance, matches func(client.ServiceInstanceDisconnection) bool) []client.ServiceInstanceDisconnection {
	if instance.Disconnections == nil {
		return nil
	}
	var matched []client.ServiceInstanceDisconnection
	for _, item := range instance.Disconnections.Items {
		if matches(item) {
			matched = append(matched, item)
		}
	}
	return matched
}

// serviceDisconnectionsUnreadableNote says, inside a not-served refusal, that
// Ankra could not tell whether a disconnection is in progress.
func serviceDisconnectionsUnreadableNote(instance client.ServiceInstance) string {
	if instance.Disconnections != nil && instance.Disconnections.State == client.ServiceDisconnectionsUnknown {
		return " (Ankra could not read whether a disconnection from it is in progress)"
	}
	return ""
}

func serviceAlreadyDisconnectingError(names *serviceNames, instance client.ServiceInstance, draining []client.ServiceInstanceDisconnection) error {
	described := make([]string, 0, len(draining))
	for _, item := range draining {
		described = append(described, fmt.Sprintf("application %s (namespace %s, phase %s)", names.application(item.ApplicationID), item.Namespace, item.Phase))
	}
	return fmt.Errorf("%s is already being disconnected from service %q; 'ankra services get %s' shows the disconnection until it has finished",
		strings.Join(described, " and "), instance.Name, instance.Name)
}

// serviceConsumerLabel names a consumer for a person: the application and
// the environment deployed in its namespace, or the namespace while none is.
func serviceConsumerLabel(names *serviceNames, consumer client.ServiceInstanceConsumer) string {
	return fmt.Sprintf("%s (%s)", names.consumerApplication(consumer.ApplicationID, consumer.ApplicationName), serviceConsumerEnvironmentLabel(consumer))
}

func serviceConsumerEnvironmentLabel(consumer client.ServiceInstanceConsumer) string {
	if consumer.Environment != nil && consumer.Environment.Name != "" {
		return "environment " + consumer.Environment.Name
	}
	return "namespace " + consumer.Namespace
}

// serviceNamesChoiceFor finds the choice --names-from names among the ones
// the review offers: none, a service id, or a service name that only one
// offered service has.
func serviceNamesChoiceFor(choices []client.ServiceDisconnectionNamesChoice, value string) (client.ServiceDisconnectionNamesChoice, error) {
	if value == serviceNamesFromNone {
		return client.ServiceDisconnectionNamesChoice{None: true}, nil
	}
	var matched []client.ServiceDisconnectionNamesChoice
	for _, choice := range choices {
		if choice.None || choice.InstanceID == nil {
			continue
		}
		if *choice.InstanceID == value || choice.ServiceName == value {
			matched = append(matched, choice)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return client.ServiceDisconnectionNamesChoice{}, withExitCode(exitUsage, fmt.Errorf(
			"nothing changed: --names-from %s is not one of the services the review offers to give the conventional connection names; choose %s",
			value, serviceNamesFromChoices(choices)))
	default:
		ids := make([]string, 0, len(matched))
		for _, choice := range matched {
			ids = append(ids, *choice.InstanceID)
		}
		return client.ServiceDisconnectionNamesChoice{}, withExitCode(exitUsage, fmt.Errorf(
			"nothing changed: %d of the services the review offers are named %s; pass the id of the one you mean (%s)",
			len(matched), value, strings.Join(ids, ", ")))
	}
}

// serviceNamesFromChoices spells the --names-from values a review offers: each
// service by name (by id where two share a name), then none.
func serviceNamesFromChoices(choices []client.ServiceDisconnectionNamesChoice) string {
	count := map[string]int{}
	for _, choice := range choices {
		if !choice.None {
			count[choice.ServiceName]++
		}
	}
	values := make([]string, 0, len(choices)+1)
	for _, choice := range choices {
		if choice.None || choice.InstanceID == nil {
			continue
		}
		value := choice.ServiceName
		if value == "" || value == serviceNamesFromNone || count[value] > 1 {
			value = *choice.InstanceID
		}
		values = append(values, value)
	}
	values = append(values, serviceNamesFromNone)
	return strings.Join(values, ", ")
}

// serviceNamesChoiceRecorded reports whether a review prepared with a choice
// carries exactly that choice.
func serviceNamesChoiceRecorded(names client.ServiceDisconnectionNames, request client.ServiceDisconnectionRequest) bool {
	choice := names.Choice
	if choice == nil {
		return false
	}
	if request.ConnectionNamesNone {
		return choice.None
	}
	return !choice.None && choice.InstanceID != nil && request.ConnectionNamesInstanceID != nil &&
		*choice.InstanceID == *request.ConnectionNamesInstanceID
}

// serviceConventionalNames spells the protocol's conventional names a review
// is about, for a sentence.
func serviceConventionalNames(names client.ServiceDisconnectionNames) string {
	if len(names.Conventional) == 0 {
		return "the conventional connection names"
	}
	return strings.Join(names.Conventional, ", ")
}

// serviceDisconnectionLine prints one labelled statement of a review; an
// empty statement is left out rather than shown blank.
func serviceDisconnectionLine(out io.Writer, label string, statement string) {
	if strings.TrimSpace(statement) == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "  %-18s%s\n", label+":", statement)
}

// printServiceDisconnectionPlan shows a disconnection review: every statement
// of the plan as the platform wrote it, the choices for the conventional
// names when the review asks for one, the pods that read the connection
// Secret now, what stays, and the digest that is confirmed.
func printServiceDisconnectionPlan(out io.Writer, names *serviceNames, consumer client.ServiceInstanceConsumer, disconnection client.ServiceDisconnection) {
	plan := disconnection.Plan
	_, _ = fmt.Fprintf(out, "Disconnection review %s of service %q (generation %d)\n", disconnection.ID, plan.ServiceName, plan.Generation)
	engine := plan.Protocol
	if plan.Engine != "" && plan.Protocol != "" {
		engine = fmt.Sprintf("%s (%s)", plan.Engine, plan.Protocol)
	} else if plan.Engine != "" {
		engine = plan.Engine
	}
	if engine == "" {
		engine = "service"
	}
	serviceDisconnectionLine(out, "Service", fmt.Sprintf("%s on cluster %s", engine, names.clusterWithID(plan.ClusterID)))
	consumerCluster := consumer.ClusterName
	if consumerCluster == "" {
		consumerCluster = names.cluster(plan.Consumer.ClusterID)
	}
	if !plan.OnServiceCluster {
		consumerCluster += ", not the service's cluster"
	}
	serviceDisconnectionLine(out, "Application", fmt.Sprintf("%s, namespace %s on cluster %s",
		serviceConsumerLabel(names, consumer), plan.Consumer.Namespace, consumerCluster))
	state := disconnection.State
	if disconnection.State == "pending" || disconnection.State == "expired" {
		state += " (open until " + disconnection.ExpiresAt + ")"
	}
	serviceDisconnectionLine(out, "State", state)
	_, _ = fmt.Fprintln(out)

	connectionNames := plan.ConnectionNames
	serviceDisconnectionLine(out, "Variables", connectionNames.Statement)
	if connectionNames.ChoiceRequired && connectionNames.Choice == nil {
		_, _ = fmt.Fprintf(out, "  %-18sChoose who gives %s from now on, with --names-from:\n", "", serviceConventionalNames(connectionNames))
		for _, choice := range connectionNames.Choices {
			if choice.None || choice.InstanceID == nil {
				continue
			}
			note := ""
			if choice.Installing {
				note = ", still being set up: a new, empty service"
			}
			_, _ = fmt.Fprintf(out, "  %-18s  %s (%s%s)\n", "", choice.ServiceName, *choice.InstanceID, note)
		}
		_, _ = fmt.Fprintf(out, "  %-18s  %s (no service gives them)\n", "", serviceNamesFromNone)
	}
	serviceDisconnectionLine(out, "Credential", plan.Credential.Statement)
	serviceDisconnectionLine(out, "Cannot guarantee", plan.Credential.CannotGuarantee)
	serviceDisconnectionLine(out, "Rotation", plan.Rotation)
	serviceDisconnectionLine(out, "Secret", plan.ConnectionSecret.Statement)
	serviceDisconnectionLine(out, "Workloads", plan.Workloads.Statement)
	serviceDisconnectionLine(out, "Running pods", serviceDisconnectionRunningPods(disconnection))
	network := plan.Network.Statement
	if plan.Network.Narrows && !plan.Network.KeepNetworkAccess && network != "" {
		network += " (--keep-network-access keeps it.)"
	}
	serviceDisconnectionLine(out, "Network", network)
	serviceDisconnectionLine(out, "Data", plan.Data)
	serviceDisconnectionLine(out, "Timing", plan.Timing.Statement)
	if len(plan.Kept) > 0 {
		_, _ = fmt.Fprintln(out, "  What stays:")
		for _, kept := range plan.Kept {
			_, _ = fmt.Fprintf(out, "    - %s\n", kept)
		}
	}
	serviceDisconnectionLine(out, "Digest", disconnection.Digest)
}

// serviceDisconnectionRunningPods says which pods read the connection Secret
// when the disconnection was served. Nothing is said once it has settled or
// for an environment on another cluster, where the platform reads none; on
// the service's cluster a missing answer is said to be one, never read as no
// pods.
func serviceDisconnectionRunningPods(disconnection client.ServiceDisconnection) string {
	pods := disconnection.RunningPods
	if pods == nil {
		if disconnection.Plan.OnServiceCluster && disconnection.State != "settled" {
			return "not known: Ankra could not read the pods that use the connection Secret now"
		}
		return ""
	}
	if pods.Count == 0 && len(pods.Names) == 0 {
		return "no pod reads the connection Secret now"
	}
	count := max(pods.Count, len(pods.Names))
	named := strings.Join(pods.Names, ", ")
	if more := count - len(pods.Names); more > 0 {
		if named == "" {
			named = fmt.Sprintf("%d not named", more)
		} else {
			named += fmt.Sprintf(" and %d more", more)
		}
	}
	noun := "pods read"
	if count == 1 {
		noun = "pod reads"
	}
	return fmt.Sprintf("%d %s the connection Secret now: %s", count, noun, named)
}

// serviceDisconnectionPhaseMeanings say in plain words what each phase the
// platform answers today waits for. A phase not listed here, added by a later
// platform, prints as sent, without a meaning.
var serviceDisconnectionPhaseMeanings = map[string]string{
	client.ServiceDisconnectionDraining: "Ankra keeps the connection Secret until no pod and no deployed workload reads it; " +
		"a deploy of the application rolls the workloads Ankra attached it to, and a workload that refers to it by hand keeps it waiting until someone changes it",
	client.ServiceDisconnectionRemoving: "the connection Secret is being removed; Ankra waits to see it gone",
	client.ServiceDisconnectionRevoking: "the namespace's access to the service's port is being removed where the review said it would be; then the disconnection finishes",
}

// printServiceDisconnectionReceipt shows a confirmed disconnection: its state
// and phase with what the phase waits for, what a settled one observed, and
// where to follow it.
func printServiceDisconnectionReceipt(out io.Writer, serviceName string, label string, disconnection client.ServiceDisconnection) {
	_, _ = fmt.Fprintf(out, "\nApplication %s is disconnected from service %q (disconnection %s, confirmed %s).\n",
		label, serviceName, disconnection.ID, valueOr(disconnection.ConfirmedAt, "-"))
	state := disconnection.State
	if disconnection.Phase != nil && *disconnection.Phase != "" {
		state += ", phase " + *disconnection.Phase
	}
	serviceDisconnectionLine(out, "State", state)
	if disconnection.Phase != nil {
		if meaning, known := serviceDisconnectionPhaseMeanings[*disconnection.Phase]; known {
			serviceDisconnectionLine(out, "Waiting", meaning)
		}
	}
	if outcome := disconnection.Outcome; outcome != nil {
		network := outcome.Network
		if outcome.NetworkReason != "" {
			network += " (" + outcome.NetworkReason + ")"
		}
		if len(outcome.RemovedNamespaces) > 0 {
			network += ", namespaces taken off the port: " + strings.Join(outcome.RemovedNamespaces, ", ")
		}
		serviceDisconnectionLine(out, "Secret", outcome.ConnectionSecret+" (observed "+outcome.ObservedAt+")")
		serviceDisconnectionLine(out, "Network", network)
	}
	serviceDisconnectionLine(out, "Running pods", serviceDisconnectionRunningPods(disconnection))
	if disconnection.State != "settled" {
		_, _ = fmt.Fprintf(out, "Ankra finishes the disconnection by itself. 'ankra services get %s' shows it until it has finished.\n", serviceName)
	}
}

// printServiceDisconnectionsInProgress shows, under a service's consumers,
// the application environments a confirmed disconnection is still taking
// off it: the phase, since when, and what it waits for. A list the platform
// could not read is said to be unknown, never read as none; a platform from
// before disconnections sends nothing and prints nothing.
func printServiceDisconnectionsInProgress(out io.Writer, names *serviceNames, disconnections *client.ServiceInstanceDisconnections) {
	if disconnections == nil {
		return
	}
	if disconnections.State == client.ServiceDisconnectionsUnknown {
		_, _ = fmt.Fprintln(out, "  Disconnecting: not known - Ankra could not read the disconnections in progress")
		return
	}
	if len(disconnections.Items) == 0 {
		if disconnections.State != client.ServiceDisconnectionsKnown && disconnections.State != "" {
			_, _ = fmt.Fprintf(out, "  Disconnecting: %s\n", disconnections.State)
		}
		return
	}
	heading := "  Disconnecting:"
	if disconnections.State != client.ServiceDisconnectionsKnown && disconnections.State != "" {
		heading += " (" + disconnections.State + ")"
	}
	_, _ = fmt.Fprintln(out, heading)
	for _, item := range disconnections.Items {
		_, _ = fmt.Fprintf(out, "    %s  application %s, cluster %s, namespace %s (disconnection %s, confirmed %s)\n", item.ConsumerID,
			names.application(item.ApplicationID), names.cluster(item.ClusterID), item.Namespace, item.ID, item.RequestedAt)
		phase := item.Phase
		if item.PhaseChangedAt != "" {
			phase += " since " + item.PhaseChangedAt
		}
		_, _ = fmt.Fprintf(out, "      Phase:    %s\n", phase)
		if meaning, known := serviceDisconnectionPhaseMeanings[item.Phase]; known {
			_, _ = fmt.Fprintf(out, "      Waiting:  %s\n", meaning)
		}
		if item.KeepsConnectionSecret {
			_, _ = fmt.Fprintln(out, "      Secret:   kept: every deploy of the application renders it again, unchanged, for the pods started before the disconnection")
		}
	}
}
