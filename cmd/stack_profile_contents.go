package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"ankra/internal/client"
)

// profileVersionPayload is the slice of GET
// /stack-profiles/{id}/versions/{n} this command needs. The endpoint returns
// the whole version record; only the stack spec carries resource content.
type profileVersionPayload struct {
	Version int    `json:"version"`
	Channel string `json:"channel"`
	Spec    struct {
		Stacks []client.StackSpec `json:"stacks"`
	} `json:"spec"`
}

// profileResource is one addressable piece of a profile version: a manifest's
// YAML or an add-on's values.yaml. Both are stored base64-encoded in the
// spec, which is why reading a published profile previously meant decoding it
// by hand outside the CLI.
type profileResource struct {
	Name    string
	Kind    string
	Content string
}

const profileResourceKindManifest = "manifest"
const profileResourceKindAddon = "addon"

func decodeProfileSpecField(encoded string, label string) (string, error) {
	if strings.TrimSpace(encoded) == "" {
		return "", nil
	}
	decoded, decodeError := base64.StdEncoding.DecodeString(encoded)
	if decodeError != nil {
		return "", fmt.Errorf("base64-decode %s: %w", label, decodeError)
	}
	return string(decoded), nil
}

// profileVersionResources flattens a version's manifests and add-on values
// into one addressable list, decoded and in spec order.
func profileVersionResources(payload *profileVersionPayload) ([]profileResource, error) {
	resources := []profileResource{}
	for _, stack := range payload.Spec.Stacks {
		for _, manifest := range stack.Manifests {
			content, decodeError := decodeProfileSpecField(manifest.ManifestBase64, "manifest "+manifest.Name)
			if decodeError != nil {
				return nil, decodeError
			}
			resources = append(resources, profileResource{
				Name:    manifest.Name,
				Kind:    profileResourceKindManifest,
				Content: content,
			})
		}
		for _, addon := range stack.Addons {
			encoded := ""
			if addon.Configuration != nil {
				encoded = addon.Configuration.ValuesBase64
			}
			content, decodeError := decodeProfileSpecField(encoded, "add-on "+addon.Name+" values")
			if decodeError != nil {
				return nil, decodeError
			}
			resources = append(resources, profileResource{
				Name:    addon.Name,
				Kind:    profileResourceKindAddon,
				Content: content,
			})
		}
	}
	return resources, nil
}

// manifestKindAndNamespace reads the kind and namespace out of a decoded
// manifest so the inventory can say what each resource actually is. A
// manifest holding several YAML documents reports the first kind plus a count
// of the rest, and the first namespace found across the documents (a leading
// Namespace object usually carries none of its own). Anything unparseable
// degrades to "-" rather than failing the whole listing.
func manifestKindAndNamespace(content string) (string, string) {
	if strings.TrimSpace(content) == "" {
		return "-", "-"
	}
	type manifestHead struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	kinds := []string{}
	namespace := ""
	for {
		var head manifestHead
		if decodeError := decoder.Decode(&head); decodeError != nil {
			break
		}
		if head.Kind != "" {
			kinds = append(kinds, head.Kind)
		}
		if namespace == "" {
			namespace = head.Metadata.Namespace
		}
	}
	if len(kinds) == 0 {
		return "-", "-"
	}
	kind := kinds[0]
	if len(kinds) > 1 {
		kind = fmt.Sprintf("%s (+%d)", kind, len(kinds)-1)
	}
	if namespace == "" {
		namespace = "-"
	}
	return kind, namespace
}

