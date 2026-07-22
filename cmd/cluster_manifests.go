package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var clusterManifestsCmd = &cobra.Command{
	Use:   "manifests",
	Short: "Manage manifests for the cluster",
	Long:  "Commands to list, view, upgrade, and delete manifests.",
}

var clusterManifestsGetCmd = &cobra.Command{
	Use:   "get <manifest_name>",
	Short: "Print the current YAML content of a manifest",
	Long: `Print the current YAML content of a manifest.

By default the decoded YAML is written to stdout, making it easy to pipe into
a file or edit and re-apply with --from-file:

  ankra cluster manifests get web > web.yaml
  ankra cluster manifests get web -o raw   # base64-encoded form`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestName := args[0]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		outRaw, _ := cmd.Flags().GetString("output")

		clusterID, _, err := resolveClusterForCmd(clusterFlag)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		encoded, err := apiClient.GetClusterManifestConfiguration(ctx, clusterID, manifestName)
		if err != nil {
			return fmt.Errorf("fetch manifest content: %w", err)
		}
		decoded, dErr := base64.StdEncoding.DecodeString(encoded)
		if dErr != nil {
			return fmt.Errorf("base64-decode manifest content: %w", dErr)
		}
		return writeDecodedDoc(cmd.OutOrStdout(), string(decoded), outRaw)
	},
}

