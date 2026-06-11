package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)


var clusterAddonsCmd = &cobra.Command{
	Use:   "addons",
	Short: "Manage addons for clusters",
	Long:  "Commands to list, manage settings, and uninstall addons.",
}

var clusterAddonsListCmd = &cobra.Command{
	Use:   "list [addon name]",
	Short: "List addons for the active cluster; or show details for a single addon",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		addons, err := apiClient.ListClusterAddons(cluster.ID)
		if err != nil {
			fmt.Printf("Error listing addons: %v\n", err)
			return
		}
		if len(args) == 0 {
			if addons == nil {
				addons = []client.ClusterAddonListItem{}
			}
			if renderStructuredOrExit(cmd, addons) {
				return
			}
		}
		if len(addons) == 0 {
			fmt.Println("No addons found for the active cluster.")
			return
		}

		if len(args) == 1 {
			name := strings.TrimSpace(args[0])
			var found *client.ClusterAddonListItem
			for i := range addons {
				if addons[i].Name == name {
					found = &addons[i]
					break
				}
			}
			if found == nil {
				fmt.Printf("Addon %q not found on the active cluster.\n", name)
				return
			}
			if renderStructuredOrExit(cmd, found) {
				return
			}

			fmt.Println("Addon Details:")
			fmt.Printf("  ID:              %s\n", found.ID)
			fmt.Printf("  Name:            %s\n", found.Name)
			fmt.Printf("  Chart:           %s\n", found.ChartName)
			fmt.Printf("  Version:         %s\n", found.ChartVersion)
			fmt.Printf("  Repository:      %s\n", found.RepositoryURL)
			fmt.Printf("  Namespace:       %s\n", found.Namespace)
			fmt.Printf("  Through Ankra:   %t\n", found.ThroughAnkra)
			if found.Health != nil {
				fmt.Printf("  Health:          %s\n", *found.Health)
			}
			if found.State != nil {
				fmt.Printf("  State:           %s\n", *found.State)
			}
			fmt.Printf("  Created:         %s\n", formatTimeAgo(found.CreatedAt.Format(time.RFC3339)))
			fmt.Printf("  Updated:         %s\n", formatTimeAgo(found.UpdatedAt.Format(time.RFC3339)))
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			"Name", "Chart", "Version", "Namespace", "Health", "Ankra?", "Created At", "Updated At", "State",
		})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 20},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 15},
			{Number: 5, WidthMin: 10},
			{Number: 6, WidthMin: 8},
			{Number: 7, WidthMin: 15},
			{Number: 8, WidthMin: 15},
			{Number: 9, WidthMin: 10},
		})

		for _, a := range addons {
			health := ""
			if a.Health != nil {
				health = *a.Health
			}
			state := ""
			if a.State != nil {
				state = *a.State
			}
			t.AppendRow(table.Row{
				a.Name,
				a.ChartName,
				a.ChartVersion,
				a.Namespace,
				health,
				a.ThroughAnkra,
				formatTimeAgo(a.CreatedAt.Format(time.RFC3339)),
				formatTimeAgo(a.UpdatedAt.Format(time.RFC3339)),
				state,
			})
		}
		t.Render()
	},
}

var clusterAddonsAvailableCmd = &cobra.Command{
	Use:   "available",
	Short: "List addons available for installation",
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		addons, err := apiClient.ListAvailableAddons(cluster.ID)
		if err != nil {
			fmt.Printf("Error listing available addons: %v\n", err)
			return
		}
		if addons == nil {
			addons = []client.AvailableAddon{}
		}
		if renderStructuredOrExit(cmd, addons) {
			return
		}

		if len(addons) == 0 {
			fmt.Println("No addons available for installation.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Chart", "Version", "Category"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 20},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 15},
		})

		for _, a := range addons {
			category := ""
			if a.Category != nil {
				category = *a.Category
			}
			t.AppendRow(table.Row{
				a.ID,
				a.Name,
				a.ChartName,
				a.Version,
				category,
			})
		}
		t.Render()
	},
}

