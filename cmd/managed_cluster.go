package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type managedProviderRegistration struct {
	Name             string
	DisplayName      string
	CredentialName   string
	SupportsCreate   bool
	SupportsDiscover bool
}

var managedProviderRegistry = map[string]managedProviderRegistration{
	"kapsule": {
		Name:             "kapsule",
		DisplayName:      "Scaleway Kapsule",
		CredentialName:   "Scaleway credential",
		SupportsCreate:   true,
		SupportsDiscover: true,
	},
}

type kapsuleClusterOptionsFile struct {
	PrivateNetworkID string `json:"private_network_id" yaml:"private_network_id"`
	PrivateEndpoint  *bool  `json:"private_endpoint,omitempty" yaml:"private_endpoint,omitempty"`
}

type managedCreateFile struct {
	Name                 string                          `json:"name" yaml:"name"`
	Description          *string                         `json:"description,omitempty" yaml:"description,omitempty"`
	CredentialID         string                          `json:"credential_id" yaml:"credential_id"`
	Location             string                          `json:"location" yaml:"location"`
	KubernetesVersion    *string                         `json:"kubernetes_version,omitempty" yaml:"kubernetes_version,omitempty"`
	CNI                  *string                         `json:"cni,omitempty" yaml:"cni,omitempty"`
	NodePools            []client.ManagedNodePoolRequest `json:"node_pools" yaml:"node_pools"`
	ClusterPlan          *string                         `json:"cluster_plan,omitempty" yaml:"cluster_plan,omitempty"`
	HA                   *bool                           `json:"ha,omitempty" yaml:"ha,omitempty"`
	GitopsCredentialName *string                         `json:"gitops_credential_name,omitempty" yaml:"gitops_credential_name,omitempty"`
	GitopsRepository     *string                         `json:"gitops_repository,omitempty" yaml:"gitops_repository,omitempty"`
	GitopsBranch         *string                         `json:"gitops_branch,omitempty" yaml:"gitops_branch,omitempty"`
	Kapsule              *kapsuleClusterOptionsFile      `json:"kapsule,omitempty" yaml:"kapsule,omitempty"`
}

func readManagedRequestFile(command *cobra.Command) ([]byte, string, error) {
	path, _ := command.Flags().GetString("file")
	if strings.TrimSpace(path) == "" {
		return nil, "", withExitCode(exitUsage, errors.New("--file is required (use --file - for stdin)"))
	}
	var (
		data []byte
		err  error
	)
	source := path
	if path == "-" {
		source = "stdin"
		data, err = io.ReadAll(io.LimitReader(command.InOrStdin(), 2*1024*1024))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, source, fmt.Errorf("reading managed request from %s: %w", source, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, source, fmt.Errorf("managed request from %s is empty", source)
	}
	return data, source, nil
}

func decodeManagedCreateFile(data []byte, source string) (json.RawMessage, managedCreateFile, error) {
	var document managedCreateFile
	trimmed := bytes.TrimSpace(data)
	isJSON := len(trimmed) > 0 && (trimmed[0] == '{' || strings.EqualFold(filepath.Ext(source), ".json"))
	if isJSON {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&document); err != nil {
			return nil, document, formatManagedJSONError(source, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				err = errors.New("multiple JSON values")
			}
			return nil, document, fmt.Errorf("%s: invalid JSON after request document: %w", source, err)
		}
		if err := validateManagedCreateFile(document, source); err != nil {
			return nil, document, err
		}
		// Return the original JSON bytes. This preserves explicit nulls, number
		// spelling, ordering, and omitted-vs-zero semantics exactly.
		return json.RawMessage(trimmed), document, nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return nil, document, fmt.Errorf("%s: invalid strict YAML request: %w", source, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple YAML documents")
		}
		return nil, document, fmt.Errorf("%s: invalid YAML after request document: %w", source, err)
	}
	if err := validateManagedCreateFile(document, source); err != nil {
		return nil, document, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, document, fmt.Errorf("%s: converting YAML request to JSON: %w", source, err)
	}
	return encoded, document, nil
}

