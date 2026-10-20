package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// Access gateway status deliberately reports only configuration and readiness.
// It never requests secret values; the cluster API returns only their digests.
type accessGatewayDeployment struct {
	Name            string `json:"name"`
	ReadyReplicas   int    `json:"ready_replicas"`
	DesiredReplicas int    `json:"desired_replicas"`
	CredentialRef   bool   `json:"credential_ref"`
	BrokerURL       string `json:"broker_url,omitempty"`
	BrokerURLValid  bool   `json:"broker_url_valid,omitempty"`
	Ready           bool   `json:"ready"`
}

type accessGatewayReport struct {
	PlatformCluster       string                    `json:"platform_cluster"`
	RelayCluster          string                    `json:"relay_cluster"`
	PlatformDeployments   []accessGatewayDeployment `json:"platform_deployments"`
	RelayDeployment       accessGatewayDeployment   `json:"relay_deployment"`
	PlatformSecretPresent bool                      `json:"platform_secret_present"`
	RelaySecretPresent    bool                      `json:"relay_secret_present"`
	Ready                 bool                      `json:"ready"`
}

const accessGatewaySecretName = "ankra-access-gate"

func accessGatewayResource(clusterID, kind, namespace, name string) (map[string]interface{}, *client.ResourceResponseItem, error) {
	group := ""
	if kind == "Deployment" {
		group = "apps"
	}
	response, err := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{{Kind: kind, Group: group, Namespace: namespace, Name: name, Version: "v1"}},
		SkipCache:        true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s %s/%s: %w", kind, namespace, name, err)
	}
	if response == nil || len(response.ResourceResponses) != 1 {
		return nil, nil, fmt.Errorf("reading %s %s/%s: no response", kind, namespace, name)
	}
	item := &response.ResourceResponses[0]
	if item.CacheMetadata != nil && item.CacheMetadata.ServedFromCache {
		return nil, nil, fmt.Errorf("reading %s %s/%s: live cluster state unavailable", kind, namespace, name)
	}
	if item.NotFound || isAgentNotFoundText(*item) {
		return nil, item, nil
	}
	if err := failedReadError(*item, fmt.Sprintf("%s %s/%s", kind, namespace, name)); err != nil {
		return nil, nil, err
	}
	if len(item.Items) == 0 {
		return nil, item, nil
	}
	object, ok := item.Items[0].(map[string]interface{})
	if !ok || len(item.Items) != 1 {
		return nil, nil, fmt.Errorf("reading %s %s/%s: invalid object", kind, namespace, name)
	}
	return object, item, nil
}

func accessGatewayEnv(object map[string]interface{}, name string) map[string]interface{} {
	spec, _ := getNestedMap(object, "spec")
	template, _ := getNestedMap(spec, "template")
	podSpec, _ := getNestedMap(template, "spec")
	containers, _ := podSpec["containers"].([]interface{})
	for _, container := range containers {
		entry, _ := container.(map[string]interface{})
		environment, _ := entry["env"].([]interface{})
		for _, variable := range environment {
			field, _ := variable.(map[string]interface{})
			if field["name"] == name {
				return field
			}
		}
	}
	return nil
}