func findProfileResource(resources []profileResource, name string) (profileResource, error) {
	matches := []profileResource{}
	for _, resource := range resources {
		if resource.Name == name {
			matches = append(matches, resource)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return profileResource{}, fmt.Errorf(
			"resource %q is ambiguous in this version (matched %d resources)", name, len(matches))
	}
	available := make([]string, 0, len(resources))
	for _, resource := range resources {
		available = append(available, resource.Name)
	}
	sort.Strings(available)
	return profileResource{}, fmt.Errorf(
		"no resource named %q in this profile version (available: %s)", name, strings.Join(available, ", "))
}

// writeAllProfileResources emits every resource as one multi-document YAML
// stream, each preceded by a comment naming it. This is the form you can pipe
// into grep to audit a published profile in one pass.
func writeAllProfileResources(out io.Writer, resources []profileResource) error {
	for index, resource := range resources {
		if index > 0 {
			if _, writeError := io.WriteString(out, "---\n"); writeError != nil {
				return writeError
			}
		}
		header := fmt.Sprintf("# %s: %s\n", resource.Kind, resource.Name)
		if _, writeError := io.WriteString(out, header); writeError != nil {
			return writeError
		}
		text := resource.Content
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if _, writeError := io.WriteString(out, text); writeError != nil {
			return writeError
		}
	}
	return nil
}

func renderProfileContentsInventory(out io.Writer, payload *profileVersionPayload, resources []profileResource) {
	_, _ = fmt.Fprintf(out, "Profile version v%d", payload.Version)
	if payload.Channel != "" {
		_, _ = fmt.Fprintf(out, " (%s)", payload.Channel)
	}
	_, _ = fmt.Fprintln(out)

	addons := []profileResource{}
	manifests := []profileResource{}
	for _, resource := range resources {
		if resource.Kind == profileResourceKindAddon {
			addons = append(addons, resource)
			continue
		}
		manifests = append(manifests, resource)
	}

	addonSpecByName := map[string]client.AddonSpec{}
	for _, stack := range payload.Spec.Stacks {
		for _, addon := range stack.Addons {
			addonSpecByName[addon.Name] = addon
		}
	}

	if len(addons) > 0 {
		_, _ = fmt.Fprintln(out, "\nAdd-ons:")
		addonsTable := table.NewWriter()
		addonsTable.SetOutputMirror(out)
		addonsTable.SetStyle(table.StyleRounded)
		addonsTable.AppendHeader(table.Row{"Name", "Chart", "Version", "Namespace"})
		for _, resource := range addons {
			spec := addonSpecByName[resource.Name]
			namespace := spec.Namespace
			if namespace == "" {
				namespace = "-"
			}
			chartVersion := spec.ChartVersion
			if chartVersion == "" {
				chartVersion = "-"
			}
			addonsTable.AppendRow(table.Row{resource.Name, spec.ChartName, chartVersion, namespace})
		}
		addonsTable.Render()
	}

	if len(manifests) > 0 {
		_, _ = fmt.Fprintln(out, "\nManifests:")
		manifestsTable := table.NewWriter()
		manifestsTable.SetOutputMirror(out)
		manifestsTable.SetStyle(table.StyleRounded)
		manifestsTable.AppendHeader(table.Row{"Name", "Kind", "Namespace"})
		for _, resource := range manifests {
			kind, namespace := manifestKindAndNamespace(resource.Content)
			manifestsTable.AppendRow(table.Row{resource.Name, kind, namespace})
		}
		manifestsTable.Render()
	}

	if len(addons) == 0 && len(manifests) == 0 {
		_, _ = fmt.Fprintln(out, "  (no resources)")
		return
	}
	_, _ = fmt.Fprintln(out, "\nPrint one resource with --resource <name>, or every resource with --all.")
}

var stackProfilesContentsCmd = &cobra.Command{
	Use:   "contents [profile-id|profile-name] <version>",
	Short: "List or print the decoded manifests and add-on values in a profile version",
	Long: `Show what a published profile version actually contains.

Manifests and add-on values are stored base64-encoded, so ` + "`export-iac`" + ` and
` + "`version`" + ` cannot be read or grepped directly. This command decodes them.

With no --resource, it lists the add-ons and manifests, reading each
manifest's kind and namespace out of the decoded YAML:

  ankra stack-profiles contents opensearch 2

--resource prints one resource's decoded content, the same way
` + "`ankra cluster manifests get`" + ` prints a live manifest:

  ankra stack-profiles contents opensearch 2 --resource opensearch-cluster
  ankra stack-profiles contents opensearch 2 --resource fluent-bit -o raw

--all prints every resource as one multi-document YAML stream, which is the
form to audit a whole profile in one pass:

  ankra stack-profiles contents opensearch 1 --all | grep -A2 secretKeyRef`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		resourceName, _ := cmd.Flags().GetString("resource")
		allResources, _ := cmd.Flags().GetBool("all")
		outputFormat, _ := cmd.Flags().GetString("output")

		if resourceName != "" && allResources {
			return fmt.Errorf("--resource and --all are mutually exclusive")
		}
		if outputFormat != "" && resourceName == "" {
			return fmt.Errorf("-o applies only to --resource; the inventory is always human-readable and --all is always decoded YAML")
		}

		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		version, versionError := parseRequiredProfileVersionArgument(args[1])
		if versionError != nil {
			return versionError
		}

		raw, getError := apiClient.GetStackProfileVersion(cmd.Context(), profileID, version)
		if getError != nil {
			return fmt.Errorf("getting stack profile version: %w", getError)
		}
		var payload profileVersionPayload
		if unmarshalError := json.Unmarshal(raw, &payload); unmarshalError != nil {
			return fmt.Errorf("parsing stack profile version: %w", unmarshalError)
		}

		resources, decodeError := profileVersionResources(&payload)
		if decodeError != nil {
			return decodeError
		}

		output := cmd.OutOrStdout()
		if allResources {
			return writeAllProfileResources(output, resources)
		}
		if resourceName != "" {
			resource, findError := findProfileResource(resources, resourceName)
			if findError != nil {
				return findError
			}
			return writeDecodedDoc(output, resource.Content, outputFormat)
		}
		renderProfileContentsInventory(output, &payload, resources)
		return nil
	},
}

func init() {
	stackProfilesContentsCmd.Flags().String("resource", "", "Print one resource's decoded content by name (manifest or add-on)")
	stackProfilesContentsCmd.Flags().Bool("all", false, "Print every resource as one multi-document YAML stream")
	stackProfilesContentsCmd.Flags().StringP("output", "o", "", "Content format for --resource: yaml (decoded, default) or raw (base64)")
	stackProfilesCmd.AddCommand(stackProfilesContentsCmd)
}