var clusterManifestsDeleteCmd = &cobra.Command{
	Use:   "delete <manifest_name>",
	Short: "Remove a manifest from its stack (disconnect)",
	Long: `Disconnect a manifest from its stack. The manifest's resources are
removed from the cluster and dependent resources are reconnected to the
manifest's own parents.

The owning stack is discovered automatically (manifest names are unique per
cluster). Use --dry-run to preview the target without making changes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifestName := args[0]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")

		clusterID, clusterName, err := resolveClusterForCmd(clusterFlag)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		iacYAML, err := apiClient.GetClusterIaC(ctx, clusterID)
		if err != nil {
			if errors.Is(err, client.ErrClusterEmpty) {
				return fmt.Errorf("no resources on cluster %q; nothing to delete", clusterName)
			}
			return fmt.Errorf("fetch cluster IaC: %w", err)
		}
		doc, err := parseImportClusterYAML([]byte(iacYAML))
		if err != nil {
			return err
		}
		stack, _, err := findManifestInIaC(doc, manifestName)
		if err != nil {
			return err
		}

		if dryRun {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Would disconnect manifest %q from stack %q (no changes made).\n", manifestName, stack.Name)
			return nil
		}

		msg := fmt.Sprintf("This removes manifest %q (stack %q) and its resources from the cluster.\nContinue? [y/N]: ", manifestName, stack.Name)
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), msg, yes); err != nil {
			return err
		}

		if _, err := apiClient.DisconnectManifest(ctx, clusterID, stack.Name, manifestName); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Manifest %q disconnected from stack %q.\n", manifestName, stack.Name)
		return nil
	},
}

var clusterManifestsListCmd = &cobra.Command{
	Use:   "list [manifest name]",
	Short: "List manifests for the active cluster; or show details for a single manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		manifests, err := apiClient.ListClusterManifests(cluster.ID)
		if err != nil {
			return fmt.Errorf("listing manifests: %w", err)
		}
		if len(args) == 0 {
			if manifests == nil {
				manifests = []client.ClusterManifestListItem{}
			}
			if handled, err := renderStructured(cmd, manifests); err != nil {
				return err
			} else if handled {
				return nil
			}
		}
		if len(manifests) == 0 {
			fmt.Println("No manifests found for the active cluster.")
			return nil
		}

		if len(args) == 1 {
			name := strings.TrimSpace(args[0])
			var found *client.ClusterManifestListItem
			for i := range manifests {
				if strings.EqualFold(manifests[i].Name, name) {
					found = &manifests[i]
					break
				}
			}
			if found == nil {
				return withExitCode(exitNotFound, fmt.Errorf("manifest %q not found on the active cluster", name))
			}
			if handled, err := renderStructured(cmd, found); err != nil {
				return err
			} else if handled {
				return nil
			}

			kind := extractKindFromBase64(found.ManifestBase64)

			fmt.Println("Manifest Details:")
			fmt.Printf("  Name:        %s\n", found.Name)
			fmt.Printf("  Kind:        %s\n", kind)
			fmt.Printf("  Namespace:   %s\n", found.Namespace)
			fmt.Printf("  State:       %s\n", found.State)

			if len(found.Parents) > 0 {
				fmt.Printf("  Parents:     ")
				for i, parent := range found.Parents {
					if i > 0 {
						fmt.Print(", ")
					}
					fmt.Printf("%s (%s)", parent.Name, parent.Kind)
				}
				fmt.Println()
			} else {
				fmt.Printf("  Parents:     none\n")
			}

			if found.ManifestBase64 != "" {
				fmt.Println("\n  Manifest Content:")
				decoded, err := base64.StdEncoding.DecodeString(found.ManifestBase64)
				if err != nil {
					return fmt.Errorf("decoding manifest: %w", err)
				}
				lines := strings.Split(string(decoded), "\n")
				for _, line := range lines {
					fmt.Printf("    %s\n", line)
				}
			}
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			"Name", "Kind", "Namespace", "State", "Parents",
		})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 25},
			{Number: 2, WidthMin: 15},
			{Number: 3, WidthMin: 15},
			{Number: 4, WidthMin: 12},
			{Number: 5, WidthMin: 20},
		})

		for _, m := range manifests {
			kind := extractKindFromBase64(m.ManifestBase64)

			state := m.State
			switch strings.ToLower(state) {
			case "up":
				state = "✓ " + state
			case "updating":
				state = "⟳ " + state
			case "failed":
				state = "✗ " + state
			}

			parents := "none"
			if len(m.Parents) > 0 {
				var parentList []string
				for _, parent := range m.Parents {
					parentList = append(parentList, fmt.Sprintf("%s (%s)", parent.Name, parent.Kind))
				}
				parents = strings.Join(parentList, ", ")
				if len(parents) > 30 {
					parents = parents[:27] + "..."
				}
			}

			t.AppendRow(table.Row{
				m.Name,
				kind,
				m.Namespace,
				state,
				parents,
			})
		}
		t.Render()
		return nil
	},
}

var clusterManifestsUpgradeCmd = &cobra.Command{
	Use:   "upgrade <manifest_name>",
	Short: "Upgrade a manifest in-place (content, --set paths, namespace)",
	Long: `Upgrade a manifest by patching just the fields you supply.

At least one mutating flag is required. Examples:

  # Patch a single path in-place, e.g. bump a Deployment image tag
  ankra cluster manifests upgrade web \
    --set 'spec.template.spec.containers[name=app].image=nginx:1.27' \
    --cluster website-demo

  # When the manifest holds multiple documents, select which one to edit
  ankra cluster manifests upgrade web --target-kind Deployment --target-name web \
    --set 'spec.replicas=3' --cluster website-demo

  # Replace the manifest content from a file
  ankra cluster manifests upgrade demo-namespace \
    --from-file manifests/demo-namespace.yaml --cluster website-demo

  # Read the manifest from stdin
  cat manifest.yaml | ankra cluster manifests upgrade demo-namespace \
    --manifest - --cluster website-demo

--set/--set-string/--set-file MUTATE the existing manifest YAML and address
list items by a stable field (e.g. containers[name=app]) as well as by numeric
index (containers[0]). --from-file / --manifest - REPLACE the whole manifest
and are mutually exclusive with --set*.

--from-file / --manifest - accept SOPS-encrypted content: when the file
carries a top-level sops: metadata mapping, the keys holding ENC[...]
ciphertext are detected and recorded as encrypted_paths automatically (merged
with the manifest's existing encrypted_paths). Use --encrypted-path to declare
keys explicitly when auto-detection cannot see them.

When no content or --set flag is supplied, the existing content is re-sent
unchanged (only namespace is updated). This is required because the backend's
manifest validation rejects empty manifest_base64.`,
	Args: cobra.ExactArgs(1),
	RunE: runManifestsUpgrade,
}

func runManifestsUpgrade(cmd *cobra.Command, args []string) error {
	manifestName := args[0]
	flags, err := parseManifestsUpgradeFlags(cmd)
	if err != nil {
		return err
	}
	if !flags.HasAnyMutation() {
		return errors.New("at least one mutating flag is required (--from-file, --manifest, --set*, or --namespace)")
	}
	if flags.HasContent() && flags.HasSet() {
		return errors.New("--from-file / --manifest - are mutually exclusive with --set/--set-string/--set-file (the former REPLACES the entire manifest; the latter MUTATES the existing one)")
	}
	if (flags.TargetKind != "" || flags.TargetName != "") && !flags.HasSet() {
		return errors.New("--target-kind/--target-name only apply together with --set/--set-string/--set-file")
	}

	clusterID, clusterName, err := resolveClusterForCmd(flags.Cluster)
	if err != nil {
		return err
	}

	// The command ends in a partial-stack PATCH, which the backend serves
	// synchronously (DB transaction + a full GitOps commit/push when the
	// cluster has a linked repo) rather than enqueuing it - that can
	// legitimately take longer than 60s on a large cluster, so the timeout
	// matches the HTTP client's slow-write ceiling (see httpClientForSlowWrite).
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
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
	stack, manifest, err := findManifestInIaC(doc, manifestName)
	if err != nil {
		return err
	}

	mutated := *manifest
	mutated.FromFile = ""

	switch {
	case flags.HasContent():
		raw, readErr := readSource(flags.FromFile, flags.ManifestStdin)
		if readErr != nil {
			return fmt.Errorf("read manifest source: %w", readErr)
		}
		mutated.ManifestBase64 = base64.StdEncoding.EncodeToString(raw)
		derivedPaths, isSopsDocument, deriveErr := deriveSopsEncryptedPaths(raw)
		if deriveErr != nil {
			return fmt.Errorf("inspect manifest for SOPS metadata: %w", deriveErr)
		}
		if isSopsDocument || len(flags.EncryptedPaths) > 0 {
			mergedPaths := unionStringLists(manifest.EncryptedPaths, derivedPaths, flags.EncryptedPaths)
			if len(mergedPaths) == 0 {
				return errors.New("manifest content is SOPS-encrypted but no encrypted key paths could be derived; pass --encrypted-path <key> for each encrypted key so the backend keeps the encryption metadata")
			}
			mutated.EncryptedPaths = mergedPaths
		}
	case flags.HasSet():
		existing, mErr := apiClient.GetClusterManifestConfiguration(ctx, clusterID, manifestName)
		if mErr != nil {
			return fmt.Errorf("fetch current manifest content: %w", mErr)
		}
		assignments, aErr := collectSetAssignments(flags.SetEntries, flags.SetStrings, flags.SetFiles)
		if aErr != nil {
			return aErr
		}
		newB64, sErr := applyManifestSet(existing, assignments, flags.TargetKind, flags.TargetName)
		if sErr != nil {
			return sErr
		}
		mutated.ManifestBase64 = newB64
	default:
		existing, mErr := apiClient.GetClusterManifestConfiguration(ctx, clusterID, manifestName)
		if mErr != nil {
			return fmt.Errorf("fetch current manifest content: %w", mErr)
		}
		mutated.ManifestBase64 = existing
	}

	if flags.Namespace != "" {
		mutated.Namespace = flags.Namespace
	}

	if flags.HasParentEdit() {
		parents, pErr := mergeParents(manifest.Parents, flags.AddParents, flags.RemoveParents, flags.SetParents)
		if pErr != nil {
			return pErr
		}
		mutated.Parents = parents
	}

	patchStack := copyStackMetadata(stack)
	patchStack.Manifests = []client.ManifestSpec{mutated}

	beforeStack := copyStackMetadata(stack)
	beforeStack.Manifests = []client.ManifestSpec{*manifest}

	var notices []string
	if len(mutated.EncryptedPaths) > 0 {
		notices = append(notices, fmt.Sprintf("manifest will be SOPS-encrypted on git push (encrypted_paths: %s)", strings.Join(mutated.EncryptedPaths, ", ")))
	}

	if flags.DryRun {
		return renderDryRun(cmd.OutOrStdout(), beforeStack, patchStack, notices, flags.Output)
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

func parseManifestsUpgradeFlags(cmd *cobra.Command) (manifestsUpgradeFlags, error) {
	fromFile, _ := cmd.Flags().GetString("from-file")
	manifestStdin, _ := cmd.Flags().GetString("manifest")
	namespace, _ := cmd.Flags().GetString("namespace")
	setEntries, _ := cmd.Flags().GetStringArray("set")
	setStrings, _ := cmd.Flags().GetStringArray("set-string")
	setFiles, _ := cmd.Flags().GetStringArray("set-file")
	targetKind, _ := cmd.Flags().GetString("target-kind")
	targetName, _ := cmd.Flags().GetString("target-name")
	encryptedPaths, _ := cmd.Flags().GetStringArray("encrypted-path")
	addParents, _ := cmd.Flags().GetStringArray("add-parent")
	removeParents, _ := cmd.Flags().GetStringArray("remove-parent")
	setParents, _ := cmd.Flags().GetStringArray("set-parent")
	cluster, _ := cmd.Flags().GetString("cluster")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")

	if manifestStdin != "" && manifestStdin != "-" {
		return manifestsUpgradeFlags{}, fmt.Errorf("--manifest currently only accepts `-` (stdin); use --from-file for a file path")
	}
	out, err := structuredFormatFromFlags(cmd)
	if err != nil {
		return manifestsUpgradeFlags{}, err
	}
	return manifestsUpgradeFlags{
		FromFile:       fromFile,
		ManifestStdin:  manifestStdin,
		Namespace:      namespace,
		SetEntries:     setEntries,
		SetStrings:     setStrings,
		SetFiles:       setFiles,
		TargetKind:     targetKind,
		TargetName:     targetName,
		EncryptedPaths: encryptedPaths,
		AddParents:     addParents,
		RemoveParents:  removeParents,
		SetParents:     setParents,
		Cluster:        cluster,
		DryRun:         dryRun,
		Yes:            yes,
		Output:         out,
	}, nil
}

func init() {
	clusterManifestsUpgradeCmd.Flags().String("from-file", "", "Path to manifest YAML file (REPLACES the entire manifest)")
	clusterManifestsUpgradeCmd.Flags().String("manifest", "", "Use `-` to read manifest YAML from stdin (REPLACES the entire manifest)")
	clusterManifestsUpgradeCmd.Flags().String("namespace", "", "Change the manifest's namespace")
	clusterManifestsUpgradeCmd.Flags().StringArray("set", nil, "In-place edit of the manifest YAML, e.g. --set 'spec.template.spec.containers[name=app].image=nginx:1.27' (MUTATES existing content; repeatable, comma-separated)")
	clusterManifestsUpgradeCmd.Flags().StringArray("set-string", nil, "Like --set but always treats the value as a string")
	clusterManifestsUpgradeCmd.Flags().StringArray("set-file", nil, "Like --set but reads the value from a file: key=path or key=@path")
	clusterManifestsUpgradeCmd.Flags().String("target-kind", "", "With --set: select the document to edit by Kubernetes kind (e.g. Deployment) when the manifest holds multiple documents")
	clusterManifestsUpgradeCmd.Flags().String("target-name", "", "With --set: select the document to edit by metadata.name when the manifest holds multiple documents")
	clusterManifestsUpgradeCmd.Flags().StringArray("encrypted-path", nil, "With --from-file / --manifest -: declare a YAML key name that is (or must stay) SOPS-encrypted; merged with auto-detected ENC[...] keys and the manifest's existing encrypted_paths (repeatable)")
	clusterManifestsUpgradeCmd.Flags().StringArray("add-parent", nil, "Add a dependency parent, e.g. --add-parent name=infisical-ns,kind=manifest (kind defaults to manifest; repeatable)")
	clusterManifestsUpgradeCmd.Flags().StringArray("remove-parent", nil, "Remove a dependency parent, e.g. --remove-parent name=infisical-ns,kind=manifest (repeatable)")
	clusterManifestsUpgradeCmd.Flags().StringArray("set-parent", nil, "Replace ALL dependency parents with the given set (repeatable)")
	clusterManifestsUpgradeCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterManifestsUpgradeCmd.Flags().Bool("dry-run", false, "Print the proposed before/after spec without applying changes")
	clusterManifestsUpgradeCmd.Flags().Bool("yes", false, "Skip confirmation prompts")
	clusterManifestsUpgradeCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")
	registerStructuredOutputFlags(clusterManifestsListCmd)

	clusterManifestsGetCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterManifestsGetCmd.Flags().StringP("output", "o", "", "Output format: yaml (decoded, default) or raw (base64)")

	clusterManifestsDeleteCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterManifestsDeleteCmd.Flags().Bool("dry-run", false, "Print the manifest that would be disconnected without making changes")
	clusterManifestsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	clusterManifestsCmd.AddCommand(clusterManifestsListCmd)
	clusterManifestsCmd.AddCommand(clusterManifestsGetCmd)
	clusterManifestsCmd.AddCommand(clusterManifestsUpgradeCmd)
	clusterManifestsCmd.AddCommand(clusterManifestsDeleteCmd)
	clusterCmd.AddCommand(clusterManifestsCmd)
}