func accessGatewayReplicaCount(object map[string]interface{}, section, field string, defaultValue int) int {
	value := getNestedString(object, section, field)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func inspectAccessGatewayDeployment(object map[string]interface{}, name string, relay bool) accessGatewayDeployment {
	status := accessGatewayDeployment{Name: name}
	if object == nil {
		return status
	}
	status.DesiredReplicas = accessGatewayReplicaCount(object, "spec", "replicas", 1)
	status.ReadyReplicas = accessGatewayReplicaCount(object, "status", "readyReplicas", 0)
	credential := accessGatewayEnv(object, "ANKRA_ACCESS_GATE_TOKEN")
	valueFrom, _ := getNestedMap(credential, "valueFrom")
	secretRef, _ := getNestedMap(valueFrom, "secretKeyRef")
	status.CredentialRef = secretRef["name"] == accessGatewaySecretName && secretRef["key"] == "token"
	status.Ready = status.DesiredReplicas > 0 && status.ReadyReplicas >= status.DesiredReplicas && status.CredentialRef
	if relay {
		broker := accessGatewayEnv(object, "ANKRA_ACCESS_GATE_URL")
		status.BrokerURL, _ = broker["value"].(string)
		parsed, err := url.Parse(status.BrokerURL)
		status.BrokerURLValid = err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
		status.Ready = status.Ready && status.BrokerURLValid
	}
	return status
}

func readAccessGatewayReport(platformID, relayID string) (accessGatewayReport, error) {
	report := accessGatewayReport{PlatformCluster: platformID, RelayCluster: relayID}
	for _, name := range []string{"ankra-cluster-api", "ankra-cluster-api-go"} {
		object, _, err := accessGatewayResource(platformID, "Deployment", "platform", name)
		if err != nil {
			return report, err
		}
		report.PlatformDeployments = append(report.PlatformDeployments, inspectAccessGatewayDeployment(object, name, false))
	}
	relay, _, err := accessGatewayResource(relayID, "Deployment", "ankra-tunnel-relay", "ankra-tunnel-relay")
	if err != nil {
		return report, err
	}
	report.RelayDeployment = inspectAccessGatewayDeployment(relay, "ankra-tunnel-relay", true)
	platformSecret, _, err := accessGatewayResource(platformID, "Secret", "platform", accessGatewaySecretName)
	if err != nil {
		return report, err
	}
	relaySecret, _, err := accessGatewayResource(relayID, "Secret", "ankra-tunnel-relay", accessGatewaySecretName)
	if err != nil {
		return report, err
	}
	report.PlatformSecretPresent = platformSecret != nil
	report.RelaySecretPresent = relaySecret != nil
	report.Ready = report.PlatformSecretPresent && report.RelaySecretPresent && report.RelayDeployment.Ready
	for _, deployment := range report.PlatformDeployments {
		report.Ready = report.Ready && deployment.Ready
	}
	return report, nil
}

func newAccessGatewayStatusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Check Platform and Cloud access gateway readiness without revealing credentials",
		Long:  "Read both clusters live through Ankra. Check the API and relay deployments, broker URL and Secret references without revealing credentials. Redacted Secret digests are cluster-keyed and cannot prove token equality. This does not perform a browser SSO sign-in or read a Cloud DNS zone policy.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			platformName, _ := cmd.Flags().GetString("platform-cluster")
			relayName, _ := cmd.Flags().GetString("relay-cluster")
			if strings.TrimSpace(platformName) == "" || strings.TrimSpace(relayName) == "" {
				return withExitCode(exitUsage, fmt.Errorf("both --platform-cluster and --relay-cluster are required"))
			}
			platformID, err := resolveClusterID(platformName)
			if err != nil {
				return err
			}
			relayID, err := resolveClusterID(relayName)
			if err != nil {
				return err
			}
			report, err := readAccessGatewayReport(platformID, relayID)
			if err != nil {
				return err
			}
			if rendered, renderErr := renderStructured(cmd, report); rendered || renderErr != nil {
				return renderErr
			}
			state := "not ready"
			if report.Ready {
				state = "ready"
			}
			if _, writeError := fmt.Fprintf(cmd.OutOrStdout(), "Access gateway: %s\n", state); writeError != nil {
				return writeError
			}
			for _, deployment := range report.PlatformDeployments {
				if _, writeError := fmt.Fprintf(cmd.OutOrStdout(), "  Platform %s: %d/%d ready; token reference %t\n", deployment.Name, deployment.ReadyReplicas, deployment.DesiredReplicas, deployment.CredentialRef); writeError != nil {
					return writeError
				}
			}
			if _, writeError := fmt.Fprintf(cmd.OutOrStdout(), "  Cloud relay: %d/%d ready; token reference %t; broker URL valid %t\n", report.RelayDeployment.ReadyReplicas, report.RelayDeployment.DesiredReplicas, report.RelayDeployment.CredentialRef, report.RelayDeployment.BrokerURLValid); writeError != nil {
				return writeError
			}
			_, writeError := fmt.Fprintf(cmd.OutOrStdout(), "  Secrets: Platform %t, relay %t; token equality not checked\n", report.PlatformSecretPresent, report.RelaySecretPresent)
			return writeError
		},
	}
	command.Flags().String("platform-cluster", "", "Ankra cluster running the Platform API")
	command.Flags().String("relay-cluster", "", "Ankra cluster running the Cloud tunnel relay")
	registerStructuredOutputFlags(command)
	return command
}