func formatManagedJSONError(source string, err error) error {
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return fmt.Errorf("%s: invalid JSON at byte %d: %w", source, syntaxError.Offset, err)
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		location := "$"
		if typeError.Field != "" {
			location += "." + typeError.Field
		}
		return fmt.Errorf("%s: validation error at %s (byte %d): %w", source, location, typeError.Offset, err)
	}
	const unknownPrefix = `json: unknown field "`
	if message := err.Error(); strings.HasPrefix(message, unknownPrefix) && strings.HasSuffix(message, `"`) {
		field := strings.TrimSuffix(strings.TrimPrefix(message, unknownPrefix), `"`)
		return fmt.Errorf("%s: validation error at $.%s: unknown field", source, field)
	}
	return fmt.Errorf("%s: invalid strict JSON request: %w", source, err)
}

func validateManagedCreateFile(document managedCreateFile, source string) error {
	required := []struct {
		location string
		value    string
	}{
		{"name", document.Name},
		{"credential_id", document.CredentialID},
		{"location", document.Location},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("%s: validation error at $.%s: field is required and must not be empty", source, item.location)
		}
	}
	if len(document.NodePools) == 0 {
		return fmt.Errorf("%s: validation error at $.node_pools: at least one pool is required", source)
	}
	for index, pool := range document.NodePools {
		if strings.TrimSpace(pool.Name) == "" {
			return fmt.Errorf("%s: validation error at $.node_pools[%d].name: must not be empty", source, index)
		}
		if strings.TrimSpace(pool.Size) == "" {
			return fmt.Errorf("%s: validation error at $.node_pools[%d].size: must not be empty", source, index)
		}
		if pool.Count != nil && *pool.Count < 1 {
			return fmt.Errorf("%s: validation error at $.node_pools[%d].count: must be at least 1", source, index)
		}
	}
	if document.CNI != nil {
		switch *document.CNI {
		case "cilium", "cilium_native", "calico":
		default:
			return fmt.Errorf("%s: validation error at $.cni: expected cilium, cilium_native, or calico", source)
		}
	}
	if document.Kapsule == nil || strings.TrimSpace(document.Kapsule.PrivateNetworkID) == "" {
		return fmt.Errorf("%s: validation error at $.kapsule.private_network_id: field is required", source)
	}
	return nil
}

func managedCommandContext(command *cobra.Command) (context.Context, context.CancelFunc, error) {
	timeout, err := command.Flags().GetDuration("timeout")
	if err != nil || timeout <= 0 {
		return nil, nil, withExitCode(exitUsage, errors.New("--timeout must be greater than zero"))
	}
	ctx, cancel := context.WithTimeout(command.Context(), timeout)
	return ctx, cancel, nil
}

func renderManagedValue(command *cobra.Command, value any, human func()) error {
	if handled, err := renderStructured(command, value); handled || err != nil {
		return err
	}
	human()
	return nil
}

