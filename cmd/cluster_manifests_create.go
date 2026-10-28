package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// clusterManifestsCreateCmd adds a NEW manifest to an existing stack.
//
// Until this existed the only way to add a stack member was
// `ankra cluster apply -f <ImportCluster>`, which goes through the full
// declarative import lane and PRUNES stacks and addons the file does not
// mention - so adding one Secret to one stack risked removing every other
// stack on the cluster. The surgical lane was already there and already used
// by `manifests upgrade`: PATCH /stacks/{stack} with partial_stack=true,
// which keeps every member the patch does not list and upserts the ones it
// does. This command carries exactly one new member through it (ankra-mgktx).
var clusterManifestsCreateCmd = &cobra.Command{
	Use:   "create <manifest_name>",
	Short: "Add a new manifest to an existing stack",
	Long: `Add a NEW manifest to an existing stack, without touching any other member.

Unlike 'ankra cluster apply', which is declarative over the WHOLE cluster and
prunes anything the file does not mention, this sends only the new manifest.
Every other stack, addon and manifest is left exactly as it is.

Examples:

  # Add a Secret to the langfuse stack, ordered after its namespace
  ankra cluster manifests create langfuse-secrets --stack langfuse \
    --from-file ./secret.yaml --parent name=langfuse-namespace,kind=manifest \
    --cluster luminarylane

  # Read the manifest from stdin
  cat secret.yaml | ankra cluster manifests create langfuse-secrets \
    --stack langfuse --manifest - --cluster luminarylane

  # See what would be sent without applying it
  ankra cluster manifests create web --stack app --from-file ./web.yaml --dry-run

--from-file / --manifest - accept SOPS-encrypted content: when the file carries
a top-level sops: metadata mapping, the keys holding ENC[...] ciphertext are
detected and recorded as encrypted_paths automatically. Use --encrypted-path to
declare keys explicitly when auto-detection cannot see them.

To change a manifest that already exists, use 'ankra cluster manifests upgrade'.`,
	Args: cobra.ExactArgs(1),
	RunE: runManifestsCreate,
}

type manifestsCreateFlags struct {
	Stack          string
	FromFile       string
	ManifestStdin  string // "-" if set
	Namespace      string
	Parents        []string
	EncryptedPaths []string
	Cluster        string
	DryRun         bool
	Output         outputFormat
}

func parseManifestsCreateFlags(cmd *cobra.Command) (manifestsCreateFlags, error) {
	stack, _ := cmd.Flags().GetString("stack")
	fromFile, _ := cmd.Flags().GetString("from-file")
	manifestStdin, _ := cmd.Flags().GetString("manifest")
	namespace, _ := cmd.Flags().GetString("namespace")
	parents, _ := cmd.Flags().GetStringArray("parent")
	encryptedPaths, _ := cmd.Flags().GetStringArray("encrypted-path")
	cluster, _ := cmd.Flags().GetString("cluster")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if manifestStdin != "" && manifestStdin != "-" {
		return manifestsCreateFlags{}, errors.New("--manifest currently only accepts `-` (stdin); use --from-file for a file path")
	}
	out, err := structuredFormatFromFlags(cmd)
	if err != nil {
		return manifestsCreateFlags{}, err
	}
	return manifestsCreateFlags{
		Stack:          stack,
		FromFile:       fromFile,
		ManifestStdin:  manifestStdin,
		Namespace:      namespace,
		Parents:        parents,
		EncryptedPaths: encryptedPaths,
		Cluster:        cluster,
		DryRun:         dryRun,
		Output:         out,
	}, nil
}

// buildCreatedManifest assembles the new member from the supplied content.
// Split out so the name-collision refusal and the spec construction are both
// testable without a live API.
func buildCreatedManifest(name string, raw []byte, flags manifestsCreateFlags) (client.ManifestSpec, error) {
	created := client.ManifestSpec{
		Name:           name,
		Namespace:      flags.Namespace,
		ManifestBase64: base64.StdEncoding.EncodeToString(raw),
	}
	if len(flags.Parents) > 0 {
		parents, err := parseParentFlags(flags.Parents)
		if err != nil {
			return client.ManifestSpec{}, err
		}
		created.Parents = parents
	}
	derivedPaths, isSopsDocument, deriveErr := deriveSopsEncryptedPaths(raw)
	if deriveErr != nil {
		return client.ManifestSpec{}, fmt.Errorf("inspect manifest for SOPS metadata: %w", deriveErr)
	}
	if isSopsDocument || len(flags.EncryptedPaths) > 0 {
		merged := unionEncryptedPaths(nil, derivedPaths, flags.EncryptedPaths)
		if len(merged) == 0 {
			return client.ManifestSpec{}, errors.New("manifest content is SOPS-encrypted but no encrypted key paths could be derived; pass --encrypted-path <key> for each encrypted key so the backend keeps the encryption metadata")
		}
		created.EncryptedPaths = merged
	}
	return created, nil
}

