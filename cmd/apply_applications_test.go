package cmd

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

const applyApplicationsDeploymentYAML = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\n  namespace: ankra-cloud\n"

func TestBuildStackReadsApplications(t *testing.T) {
	directory := t.TempDir()
	if writeError := os.WriteFile(filepath.Join(directory, "api.yaml"), []byte(applyApplicationsDeploymentYAML), 0o644); writeError != nil {
		t.Fatal(writeError)
	}
	stack, buildError := buildStack(map[string]interface{}{
		"name": "control-plane",
		"manifests": []interface{}{
			map[string]interface{}{"name": "namespace", "manifest": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ankra-cloud\n"},
		},
		"applications": []interface{}{
			map[string]interface{}{
				"name":                    "api",
				"platform_application_id": "be1a31c5-1d49-4baa-a901-621373f4b601",
				"namespace":               "ankra-cloud",
				"from_file":               "api.yaml",
				"parameters":              map[string]interface{}{"replicas": "2"},
				"encrypted_paths":         []interface{}{"data.token"},
				"parents":                 []interface{}{map[string]interface{}{"kind": "manifest", "name": "namespace"}},
			},
			map[string]interface{}{
				"name":                    "rendered",
				"platform_application_id": "11111111-1111-4111-8111-111111111111",
			},
		},
	}, directory)
	if buildError != nil {
		t.Fatalf("building the stack: %v", buildError)
	}
	if len(stack.Applications) != 2 {
		t.Fatalf("applications = %+v, want both", stack.Applications)
	}
	api := stack.Applications[0]
	if api.PlatformApplicationID != "be1a31c5-1d49-4baa-a901-621373f4b601" || api.Namespace != "ankra-cloud" ||
		api.ManifestBase64 != base64.StdEncoding.EncodeToString([]byte(applyApplicationsDeploymentYAML)) ||
		api.Parameters["replicas"] != "2" || len(api.EncryptedPaths) != 1 ||
		len(api.Parents) != 1 || api.Parents[0].Name != "namespace" {
		t.Errorf("api = %+v, want every field read from the file", api)
	}
	if stack.Applications[1].ManifestBase64 != "" {
		t.Errorf("an application with no manifest leaves rendering to the platform, got %+v", stack.Applications[1])
	}
}

func TestBuildStackRefusesApplicationsItCannotRead(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"no name":                 {"platform_application_id": "be1a31c5-1d49-4baa-a901-621373f4b601", "manifest": applyApplicationsDeploymentYAML},
		"no manifest and no id":   {"name": "api"},
		"invalid yaml":            {"name": "api", "manifest": "kind: [unterminated"},
		"id that is not a string": {"name": "api", "platform_application_id": 7, "manifest": applyApplicationsDeploymentYAML},
		"non-string parameter":    {"name": "api", "manifest": applyApplicationsDeploymentYAML, "parameters": map[string]interface{}{"replicas": 2}},
		"parameters not a map":    {"name": "api", "manifest": applyApplicationsDeploymentYAML, "parameters": "replicas=2"},
	}
	for name, application := range cases {
		t.Run(name, func(t *testing.T) {
			if _, buildError := buildStack(map[string]interface{}{"name": "s", "applications": []interface{}{application}}, ""); buildError == nil {
				t.Fatal("want a refusal")
			}
		})
	}
	if _, buildError := buildStack(map[string]interface{}{"name": "s", "applications": []interface{}{"api"}}, ""); buildError == nil ||
		!strings.Contains(buildError.Error(), "application #1") {
		t.Fatalf("a non-object entry must be refused by position, got %v", buildError)
	}
}

func TestValidateResourceGraphKnowsApplications(t *testing.T) {
	request := client.CreateImportClusterRequest{Spec: client.CreateResourceSpec{Stacks: []client.Stack{{
		Name:      "control-plane",
		Manifests: []client.Manifest{{Name: "job", Parents: []client.Parent{{Kind: "application", Name: "api"}}}},
		Applications: []client.ImportApplication{{Name: "api",
			Parents: []client.Parent{{Kind: "manifest", Name: "namespace"}}}},
	}}}}
	request.Spec.Stacks[0].Manifests = append(request.Spec.Stacks[0].Manifests, client.Manifest{Name: "namespace"})
	if validateError := validateResourceGraph(request); validateError != nil {
		t.Fatalf("a manifest may depend on an application and the reverse: %v", validateError)
	}

	request.Spec.Stacks[0].Manifests[0].Parents = []client.Parent{{Kind: "application", Name: "missing"}}
	if validateError := validateResourceGraph(request); validateError == nil {
		t.Fatal("a parent application that is not declared must be refused")
	}

	cyclic := client.CreateImportClusterRequest{Spec: client.CreateResourceSpec{Stacks: []client.Stack{{
		Name: "s",
		Applications: []client.ImportApplication{
			{Name: "a", Parents: []client.Parent{{Kind: "application", Name: "b"}}},
			{Name: "b", Parents: []client.Parent{{Kind: "application", Name: "a"}}},
		},
	}}}}
	if validateError := validateResourceGraph(cyclic); validateError == nil {
		t.Fatal("a cycle between applications must be refused")
	}
}