func newManagedProviderCommand(registration managedProviderRegistration) *cobra.Command {
	provider := registration.Name
	root := &cobra.Command{
		Use:   provider,
		Short: "Manage " + registration.DisplayName + " clusters",
		Long: `Manage cloud-provider Kubernetes through Ankra's provider-generic API.

Create and preflight accept strict YAML/JSON request files so provider-specific
fields remain typed without turning every capability into a global flag.`,
	}
	root.PersistentFlags().Duration("timeout", 2*time.Minute, "API request timeout")

	options := &cobra.Command{
		Use: "options", Short: "List credential-scoped Kapsule options and capabilities",
		RunE: func(command *cobra.Command, _ []string) error {
			credentialID, _ := command.Flags().GetString("credential-id")
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedOptions(ctx, provider, credentialID)
			if err != nil {
				return fmt.Errorf("listing %s options: %w", registration.DisplayName, err)
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("%s options: %d locations, %d versions, %d sizes\n", registration.DisplayName, len(result.Locations), len(result.VersionOptions), len(result.Sizes))
				if result.Incomplete {
					fmt.Printf("Incomplete regions: %s\n", strings.Join(result.IncompleteRegions, ", "))
				}
			})
		},
	}
	options.Flags().String("credential-id", "", registration.CredentialName+" ID (required)")
	_ = options.MarkFlagRequired("credential-id")

	preflight := &cobra.Command{
		Use: "preflight", Short: "Preflight a strict managed-cluster request file",
		RunE: func(command *cobra.Command, _ []string) error {
			data, source, err := readManagedRequestFile(command)
			if err != nil {
				return err
			}
			request, _, err := decodeManagedCreateFile(data, source)
			if err != nil {
				return withExitCode(exitUsage, err)
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedPreflight(ctx, provider, request)
			if err != nil {
				return fmt.Errorf("preflighting %s cluster: %w", registration.DisplayName, err)
			}
			renderErr := renderManagedValue(command, result, func() {
				for _, item := range result.Items {
					fmt.Printf("%-5s %-24s %s\n", strings.ToUpper(item.Status), item.Check, item.Message)
				}
			})
			if renderErr != nil {
				return renderErr
			}
			if !result.CanProceed {
				return withExitCode(exitError, errors.New("preflight reported can_proceed=false"))
			}
			return nil
		},
	}
	preflight.Flags().StringP("file", "f", "", "Strict YAML/JSON request file, or - for stdin (required)")
	_ = preflight.MarkFlagRequired("file")

	create := &cobra.Command{
		Use: "create", Short: "Create a Kapsule cluster from a strict request file",
		RunE: func(command *cobra.Command, _ []string) error {
			data, source, err := readManagedRequestFile(command)
			if err != nil {
				return err
			}
			request, _, err := decodeManagedCreateFile(data, source)
			if err != nil {
				return withExitCode(exitUsage, err)
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			check, err := activeManagedK8sAPI().ManagedPreflight(ctx, provider, request)
			if err != nil {
				return fmt.Errorf("preflighting %s cluster: %w", registration.DisplayName, err)
			}
			if !check.CanProceed {
				return withExitCode(exitError, errors.New("preflight reported can_proceed=false; run the preflight command to inspect checks"))
			}
			result, err := activeManagedK8sAPI().ManagedCreate(ctx, provider, request)
			if err != nil {
				return fmt.Errorf("creating %s cluster: %w", registration.DisplayName, err)
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("%s cluster %q created (ID: %s, provenance: %s).\n", registration.DisplayName, result.Name, result.ClusterID, result.Provenance)
			})
		},
	}
	create.Flags().StringP("file", "f", "", "Strict YAML/JSON request file, or - for stdin (required)")
	_ = create.MarkFlagRequired("file")

	discover := &cobra.Command{
		Use: "discover", Short: "Discover provider clusters available for import",
		RunE: func(command *cobra.Command, _ []string) error {
			credentialID, _ := command.Flags().GetString("credential-id")
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedDiscover(ctx, provider, credentialID)
			if err != nil {
				return fmt.Errorf("discovering %s clusters: %w", registration.DisplayName, err)
			}
			return renderManagedValue(command, result, func() {
				for _, item := range result.Clusters {
					cni := "-"
					if item.CNI != nil {
						cni = *item.CNI
					}
					fmt.Printf("%s  %-24s location=%s status=%s nodes=%d cni=%s imported=%t\n",
						item.ProviderClusterID, item.Name, item.Location, item.Status, item.NodeCount, cni, item.AlreadyImported)
				}
				if result.Incomplete {
					fmt.Printf("Discovery incomplete in regions: %s\n", strings.Join(result.IncompleteRegions, ", "))
				}
			})
		},
	}
	discover.Flags().String("credential-id", "", registration.CredentialName+" ID (required)")
	_ = discover.MarkFlagRequired("credential-id")

	importCommand := &cobra.Command{
		Use: "import", Short: "Import a discovered provider cluster",
		RunE: func(command *cobra.Command, _ []string) error {
			credentialID, _ := command.Flags().GetString("credential-id")
			providerClusterID, _ := command.Flags().GetString("provider-cluster-id")
			name, _ := command.Flags().GetString("name")
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			discovery, err := activeManagedK8sAPI().ManagedDiscover(ctx, provider, credentialID)
			if err != nil {
				return fmt.Errorf("verifying discovery before import: %w", err)
			}
			if discovery.Incomplete {
				return fmt.Errorf("discovery is incomplete in regions %s; selection/import is blocked until discovery is complete",
					strings.Join(discovery.IncompleteRegions, ", "))
			}
			found := false
			for _, item := range discovery.Clusters {
				if item.ProviderClusterID == providerClusterID && !item.AlreadyImported {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("provider cluster %q was not found as an importable result; run discover again", providerClusterID)
			}
			request := client.ManagedImportRequest{CredentialID: credentialID, ProviderClusterID: providerClusterID}
			if name != "" {
				request.Name = &name
			}
			result, err := activeManagedK8sAPI().ManagedImport(ctx, provider, request)
			if err != nil {
				return fmt.Errorf("importing %s cluster: %w", registration.DisplayName, err)
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Imported %q as cluster %s (provenance: %s).\n", result.ProviderClusterID, result.ClusterID, result.Provenance)
			})
		},
	}
	importCommand.Flags().String("credential-id", "", registration.CredentialName+" ID (required)")
	importCommand.Flags().String("provider-cluster-id", "", "Opaque region-qualified provider cluster reference (required)")
	importCommand.Flags().String("name", "", "Override imported cluster name")
	_ = importCommand.MarkFlagRequired("credential-id")
	_ = importCommand.MarkFlagRequired("provider-cluster-id")

	status := &cobra.Command{
		Use: "status <cluster_id>", Short: "Read provider status, CNI, ownership, and pools", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedStatus(ctx, provider, args[0])
			if err != nil {
				return fmt.Errorf("reading managed cluster status: %w", err)
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Status: %s\nOwnership: %s\nProvider cluster: %s\nCNI: %s\nPools: %d\n",
					result.Status, result.Ownership, result.ProviderClusterID, optionalString(result.CNI), len(result.NodePools))
			})
		},
	}

	disconnect := &cobra.Command{
		Use: "disconnect <cluster_id>", Short: "Disconnect from Ankra without deleting Kapsule", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			force, _ := command.Flags().GetBool("force")
			yes, _ := command.Flags().GetBool("yes")
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			state, err := activeManagedK8sAPI().ManagedStatus(ctx, provider, args[0])
			if err != nil {
				return fmt.Errorf("reading ownership before disconnect: %w", err)
			}
			if err := confirmPrompt(command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Disconnect %s cluster %q (ownership %s)? The provider cluster is retained. [y/N]: ", registration.DisplayName, args[0], state.Ownership), yes); err != nil {
				return err
			}
			result, err := activeManagedK8sAPI().ManagedDisconnect(ctx, provider, args[0], force)
			if err != nil {
				return fmt.Errorf("disconnecting managed cluster: %w", err)
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Disconnected cluster %s; provider resources retained.\n", result.ClusterID)
			})
		},
	}
	disconnect.Flags().Bool("force", false, "Force disconnect through a non-idle state")
	disconnect.Flags().BoolP("yes", "y", false, "Skip confirmation")

	deleteProvider := &cobra.Command{
		Use: "delete-provider-cluster <cluster_id>", Short: "Delete Kapsule at Scaleway, then disconnect", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			force, _ := command.Flags().GetBool("force")
			yes, _ := command.Flags().GetBool("yes")
			retention, _ := command.Flags().GetString("retention-policy")
			if retention != "delete" && retention != "retain" {
				return withExitCode(exitUsage, errors.New("--retention-policy must be delete or retain"))
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			state, err := activeManagedK8sAPI().ManagedStatus(ctx, provider, args[0])
			if err != nil {
				return fmt.Errorf("reading ownership before provider deletion: %w", err)
			}
			if state.Ownership != client.ManagedProvenanceCreated && !force {
				return fmt.Errorf("cluster ownership is %s; provider deletion requires --force after verifying the provider cluster reference", state.Ownership)
			}
			if err := confirmPrompt(command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("DELETE provider cluster %q (%s, ownership %s, retention %s)? This cannot be undone. [y/N]: ",
					state.ProviderClusterID, args[0], state.Ownership, retention), yes); err != nil {
				return err
			}
			result, err := activeManagedK8sAPI().ManagedDeleteProviderCluster(ctx, provider, args[0], force, retention)
			if err != nil {
				return fmt.Errorf("deleting provider cluster: %w", err)
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Provider deletion initiated for %s (operation %s, retention %s).\n",
					result.ClusterID, optionalString(result.OperationID), retention)
			})
		},
	}
	deleteProvider.Flags().Bool("force", false, "Required for imported/unknown provenance and non-idle state")
	deleteProvider.Flags().BoolP("yes", "y", false, "Skip confirmation")
	deleteProvider.Flags().String("retention-policy", "delete", "Provider storage retention: delete or retain")

	pools := newManagedPoolsCommand(registration)
	upgrades := &cobra.Command{
		Use: "upgrades <cluster_id>", Short: "List provider-supported Kubernetes upgrades", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedUpgrades(ctx, provider, args[0])
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Current version: %s\n", optionalString(result.CurrentVersion))
				for _, item := range result.Upgrades {
					fmt.Printf("  %s (CNIs: %s)\n", item.Version, strings.Join(item.AvailableCNIs, ","))
				}
			})
		},
	}
	upgrade := &cobra.Command{
		Use: "upgrade <cluster_id> <version>", Short: "Upgrade Kapsule Kubernetes", Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			yes, _ := command.Flags().GetBool("yes")
			if err := confirmPrompt(command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Upgrade managed cluster %q to Kubernetes %s? [y/N]: ", args[0], args[1]), yes); err != nil {
				return err
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedUpgrade(ctx, provider, args[0], args[1])
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Upgrade to %s initiated (operation %s).\n", result.Version, result.OperationID)
			})
		},
	}
	upgrade.Flags().BoolP("yes", "y", false, "Skip confirmation")

	all := []*cobra.Command{options, preflight, create, discover, importCommand, status, disconnect, deleteProvider, pools, upgrades, upgrade}
	for _, command := range all {
		registerStructuredOutputFlags(command)
		root.AddCommand(command)
	}
	return root
}

