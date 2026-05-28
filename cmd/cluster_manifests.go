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
	Long:  "Commands to list and view manifests.",
}

var clusterManifestsListCmd = &cobra.Command{
	Use:   "list [manifest name]",
	Short: "List manifests for the active cluster; or show details for a single manifest",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		manifests, err := apiClient.ListClusterManifests(cluster.ID)
		if err != nil {
			fmt.Printf("Error listing manifests: %v\n", err)
			return
		}
		if len(manifests) == 0 {
			fmt.Println("No manifests found for the active cluster.")
			return
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
				fmt.Printf("Manifest %q not found on the active cluster.\n", name)
				return
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
					fmt.Printf("    Error decoding manifest: %v\n", err)
				} else {
					lines := strings.Split(string(decoded), "\n")
					for _, line := range lines {
						fmt.Printf("    %s\n", line)
					}
				}
			}
			return
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
	},
}

var clusterManifestsUpgradeCmd = &cobra.Command{
	Use:   "upgrade <manifest_name>",
	Short: "Upgrade a manifest in-place (content, namespace)",
	Long: `Upgrade a manifest by patching just the fields you supply.

At least one mutating flag is required. Examples:

  # Replace the manifest content from a file
  ankra cluster manifests upgrade demo-namespace \
    --from-file manifests/demo-namespace.yaml --cluster website-demo

  # Read the manifest from stdin
  cat manifest.yaml | ankra cluster manifests upgrade demo-namespace \
    --manifest - --cluster website-demo

When neither --from-file nor --manifest is supplied, the existing content is
re-sent unchanged (only namespace is updated). This is required because the
backend's manifest validation rejects empty manifest_base64.`,
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
		return errors.New("at least one mutating flag is required (--from-file, --manifest, or --namespace)")
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
	stack, manifest, err := findManifestInIaC(doc, manifestName)
	if err != nil {
		return err
	}

	mutated := *manifest
	mutated.FromFile = ""

	if flags.HasContent() {
		raw, readErr := readSource(flags.FromFile, flags.ManifestStdin)
		if readErr != nil {
			return fmt.Errorf("read manifest source: %w", readErr)
		}
		mutated.ManifestBase64 = base64.StdEncoding.EncodeToString(raw)
	} else {
		existing, mErr := apiClient.GetClusterManifestConfiguration(ctx, clusterID, manifestName)
		if mErr != nil {
			return fmt.Errorf("fetch current manifest content: %w", mErr)
		}
		mutated.ManifestBase64 = existing
	}

	if flags.Namespace != "" {
		mutated.Namespace = flags.Namespace
	}

	patchStack := copyStackMetadata(stack)
	patchStack.Manifests = []client.ManifestSpec{mutated}

	beforeStack := copyStackMetadata(stack)
	beforeStack.Manifests = []client.ManifestSpec{*manifest}

	var notices []string
	if len(manifest.EncryptedPaths) > 0 {
		notices = append(notices, fmt.Sprintf("manifest will be SOPS-encrypted on git push (encrypted_paths: %s)", strings.Join(manifest.EncryptedPaths, ", ")))
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
		fmt.Fprintln(cmd.ErrOrStderr(), "Update completed with resource errors:")
		for _, e := range res.Errors {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s %s [%s]: %s\n", e.Kind, e.Name, e.Key, e.Message)
		}
		return errors.New("update partially failed; see errors above")
	}
	return printAsOutput(cmd.OutOrStdout(), res, flags.Output)
}

func parseManifestsUpgradeFlags(cmd *cobra.Command) (manifestsUpgradeFlags, error) {
	fromFile, _ := cmd.Flags().GetString("from-file")
	manifestStdin, _ := cmd.Flags().GetString("manifest")
	namespace, _ := cmd.Flags().GetString("namespace")
	cluster, _ := cmd.Flags().GetString("cluster")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	outRaw, _ := cmd.Flags().GetString("output")

	if manifestStdin != "" && manifestStdin != "-" {
		return manifestsUpgradeFlags{}, fmt.Errorf("--manifest currently only accepts `-` (stdin); use --from-file for a file path")
	}
	out, err := parseOutputFormat(outRaw)
	if err != nil {
		return manifestsUpgradeFlags{}, err
	}
	return manifestsUpgradeFlags{
		FromFile:      fromFile,
		ManifestStdin: manifestStdin,
		Namespace:     namespace,
		Cluster:       cluster,
		DryRun:        dryRun,
		Output:        out,
	}, nil
}

func init() {
	clusterManifestsUpgradeCmd.Flags().String("from-file", "", "Path to manifest YAML file")
	clusterManifestsUpgradeCmd.Flags().String("manifest", "", "Use `-` to read manifest YAML from stdin")
	clusterManifestsUpgradeCmd.Flags().String("namespace", "", "Change the manifest's namespace")
	clusterManifestsUpgradeCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterManifestsUpgradeCmd.Flags().Bool("dry-run", false, "Print the proposed before/after spec without applying changes")
	clusterManifestsUpgradeCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")

	clusterManifestsCmd.AddCommand(clusterManifestsListCmd)
	clusterManifestsCmd.AddCommand(clusterManifestsUpgradeCmd)
	clusterCmd.AddCommand(clusterManifestsCmd)
}