func newAccessGatewayZonesCommand() *cobra.Command {
	command := &cobra.Command{Use: "zones", Short: "List Cloud DNS zones in this organisation", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(command.Context(), 60*time.Second)
			defer cancel()
			zones, listError := apiClient.ListCloudDNSZones(ctx)
			if listError != nil {
				return fmt.Errorf("list Cloud DNS zones: %w", listError)
			}
			if rendered, renderError := renderStructured(command, zones); rendered || renderError != nil {
				return renderError
			}
			for _, zone := range zones.Items {
				if _, writeError := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", zone.ID, zone.Name); writeError != nil {
					return writeError
				}
			}
			return nil
		}}
	registerStructuredOutputFlags(command)
	return command
}

func newAccessGatewayGetCommand() *cobra.Command {
	command := &cobra.Command{Use: "get <zone-id>", Short: "Show a Cloud DNS zone's access policy", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(command.Context(), 60*time.Second)
			defer cancel()
			policy, readError := apiClient.GetCloudDNSAccess(ctx, args[0])
			if readError != nil {
				return fmt.Errorf("get Cloud DNS access policy: %w", readError)
			}
			if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
				return renderError
			}
			if _, writeError := fmt.Fprintf(command.OutOrStdout(), "Mode: %s\nRoles: %s\n", policy.Mode, strings.Join(policy.Roles, ", ")); writeError != nil {
				return writeError
			}
			if policy.InheritedFrom != "" {
				if _, writeError := fmt.Fprintf(command.OutOrStdout(), "Inherited from: %s\n", policy.InheritedFrom); writeError != nil {
					return writeError
				}
			}
			return nil
		}}
	registerStructuredOutputFlags(command)
	return command
}

func newAccessGatewaySetCommand() *cobra.Command {
	command := &cobra.Command{Use: "set <zone-id>", Short: "Set a Cloud DNS zone's access policy", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			mode, _ := command.Flags().GetString("mode")
			roles, _ := command.Flags().GetStringSlice("role")
			if mode != "public" && mode != "organisation" {
				return withExitCode(exitUsage, fmt.Errorf("--mode must be public or organisation"))
			}
			if mode == "public" && len(roles) > 0 {
				return withExitCode(exitUsage, fmt.Errorf("--role requires --mode organisation"))
			}
			ctx, cancel := context.WithTimeout(command.Context(), 60*time.Second)
			defer cancel()
			policy, updateError := apiClient.UpdateCloudDNSAccess(ctx, args[0], client.CloudDNSAccessPolicy{Mode: mode, Roles: roles})
			if updateError != nil {
				return fmt.Errorf("set Cloud DNS access policy: %w", updateError)
			}
			if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
				return renderError
			}
			_, writeError := fmt.Fprintf(command.OutOrStdout(), "Access policy set to %s for zone %s.\n", policy.Mode, args[0])
			return writeError
		}}
	command.Flags().String("mode", "", "Access mode: public or organisation")
	command.Flags().StringSlice("role", nil, "Allowed organisation role; repeat to allow several (empty means all members)")
	_ = command.MarkFlagRequired("mode")
	registerStructuredOutputFlags(command)
	return command
}

func init() {
	group := &cobra.Command{Use: "access-gateway", Short: "Inspect the Cloud proxy's Platform organisation access gate"}
	group.AddCommand(newAccessGatewayStatusCommand())
	group.AddCommand(newAccessGatewayZonesCommand(), newAccessGatewayGetCommand(), newAccessGatewaySetCommand())
	orgCmd.AddCommand(group)
}