func newManagedPoolsCommand(registration managedProviderRegistration) *cobra.Command {
	provider := registration.Name
	root := &cobra.Command{Use: "pool", Aliases: []string{"node-pool", "pools"}, Short: "Manage Kapsule node pools"}
	list := &cobra.Command{
		Use: "list <cluster_id>", Short: "List typed provider pools", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedListPools(ctx, provider, args[0])
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				for _, pool := range result.NodePools {
					fmt.Printf("%-20s provider_id=%s size=%s count=%d zone=%s status=%s\n",
						pool.Name, optionalString(pool.ProviderID), pool.Size, pool.Count, optionalString(pool.Zone), optionalString(pool.Status))
				}
			})
		},
	}
	catalog := &cobra.Command{
		Use: "catalog <cluster_id>", Short: "List cluster-scoped live pool catalog", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedPoolCatalog(ctx, provider, args[0])
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Pool catalog: %d sizes, %d zones, %d root-volume types\n", len(result.Sizes), len(result.Zones), len(result.RootVolumeTypes))
				if result.Incomplete {
					fmt.Printf("Incomplete regions: %s\n", strings.Join(result.IncompleteRegions, ", "))
				}
			})
		},
	}
	add := &cobra.Command{
		Use: "add <cluster_id>", Short: "Add a typed Kapsule pool", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			request, err := managedPoolRequestFromFlags(command)
			if err != nil {
				return err
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedAddPool(ctx, provider, args[0], request)
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Pool %q added (count %s).\n", result.NodePoolName, optionalInt(result.Count))
			})
		},
	}
	add.Flags().String("name", "", "Pool name (required)")
	add.Flags().String("size", "", "Kapsule node type (required)")
	add.Flags().Int("count", 1, "Initial node count")
	add.Flags().String("zone", "", "Pool zone")
	add.Flags().String("root-volume-type", "", "Root volume type")
	add.Flags().Int("root-volume-size-gb", 0, "Root volume size in GiB")
	add.Flags().String("security-group-id", "", "Security group ID")
	add.Flags().Bool("public-ip", false, "Assign public IPs")
	add.Flags().Bool("autohealing", true, "Enable autohealing")
	add.Flags().Bool("autoscaling", false, "Enable autoscaling")
	add.Flags().Int("autoscaling-min", 1, "Autoscaling minimum")
	add.Flags().Int("autoscaling-max", 5, "Autoscaling maximum")
	add.Flags().String("upgrade-policy", "", "Upgrade policy: surge, unavailable, or balanced")
	add.Flags().String("labels", "", "Comma-separated key=value labels")
	add.Flags().String("taints", "", "Comma-separated key=value:Effect taints")
	_ = add.MarkFlagRequired("name")
	_ = add.MarkFlagRequired("size")

	scale := &cobra.Command{
		Use: "scale <cluster_id> <pool_name> <count>", Short: "Scale a Kapsule pool", Args: cobra.ExactArgs(3),
		RunE: func(command *cobra.Command, args []string) error {
			count, err := strconv.Atoi(args[2])
			if err != nil || count < 1 {
				return withExitCode(exitUsage, errors.New("count must be an integer of at least 1"))
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedScalePool(ctx, provider, args[0], args[1], count)
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Pool %q scaled to %s.\n", result.NodePoolName, optionalInt(result.Count))
			})
		},
	}
	update := &cobra.Command{
		Use: "update <cluster_id> <pool_name>", Short: "Update mutable Kapsule pool settings", Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			request, err := managedPoolUpdateFromFlags(command)
			if err != nil {
				return err
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedUpdatePool(ctx, provider, args[0], args[1], request)
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Pool %q updated.\n", result.NodePoolName)
			})
		},
	}
	update.Flags().Int("count", 0, "Desired node count")
	update.Flags().Bool("autoscaling-enabled", false, "Enable or disable autoscaling")
	update.Flags().Int("autoscaling-min", 0, "Autoscaling minimum")
	update.Flags().Int("autoscaling-max", 0, "Autoscaling maximum")
	update.Flags().Bool("autohealing", false, "Enable or disable autohealing")
	update.Flags().String("upgrade-policy", "", "Upgrade policy: surge, unavailable, or balanced")
	update.Flags().String("zone", "", "Immutable zone (accepted to return backend capability error)")
	update.Flags().String("root-volume-type", "", "Immutable root volume type")
	update.Flags().Int("root-volume-size-gb", 0, "Immutable root volume size")
	update.Flags().String("security-group-id", "", "Immutable security group")
	update.Flags().Bool("public-ip", false, "Immutable public-IP policy")

	remove := &cobra.Command{
		Use: "delete <cluster_id> <pool_name>", Short: "Delete a Kapsule pool", Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			yes, _ := command.Flags().GetBool("yes")
			if err := confirmPrompt(command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Delete pool %q and its provider nodes? [y/N]: ", args[1]), yes); err != nil {
				return err
			}
			ctx, cancel, err := managedCommandContext(command)
			if err != nil {
				return err
			}
			defer cancel()
			result, err := activeManagedK8sAPI().ManagedDeletePool(ctx, provider, args[0], args[1])
			if err != nil {
				return err
			}
			return renderManagedValue(command, result, func() {
				fmt.Printf("Pool %q deleted.\n", result.NodePoolName)
			})
		},
	}
	remove.Flags().BoolP("yes", "y", false, "Skip confirmation")
	for _, command := range []*cobra.Command{list, catalog, add, scale, update, remove} {
		registerStructuredOutputFlags(command)
		root.AddCommand(command)
	}
	return root
}