// refuseIfManifestExists keeps the create lane honest: partial_stack upserts,
// so without this a "create" against an existing name would silently REPLACE
// that manifest's content instead of failing.
func refuseIfManifestExists(doc *ImportClusterDoc, name string) error {
	for i := range doc.Spec.Stacks {
		stack := &doc.Spec.Stacks[i]
		for j := range stack.Manifests {
			if stack.Manifests[j].Name == name {
				return fmt.Errorf(
					"manifest %q already exists in stack %q; use 'ankra cluster manifests upgrade %s' to change it",
					name, stack.Name, name)
			}
		}
	}
	return nil
}

func runManifestsCreate(cmd *cobra.Command, args []string) error {
	manifestName := args[0]
	flags, err := parseManifestsCreateFlags(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(flags.Stack) == "" {
		return errors.New("--stack is required: name the existing stack the manifest joins")
	}
	if flags.FromFile == "" && flags.ManifestStdin == "" {
		return errors.New("manifest content is required (--from-file <path> or --manifest -)")
	}

	clusterID, clusterName, err := resolveClusterForCmd(flags.Cluster)
	if err != nil {
		return err
	}

	// Same timeout rationale as manifests upgrade: the partial-stack PATCH is
	// served synchronously, including a GitOps commit and push.
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	iacYAML, err := apiClient.GetClusterIaC(ctx, clusterID)
	if err != nil {
		if errors.Is(err, client.ErrClusterEmpty) {
			return fmt.Errorf("no resources on cluster %q; create the stack before adding a manifest to it", clusterName)
		}
		return fmt.Errorf("fetch cluster IaC: %w", err)
	}
	doc, err := parseImportClusterYAML([]byte(iacYAML))
	if err != nil {
		return err
	}
	stack, err := findStackInIaC(doc, flags.Stack)
	if err != nil {
		return err
	}
	if err := refuseIfManifestExists(doc, manifestName); err != nil {
		return err
	}

	raw, err := readSource(flags.FromFile, flags.ManifestStdin)
	if err != nil {
		return fmt.Errorf("read manifest source: %w", err)
	}
	created, err := buildCreatedManifest(manifestName, raw, flags)
	if err != nil {
		return err
	}

	// Only the new member travels. The server keeps every existing member of
	// the stack (clusterengine ProcessAPIStack pulls them into the search
	// space in partial mode), so nothing else is touched or reverted.
	patchStack := copyStackMetadata(stack)
	patchStack.Manifests = []client.ManifestSpec{created}

	beforeStack := copyStackMetadata(stack)
	beforeStack.Manifests = []client.ManifestSpec{}

	var notices []string
	if len(created.EncryptedPaths) > 0 {
		notices = append(notices, fmt.Sprintf(
			"manifest will be SOPS-encrypted on git push (encrypted_paths: %s)",
			strings.Join(created.EncryptedPaths, ", ")))
	}
	if len(created.Parents) == 0 {
		notices = append(notices, "no --parent given: the manifest deploys in parallel with the rest of the stack")
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
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Create completed with resource errors:")
		renderPatchResourceErrors(cmd.ErrOrStderr(), res.Errors)
		return errors.New("create partially failed; see errors above")
	}
	return printAsOutput(cmd.OutOrStdout(), res, flags.Output)
}

func init() {
	clusterManifestsCreateCmd.Flags().String("stack", "", "Existing stack the manifest joins (required)")
	clusterManifestsCreateCmd.Flags().String("from-file", "", "Path to the manifest YAML file")
	clusterManifestsCreateCmd.Flags().String("manifest", "", "Use `-` to read the manifest YAML from stdin")
	clusterManifestsCreateCmd.Flags().String("namespace", "", "Namespace for the manifest")
	clusterManifestsCreateCmd.Flags().StringArray("parent", nil, "Dependency parent, e.g. --parent name=langfuse-namespace,kind=manifest (kind defaults to manifest; repeatable)")
	clusterManifestsCreateCmd.Flags().StringArray("encrypted-path", nil, "Declare a YAML key name that is (or must stay) SOPS-encrypted; merged with auto-detected ENC[...] keys (repeatable)")
	clusterManifestsCreateCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterManifestsCreateCmd.Flags().Bool("dry-run", false, "Print the proposed before/after spec without applying changes")
	clusterManifestsCreateCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")

	clusterManifestsCmd.AddCommand(clusterManifestsCreateCmd)
}
