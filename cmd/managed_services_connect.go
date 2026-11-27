package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Connecting one more application environment to a managed service that
// already runs (cluster#4559). It is one step on the platform: the
// environment's binding joins the service's consumers at the generation the
// caller read. The service is not redeployed and nothing is reviewed first,
// because nothing is removed and nothing restarts. The platform decides which
// credential the application gets unless --credential names one, and the
// command says which it got (ankra-otrxtu.9.5).

func newServicesConnectCommand() *cobra.Command {
	connectCommand := &cobra.Command{
		Use:   "connect <service>",
		Short: "Connect one more application to a managed service that already runs",
		Long: `Connect an application environment to a managed service that already runs, so
the application uses that service instead of getting one of its own.

--consumer is the binding of the application's namespace, from 'ankra services
consumers bind'. The namespace must be on the service's own cluster and in its
data boundary.

The service is not redeployed and nothing restarts. The application joins the
service's consumers and receives the connection as environment variables
(DATABASE_URL, ...) the way every consumer does: without a redeploy while none
of its running pods reads the connection, otherwise at its next deploy.
'ankra services get <service>' shows its pickup.

Which credential the application connects with is printed when it is
connected. On a PostgreSQL service that can carry one, Ankra gives the
application a database role of its own: it acts as the database owner in the
shared database, so the application reads and writes the same tables, and
disconnecting the application stops the role logging in and removes it.
Otherwise the application gets the service's owner credential, shared by
every application on it, which disconnecting one application cannot revoke.
--credential owner asks for the shared owner credential instead, and
--credential shared_role asks for a role and is refused, with the reason,
where the service cannot carry one. An application that is already connected
keeps the credential it has.

Connecting is refused while the service is still being installed, while it is
being retired, and when the service changed after this command read it: run
the command again.

A service created from another service's backups ('ankra services setup
--from') starts with no applications. This is how it gets them.

A name that runs on more than one cluster needs --cluster.`,
		Example: "  ankra services consumers bind --application storefront --cluster prod --namespace storefront\n" +
			"  ankra services connect orders-db --consumer 0b7c4d19-5f6a-4b8c-9d0e-1f2a3b4c5d6f\n" +
			"  ankra services connect orders-db --consumer 0b7c4d19-5f6a-4b8c-9d0e-1f2a3b4c5d6f --credential owner\n" +
			"  ankra services connect orders-db --cluster prod --consumer 0b7c4d19-5f6a-4b8c-9d0e-1f2a3b4c5d6f -o json",
		Args: cobra.ExactArgs(1),
		RunE: runServicesConnect,
	}
	connectCommand.Flags().String("consumer", "", "Consumer binding id of the application's namespace ('ankra services consumers bind')")
	connectCommand.Flags().String("cluster", "", "The cluster the service runs on, when its name is not unique (name or id)")
	connectCommand.Flags().String("credential", "",
		"The credential the application connects with: owner (the service's shared owner credential) or shared_role "+
			"(a database role of its own); left out, Ankra chooses")
	_ = connectCommand.MarkFlagRequired("consumer")
	registerStructuredOutputFlags(connectCommand)
	return connectCommand
}

func runServicesConnect(command *cobra.Command, arguments []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	consumerID, _ := command.Flags().GetString("consumer")
	consumerID = strings.TrimSpace(consumerID)
	if !looksLikeUUID(consumerID) {
		return withExitCode(exitUsage, fmt.Errorf("--consumer %q: expected a consumer binding id "+
			"('ankra services consumers bind' prints it)", consumerID))
	}
	credential, _ := command.Flags().GetString("credential")
	credential = strings.TrimSpace(credential)
	if command.Flags().Changed("credential") && credential != client.ServiceCredentialOwner && credential != client.ServiceCredentialSharedRole {
		return withExitCode(exitUsage, fmt.Errorf("--credential %q: expected %s or %s",
			credential, client.ServiceCredentialOwner, client.ServiceCredentialSharedRole))
	}
	instance, resolveError := resolveServiceInstance(command, arguments[0])
	if resolveError != nil {
		return resolveError
	}
	if instance.ReleasedAt != nil {
		return withExitCode(exitBlocked, fmt.Errorf("service %q was retired on %s, so no application can be connected to it",
			instance.Name, *instance.ReleasedAt))
	}
	connected, connectError := apiClient.ConnectServiceConsumer(command.Context(), instance.ID,
		client.ServiceInstanceConsumerRequest{ConsumerID: consumerID, ExpectedGeneration: instance.Generation, Credential: credential})
	if connectError != nil {
		return serviceConnectError(connectError, instance.Name)
	}
	if rendered, renderError := renderStructured(command, connected); rendered || renderError != nil {
		return renderError
	}
	// The service's own record names its cluster, so no second read is
	// needed to say where it runs; a platform that sends no name gets the id.
	clusterLabel := connected.ClusterName
	if clusterLabel == "" {
		clusterLabel = connected.ClusterID
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "The application is connected to service %q on cluster %s.\n", connected.Name, clusterLabel)
	if line := serviceConnectedCredentialLine(connected, consumerID); line != "" {
		_, _ = fmt.Fprintln(out, line)
	}
	_, _ = fmt.Fprintf(out, "Ankra delivers the connection without a redeploy while none of the application's running pods reads it; "+
		"otherwise it is left to the application's next deploy. Follow its pickup with:\n  ankra services get %s --cluster %s\n",
		connected.Name, clusterLabel)
	return nil
}

// serviceConnectError adds the way forward to a connection the platform
// refused as a conflict. The platform's own sentence says why; two of its
// conflicts record the application and only fail to open the network policy,
// so the answer never claims that nothing changed.
func serviceConnectError(connectError error, serviceName string) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(connectError, &unexpected) && unexpected.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w - 'ankra services get %s' shows which applications the service has now", connectError, serviceName)
	}
	return connectError
}

// serviceConnectedCredentialLine says which credential the connected
// application got, from the consumer the platform answered. An engine whose
// consumers all share one credential, and a platform from before
// per-application credentials, answer none: nothing is said, which is not a
// claim either way.
func serviceConnectedCredentialLine(connected *client.ServiceInstance, consumerID string) string {
	for _, consumer := range connected.Consumers {
		if consumer.ID != consumerID || consumer.Credential == nil {
			continue
		}
		return "Credential: " + serviceCredentialSummary(*consumer.Credential)
	}
	return ""
}

// serviceCredentialSummary says what a consumer's credential is in one line:
// the platform's own description, and for a role that is not ready yet, that
// it is still being created and what it waits for.
func serviceCredentialSummary(credential client.ServiceConsumerCredential) string {
	if credential.Mode == client.ServiceCredentialUnknown {
		return "unknown: Ankra could not read which credential it connects with; 'ankra services get' shows it once it can"
	}
	summary := credential.Description
	if summary == "" {
		summary = credential.Mode
	}
	if credential.Mode == client.ServiceCredentialSharedRole && credential.State == "provisioning" {
		summary += " Ankra is still creating the role; the connection is delivered once it is ready."
		if credential.Waiting != "" {
			summary += " Waiting for: " + credential.Waiting
		}
	}
	return summary
}