var clusterAddonsSettingsCmd = &cobra.Command{
	Use:   "settings <addon_name>",
	Short: "Get settings for an addon",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addonName := args[0]

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		settings, err := apiClient.GetAddonSettings(cluster.ID, addonName)
		if err != nil {
			fmt.Printf("Error getting addon settings: %v\n", err)
			return
		}
		if renderStructuredOrExit(cmd, settings) {
			return
		}

		fmt.Printf("Settings for addon '%s':\n\n", settings.AddonName)

		// Pretty print as JSON
		jsonData, err := json.MarshalIndent(settings.Settings, "", "  ")
		if err != nil {
			fmt.Printf("Error formatting settings: %v\n", err)
			return
		}
		fmt.Println(string(jsonData))
	},
}

var clusterAddonsValuesCmd = &cobra.Command{
	Use:   "values <addon_name>",
	Short: "Print the current Helm values for an addon",
	Long: `Print the current Helm values document for an addon.

By default the decoded YAML is written to stdout, making it easy to pipe into
a file or edit and re-apply with --values-from-file:

  ankra cluster addons values my-addon > values.yaml
  ankra cluster addons values my-addon -o raw   # base64-encoded form`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addonName := args[0]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		outRaw, _ := cmd.Flags().GetString("output")

		clusterID, _, err := resolveClusterForCmd(clusterFlag)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		values, err := apiClient.GetClusterAddonValues(ctx, clusterID, addonName)
		if err != nil {
			return fmt.Errorf("fetch addon values: %w", err)
		}
		return writeDecodedDoc(cmd.OutOrStdout(), values, outRaw)
	},
}

