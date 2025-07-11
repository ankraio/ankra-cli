package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ankra/internal/client"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply an ImportCluster YAML to the Ankra API",
	Args:  cobra.NoArgs,
	Run:   runApply,
}

func init() {
	rootCmd.AddCommand(applyCmd)
	applyCmd.Flags().StringP("file", "f", "", "Path to the ImportCluster YAML file to apply")
	if err := applyCmd.MarkFlagRequired("file"); err != nil {
		fmt.Fprintf(os.Stderr, "Error marking flag as required: %s\n", err)
		os.Exit(1)
	}
}

func runApply(cmd *cobra.Command, _ []string) {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading --file: %s\n", err)
		os.Exit(1)
	}
	if filePath == "" {
		fmt.Fprintln(os.Stderr, "--file is required")
		if err := cmd.Usage(); err != nil {
			fmt.Fprintf(os.Stderr, "Error displaying usage: %s\n", err)
		}
		os.Exit(1)
	}
	if apiToken == "" {
		fmt.Fprintln(os.Stderr, "API token not provided; use --token or set ANKRA_API_TOKEN")
		os.Exit(1)
	}

	importRequest, err := buildImportRequest(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error preparing request: %s\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	importResponse, err := client.ApplyCluster(ctx, apiToken, baseURL, importRequest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error applying cluster: %s\n", err)
		os.Exit(1)
	}

	if len(importResponse.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "Import failed with the following issues:")
		for _, resourceError := range importResponse.Errors {
			fmt.Fprintf(os.Stderr, "- %s %q:\n", resourceError.Kind, resourceError.Name)
			for _, detail := range resourceError.Errors {
				fmt.Fprintf(os.Stderr, "    • %s: %s\n", detail.Key, detail.Message)
			}
		}
		os.Exit(1)
	}

	if importResponse.ImportCommand == "" {
		fmt.Printf("Cluster '%s' has been updated!\n\n", importResponse.Name)
	} else {
		fmt.Printf("Cluster '%s' imported!\n\n", importResponse.Name)
		fmt.Println("To install the Ankra agent, run:")
		commandParts := strings.Fields(importResponse.ImportCommand)
		flattenedCommand := strings.Join(commandParts, " ")
		fmt.Println(flattenedCommand)
	}

	fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
		strings.TrimRight(baseURL, "/"), importResponse.ClusterId)
}

func buildImportRequest(path string) (client.CreateImportClusterRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return client.CreateImportClusterRequest{}, fmt.Errorf("cannot read %q: %w", path, err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return client.CreateImportClusterRequest{}, fmt.Errorf("invalid YAML: %w", err)
	}

	if kind, _ := raw["kind"].(string); kind != "ImportCluster" {
		return client.CreateImportClusterRequest{}, fmt.Errorf("expected kind=ImportCluster, got %q", kind)
	}

	meta, ok := raw["metadata"].(map[string]interface{})
	if !ok {
		return client.CreateImportClusterRequest{}, errors.New("metadata missing or invalid")
	}
	clusterName, _ := meta["name"].(string)
	if clusterName == "" {
		return client.CreateImportClusterRequest{}, errors.New("metadata.name is required")
	}
	clusterDescription, _ := meta["description"].(string)

	spec, ok := raw["spec"].(map[string]interface{})
	if !ok {
		return client.CreateImportClusterRequest{}, errors.New("spec missing or invalid")
	}

	var gitRepository *client.GitRepository
	if gr, ok := spec["git_repository"].(map[string]interface{}); ok {
		gitRepository = &client.GitRepository{
			Provider:       fmt.Sprint(gr["provider"]),
			CredentialName: fmt.Sprint(gr["credential_name"]),
			Branch:         fmt.Sprint(gr["branch"]),
			Repository:     fmt.Sprint(gr["repository"]),
		}
	}

	baseDirectory := filepath.Dir(path)
	rawStackItems, _ := spec["stacks"].([]interface{})
	stacks := make([]client.Stack, 0, len(rawStackItems))
	for index, rawStack := range rawStackItems {
		stackMap, ok := rawStack.(map[string]interface{})
		if !ok {
			return client.CreateImportClusterRequest{}, fmt.Errorf("stack[%d] invalid", index)
		}
		builtStack, err := buildStack(stackMap, baseDirectory)
		if err != nil {
			return client.CreateImportClusterRequest{}, fmt.Errorf("stack[%d]: %w", index, err)
		}
		stacks = append(stacks, builtStack)
	}

	return client.CreateImportClusterRequest{
		Name:        clusterName,
		Description: clusterDescription,
		Spec: client.CreateResourceSpec{
			GitRepository: gitRepository,
			Stacks:        stacks,
		},
	}, nil
}