func managedPoolRequestFromFlags(command *cobra.Command) (client.ManagedNodePoolRequest, error) {
	name, _ := command.Flags().GetString("name")
	size, _ := command.Flags().GetString("size")
	count, _ := command.Flags().GetInt("count")
	labelsRaw, _ := command.Flags().GetString("labels")
	taintsRaw, _ := command.Flags().GetString("taints")
	labels, err := parseLabelsFlag(labelsRaw)
	if err != nil {
		return client.ManagedNodePoolRequest{}, err
	}
	taints, err := parseTaintsFlag(taintsRaw)
	if err != nil {
		return client.ManagedNodePoolRequest{}, err
	}
	request := client.ManagedNodePoolRequest{Name: name, Size: size, Labels: labels, Taints: taints}
	if command.Flags().Changed("count") {
		request.Count = &count
	}
	setStringPointerFlag(command, "zone", &request.Zone)
	setStringPointerFlag(command, "root-volume-type", &request.RootVolumeType)
	setIntPointerFlag(command, "root-volume-size-gb", &request.RootVolumeSizeGB)
	setStringPointerFlag(command, "security-group-id", &request.SecurityGroupID)
	setBoolPointerFlag(command, "public-ip", &request.PublicIP)
	setBoolPointerFlag(command, "autohealing", &request.Autohealing)
	setStringPointerFlag(command, "upgrade-policy", &request.UpgradePolicy)
	autoscaling, _ := command.Flags().GetBool("autoscaling")
	if autoscaling {
		minimum, _ := command.Flags().GetInt("autoscaling-min")
		maximum, _ := command.Flags().GetInt("autoscaling-max")
		enabled := true
		request.Autoscaling = &client.ManagedAutoscaling{Enabled: &enabled, MinCount: minimum, MaxCount: maximum}
	}
	return request, nil
}