var clusterAddonsUninstallCmd = &cobra.Command{
	Use:   "uninstall <addon_name>",
	Short: "Uninstall an addon from the cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addonName := args[0]
		deletePermanently, _ := cmd.Flags().GetBool("delete")

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		addon, err := apiClient.GetAddonByName(cluster.ID, addonName)
		if err != nil {
			fmt.Printf("Error finding addon: %v\n", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.UninstallAddon(ctx, cluster.ID, addon.ID, deletePermanently)
		if err != nil {
			fmt.Printf("Error uninstalling addon: %v\n", err)
			return
		}

		if result.Success {
			if deletePermanently {
				fmt.Printf("Addon '%s' uninstalled and deleted successfully!\n", addonName)
			} else {
				fmt.Printf("Addon '%s' uninstalled successfully!\n", addonName)
			}
		}
	},
}

var clusterAddonsUpdateCmd = &cobra.Command{
	Use:   "update <addon_name>",
	Short: "Update settings for an addon from a JSON file",
	Long: `Update addon settings by providing a JSON file that conforms to the settings schema.

Example JSON file:
  {
    "retry_policy": { "limit": 3, "backoff": { "duration": "5s", "factor": 2, "max_duration": "3m" } },
    "sync_policy": { "automated": true, "self_heal": true, "auto_prune": false },
    "revision_history_limit": 10
  }

Usage:
  ankra cluster addons update my-addon -f settings.json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addonName := args[0]
		filePath, _ := cmd.Flags().GetString("file")

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		fileData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file %s: %v\n", filePath, err)
			os.Exit(1)
		}

		var settings client.AddonSettings
		if err := json.Unmarshal(fileData, &settings); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing settings JSON: %v\n", err)
			os.Exit(1)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := apiClient.UpdateAddonSettings(ctx, cluster.ID, addonName, settings); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating addon settings: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Settings for addon '%s' updated successfully!\n", addonName)
	},
}

var clusterAddonsUpgradeCmd = &cobra.Command{
	Use:   "upgrade <addon_name>",
	Short: "Upgrade an addon in-place (chart version, values, registry, namespace)",
	Long: `Upgrade an addon by patching just the fields you supply.

At least one mutating flag is required. Examples:

  # Bump chart version
  ankra cluster addons upgrade ankra-website --chart-version 1.0.146 \
    --cluster website-demo

  # Tweak a single Helm values field with --set (mutates the existing values)
  ankra cluster addons upgrade website --set image.tag=1.0.146 \
    --cluster website-demo

  # Address a list item by a field instead of an index
  ankra cluster addons upgrade website --set 'env[name=LOG_LEVEL].value=debug' \
    --cluster website-demo

  # Replace the whole values document
  ankra cluster addons upgrade website \
    --values-from-file ./values.yaml --cluster website-demo

--set* and --values-from-file are mutually exclusive: --set* mutates the
existing values document while --values-from-file replaces it.

Changing --namespace is destructive (Helm reinstall in the new namespace,
leaves the old release orphaned). Use --yes to skip the confirmation prompt
or interactively confirm.`,
	Args: cobra.ExactArgs(1),
	RunE: runAddonsUpgrade,
}

func runAddonsUpgrade(cmd *cobra.Command, args []string) error {
	addonName := args[0]
	flags, err := parseAddonsUpgradeFlags(cmd)
	if err != nil {
		return err
	}
	if !flags.HasAnyMutation() {
		return errors.New("at least one mutating flag is required (--chart-version, --values-from-file, --values, --set*, --registry-*, or --namespace)")
	}
	if flags.HasValuesReplace() && flags.HasSet() {
		return errors.New("--values-from-file / --values - are mutually exclusive with --set/--set-string/--set-file (the former REPLACES the entire values document; the latter MUTATES the existing one)")
	}

	clusterID, clusterName, err := resolveClusterForCmd(flags.Cluster)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	iacYAML, err := apiClient.GetClusterIaC(ctx, clusterID)
	if err != nil {
		if errors.Is(err, client.ErrClusterEmpty) {
			return fmt.Errorf("no resources on cluster %q; nothing to upgrade", clusterName)
		}
		return fmt.Errorf("fetch cluster IaC: %w", err)
	}
	doc, err := parseImportClusterYAML([]byte(iacYAML))
	if err != nil {
		return err
	}
	stack, addon, err := findAddonInIaC(doc, addonName, flags.Stack)
	if err != nil {
		return err
	}

	var newValuesB64 *string
	var notices []string

	encryptedPaths := []string{}
	if addon.Configuration != nil && len(addon.Configuration.EncryptedPaths) > 0 {
		encryptedPaths = addon.Configuration.EncryptedPaths
	}

	switch {
	case flags.HasValuesReplace():
		raw, readErr := readSource(flags.ValuesFromFile, flags.ValuesStdin)
		if readErr != nil {
			return fmt.Errorf("read values source: %w", readErr)
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		newValuesB64 = &encoded
		if len(encryptedPaths) > 0 {
			notices = append(notices, fmt.Sprintf("values will be SOPS-encrypted on git push (encrypted_paths: %s)", strings.Join(encryptedPaths, ", ")))
		}
	case flags.HasSet():
		currentYAML, valErr := apiClient.GetClusterAddonValues(ctx, clusterID, addonName)
		if valErr != nil {
			return fmt.Errorf("fetch current addon values: %w", valErr)
		}
		var root yaml.Node
		if currentYAML != "" {
			if err := yaml.Unmarshal([]byte(currentYAML), &root); err != nil {
				return fmt.Errorf("parse current addon values: %w", err)
			}
		}
		assignments, err := collectSetAssignments(flags.SetEntries, flags.SetStrings, flags.SetFiles)
		if err != nil {
			return err
		}
		if err := ApplySetAssignments(&root, assignments); err != nil {
			return err
		}
		var buf strings.Builder
		enc := yaml.NewEncoder(stringWriter{w: &buf})
		enc.SetIndent(2)
		if err := enc.Encode(&root); err != nil {
			return fmt.Errorf("re-encode addon values: %w", err)
		}
		_ = enc.Close()
		encoded := base64.StdEncoding.EncodeToString([]byte(buf.String()))
		newValuesB64 = &encoded
		if len(encryptedPaths) > 0 {
			notices = append(notices, fmt.Sprintf("values will be SOPS-encrypted on git push (encrypted_paths: %s)", strings.Join(encryptedPaths, ", ")))
		}
	}

	mutatedAddon := applyAddonMutations(*addon, flags, newValuesB64)
	if mutatedAddon.Configuration != nil && len(encryptedPaths) > 0 {
		mutatedAddon.Configuration.EncryptedPaths = encryptedPaths
	}
	if flags.HasParentEdit() {
		parents, pErr := mergeParents(addon.Parents, flags.AddParents, flags.RemoveParents, flags.SetParents)
		if pErr != nil {
			return pErr
		}
		mutatedAddon.Parents = parents
	}
	patchStack := copyStackMetadata(stack)
	patchStack.Addons = []client.AddonSpec{mutatedAddon}

	beforeStack := previewStackBefore(stack, *addon)

	if flags.Namespace != "" && flags.Namespace != addon.Namespace {
		notices = append(notices, fmt.Sprintf("addon will be re-installed in namespace %q; the old release in %q is left orphaned", flags.Namespace, addon.Namespace))
	}

	if flags.DryRun {
		return renderDryRun(cmd.OutOrStdout(), beforeStack, patchStack, notices, flags.Output)
	}

	if flags.Namespace != "" && flags.Namespace != addon.Namespace {
		if err := confirmNamespaceChange(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), addon.Namespace, flags.Namespace, flags.Yes); err != nil {
			return err
		}
	}

	req := buildPartialStackPatch(patchStack)
	res, err := apiClient.PatchClusterStackPartial(ctx, clusterID, stack.Name, req)
	if err != nil {
		var perr *client.PatchStackError
		if errors.As(err, &perr) {
			return mapPatchError(perr)
		}
		return err
	}
	if len(res.Errors) > 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Update completed with resource errors:")
		for _, e := range res.Errors {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  - %s %s [%s]: %s\n", e.Kind, e.Name, e.Key, e.Message)
		}
		return errors.New("update partially failed; see errors above")
	}
	return printAsOutput(cmd.OutOrStdout(), res, flags.Output)
}

// stringWriter adapts a *strings.Builder to io.Writer.
type stringWriter struct{ w *strings.Builder }

func (s stringWriter) Write(p []byte) (int, error) { return s.w.Write(p) }

// previewStackBefore returns a stack with just the original addon for the
// dry-run "before" rendering, matching the shape of the "after" stack.
func previewStackBefore(src *client.StackSpec, orig client.AddonSpec) client.StackSpec {
	before := copyStackMetadata(src)
	before.Addons = []client.AddonSpec{orig}
	return before
}

func parseAddonsUpgradeFlags(cmd *cobra.Command) (addonsUpgradeFlags, error) {
	chartVersion, _ := cmd.Flags().GetString("chart-version")
	namespace, _ := cmd.Flags().GetString("namespace")
	regName, _ := cmd.Flags().GetString("registry-name")
	regURL, _ := cmd.Flags().GetString("registry-url")
	regCred, _ := cmd.Flags().GetString("registry-credential-name")

	valuesFile, _ := cmd.Flags().GetString("values-from-file")
	valuesStdin, _ := cmd.Flags().GetString("values")
	setEntries, _ := cmd.Flags().GetStringArray("set")
	setStrings, _ := cmd.Flags().GetStringArray("set-string")
	setFiles, _ := cmd.Flags().GetStringArray("set-file")

	addParents, _ := cmd.Flags().GetStringArray("add-parent")
	removeParents, _ := cmd.Flags().GetStringArray("remove-parent")
	setParents, _ := cmd.Flags().GetStringArray("set-parent")

	cluster, _ := cmd.Flags().GetString("cluster")
	stack, _ := cmd.Flags().GetString("stack")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")

	out, err := structuredFormatFromFlags(cmd)
	if err != nil {
		return addonsUpgradeFlags{}, err
	}
	if valuesStdin != "" && valuesStdin != "-" {
		return addonsUpgradeFlags{}, fmt.Errorf("--values currently only accepts `-` (stdin); use --values-from-file for a file path")
	}
	return addonsUpgradeFlags{
		ChartVersion:           chartVersion,
		Namespace:              namespace,
		RegistryName:           regName,
		RegistryURL:            regURL,
		RegistryCredentialName: regCred,
		ValuesFromFile:         valuesFile,
		ValuesStdin:            valuesStdin,
		SetEntries:             setEntries,
		SetStrings:             setStrings,
		SetFiles:               setFiles,
		AddParents:             addParents,
		RemoveParents:          removeParents,
		SetParents:             setParents,
		Cluster:                cluster,
		Stack:                  stack,
		DryRun:                 dryRun,
		Yes:                    yes,
		Output:                 out,
	}, nil
}

// collectSetAssignments parses the raw --set / --set-string / --set-file flag
// slices into a single ordered list of assignments. Shared by both the addon
// and manifest upgrade commands.
func collectSetAssignments(setEntries, setStrings, setFiles []string) ([]SetAssignment, error) {
	var all []SetAssignment
	if len(setEntries) > 0 {
		got, err := ParseSetAssignments(setEntries, setKindCoerce)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	if len(setStrings) > 0 {
		got, err := ParseSetAssignments(setStrings, setKindString)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	if len(setFiles) > 0 {
		got, err := ParseSetAssignments(setFiles, setKindFile)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	return all, nil
}

func init() {
	clusterAddonsUninstallCmd.Flags().Bool("delete", false, "Also delete the addon permanently")

	clusterAddonsUpdateCmd.Flags().StringP("file", "f", "", "Path to JSON settings file (required)")
	_ = clusterAddonsUpdateCmd.MarkFlagRequired("file")

	clusterAddonsUpgradeCmd.Flags().String("chart-version", "", "New chart version to install")
	clusterAddonsUpgradeCmd.Flags().String("namespace", "", "Change the addon's namespace (destructive: Helm reinstall)")
	clusterAddonsUpgradeCmd.Flags().String("registry-name", "", "New helm registry name")
	clusterAddonsUpgradeCmd.Flags().String("registry-url", "", "New helm registry URL")
	clusterAddonsUpgradeCmd.Flags().String("registry-credential-name", "", "New helm registry credential name")
	clusterAddonsUpgradeCmd.Flags().String("values-from-file", "", "Path to YAML values file (REPLACES the entire values document)")
	clusterAddonsUpgradeCmd.Flags().String("values", "", "Use `-` to read values YAML from stdin (REPLACES the entire values document)")
	clusterAddonsUpgradeCmd.Flags().StringArray("set", nil, "Helm-style values mutation, e.g. --set image.tag=1.0.0 (MUTATES existing values; repeatable, comma-separated)")
	clusterAddonsUpgradeCmd.Flags().StringArray("set-string", nil, "Like --set but always treats the value as a string")
	clusterAddonsUpgradeCmd.Flags().StringArray("set-file", nil, "Like --set but reads the value from a file: key=path or key=@path")
	clusterAddonsUpgradeCmd.Flags().StringArray("add-parent", nil, "Add a dependency parent, e.g. --add-parent name=infisical-ns,kind=manifest (kind defaults to manifest; repeatable)")
	clusterAddonsUpgradeCmd.Flags().StringArray("remove-parent", nil, "Remove a dependency parent, e.g. --remove-parent name=infisical-ns,kind=manifest (repeatable)")
	clusterAddonsUpgradeCmd.Flags().StringArray("set-parent", nil, "Replace ALL dependency parents with the given set (repeatable); pass none with the others to clear")
	clusterAddonsUpgradeCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterAddonsUpgradeCmd.Flags().String("stack", "", "Stack name (required when the addon exists in multiple stacks)")
	clusterAddonsUpgradeCmd.Flags().Bool("dry-run", false, "Print the proposed before/after spec without applying changes")
	clusterAddonsUpgradeCmd.Flags().Bool("yes", false, "Skip the confirmation prompt for destructive changes (--namespace)")
	clusterAddonsUpgradeCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")
	registerStructuredOutputFlags(clusterAddonsListCmd, clusterAddonsAvailableCmd, clusterAddonsSettingsCmd)

	clusterAddonsValuesCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterAddonsValuesCmd.Flags().StringP("output", "o", "", "Output format: yaml (decoded, default) or raw (base64)")

	clusterAddonsCmd.AddCommand(clusterAddonsListCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsAvailableCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsSettingsCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsValuesCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsUninstallCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsUpdateCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsUpgradeCmd)

	clusterCmd.AddCommand(clusterAddonsCmd)
}