func buildStack(sm map[string]interface{}, baseDir string) (client.Stack, error) {
	name, _ := sm["name"].(string)
	if name == "" {
		return client.Stack{}, errors.New("stack.name is required")
	}
	desc, _ := sm["description"].(string)

	var manifests []client.Manifest
	if rawMan, ok := sm["manifests"].([]interface{}); ok {
		for i, mi := range rawMan {
			mm, ok := mi.(map[string]interface{})
			if !ok {
				return client.Stack{}, fmt.Errorf("manifest[%d] invalid", i)
			}
			m, err := buildManifest(mm, baseDir)
			if err != nil {
				return client.Stack{}, fmt.Errorf("manifest[%d]: %w", i, err)
			}
			manifests = append(manifests, m)
		}
	}

	var addons []client.Addon
	if rawAdd, ok := sm["addons"].([]interface{}); ok {
		for i, ai := range rawAdd {
			am, ok := ai.(map[string]interface{})
			if !ok {
				return client.Stack{}, fmt.Errorf("addon[%d] invalid", i)
			}
			a, err := buildAddon(am, baseDir)
			if err != nil {
				return client.Stack{}, fmt.Errorf("addon[%d]: %w", i, err)
			}
			addons = append(addons, a)
		}
	}

	return client.Stack{
		Name:        name,
		Description: desc,
		Manifests:   manifests,
		Addons:      addons,
	}, nil
}

func buildManifest(mm map[string]interface{}, baseDir string) (client.Manifest, error) {
	name, _ := mm["name"].(string)
	if name == "" {
		return client.Manifest{}, errors.New("manifest.name is required")
	}

	var content []byte
	if inline, ok := mm["manifest"].(string); ok && inline != "" {
		content = []byte(inline)
	} else if fileRef, ok := mm["from_file"].(string); ok {
		full := filepath.Join(baseDir, fileRef)
		b, err := os.ReadFile(full)
		if err != nil {
			return client.Manifest{}, fmt.Errorf("read manifest %q: %w", full, err)
		}
		content = b
	} else {
		return client.Manifest{}, errors.New("manifest: either inline or from_file must be set")
	}

	encoded := base64.StdEncoding.EncodeToString(content)
	ns, _ := mm["namespace"].(string)
	parents := parseParentList(mm["parents"])

	return client.Manifest{
		Name:           name,
		ManifestBase64: encoded,
		Namespace:      ns,
		Parents:        parents,
	}, nil
}

func buildAddon(am map[string]interface{}, baseDir string) (client.Addon, error) {
	name, _ := am["name"].(string)
	if name == "" {
		return client.Addon{}, errors.New("addon.name is required")
	}
	chart := fmt.Sprint(am["chart_name"])
	ver := fmt.Sprint(am["chart_version"])
	repo := fmt.Sprint(am["repository_url"])
	ns, _ := am["namespace"].(string)
	parents := parseParentList(am["parents"])

	var cfg interface{}
	ct, _ := am["configuration_type"].(string)
	if conf, ok := am["configuration"].(map[string]interface{}); ok {
		switch client.AddonConfigurationType(ct) {
		case client.StandaloneType:
			if pf, ok := conf["from_file"].(string); ok {
				full := filepath.Join(baseDir, pf)
				b, err := os.ReadFile(full)
				if err != nil {
					return client.Addon{}, fmt.Errorf("read addon configuration %q: %w", full, err)
				}
				cfg = client.AddonStandaloneConfiguration{
					ValuesBase64: base64.StdEncoding.EncodeToString(b),
				}
			} else if inline, ok := conf["values"].(string); ok && inline != "" {
				cfg = client.AddonStandaloneConfiguration{
					ValuesBase64: base64.StdEncoding.EncodeToString([]byte(inline)),
				}
			}
		case client.ProfileType:
			if pf, ok := conf["from_file"].(string); ok {
				full := filepath.Join(baseDir, pf)
				b, err := os.ReadFile(full)
				if err != nil {
					return client.Addon{}, fmt.Errorf("read addon profile %q: %w", full, err)
				}
				var prof client.AddonProfile
				if err := yaml.Unmarshal(b, &prof); err != nil {
					return client.Addon{}, fmt.Errorf("unmarshal profile %q: %w", full, err)
				}
				cfg = client.AddonProfileConfiguration{Profile: prof}
			}
		}
	}

	return client.Addon{
		Name:              name,
		ChartName:         chart,
		ChartVersion:      ver,
		RepositoryURL:     repo,
		Namespace:         ns,
		ConfigurationType: ct,
		Configuration:     cfg,
		Parents:           parents,
	}, nil
}

func parseParentList(raw interface{}) []client.Parent {
	arr, ok := raw.([]interface{})
	if !ok {
		return []client.Parent{}
	}
	out := make([]client.Parent, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, okName := m["name"].(string)
		kind, okKind := m["kind"].(string)
		if okName && okKind {
			out = append(out, client.Parent{Name: name, Kind: client.AnkraResourceKind(kind)})
		}
	}
	return out
}