func managedPoolUpdateFromFlags(command *cobra.Command) (client.ManagedPoolUpdateRequest, error) {
	var request client.ManagedPoolUpdateRequest
	changed := 0
	for name, target := range map[string]**int{
		"count": &request.Count, "autoscaling-min": &request.AutoscalingMin, "autoscaling-max": &request.AutoscalingMax,
		"root-volume-size-gb": &request.RootVolumeSizeGB,
	} {
		if command.Flags().Changed(name) {
			value, _ := command.Flags().GetInt(name)
			*target = &value
			changed++
		}
	}
	for name, target := range map[string]**bool{
		"autoscaling-enabled": &request.AutoscalingEnabled, "autohealing": &request.Autohealing, "public-ip": &request.PublicIP,
	} {
		if command.Flags().Changed(name) {
			value, _ := command.Flags().GetBool(name)
			*target = &value
			changed++
		}
	}
	for name, target := range map[string]**string{
		"upgrade-policy": &request.UpgradePolicy, "zone": &request.Zone, "root-volume-type": &request.RootVolumeType,
		"security-group-id": &request.SecurityGroupID,
	} {
		if command.Flags().Changed(name) {
			value, _ := command.Flags().GetString(name)
			*target = &value
			changed++
		}
	}
	if changed == 0 {
		return request, withExitCode(exitUsage, errors.New("at least one update flag is required"))
	}
	return request, nil
}

func setStringPointerFlag(command *cobra.Command, name string, target **string) {
	if command.Flags().Changed(name) {
		value, _ := command.Flags().GetString(name)
		*target = &value
	}
}

func setIntPointerFlag(command *cobra.Command, name string, target **int) {
	if command.Flags().Changed(name) {
		value, _ := command.Flags().GetInt(name)
		*target = &value
	}
}

func setBoolPointerFlag(command *cobra.Command, name string, target **bool) {
	if command.Flags().Changed(name) {
		value, _ := command.Flags().GetBool(name)
		*target = &value
	}
}

func optionalInt(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

func init() {
	for _, registration := range managedProviderRegistry {
		managedCmd.AddCommand(newManagedProviderCommand(registration))
	}
}
