package cmd

import (
	"encoding/base64"
	"strings"
	"testing"

	"ankra/internal/client"
)

func docWithStacks(stacks ...client.StackSpec) *ImportClusterDoc {
	doc := &ImportClusterDoc{}
	doc.Spec.Stacks = stacks
	return doc
}

// A create against a name that already exists must FAIL rather than upsert.
// partial_stack merges, so without this refusal "create" would silently
// replace the existing manifest's content (ankra-mgktx).
func TestRefuseIfManifestExists(t *testing.T) {
	doc := docWithStacks(
		client.StackSpec{Name: "langfuse", Manifests: []client.ManifestSpec{{Name: "langfuse-namespace"}}},
		client.StackSpec{Name: "observability", Manifests: []client.ManifestSpec{{Name: "loki-namespace"}}},
	)

	err := refuseIfManifestExists(doc, "langfuse-namespace")
	if err == nil {
		t.Fatal("expected a refusal for an existing manifest name")
	}
	if !strings.Contains(err.Error(), "already exists in stack \"langfuse\"") {
		t.Fatalf("error should name the owning stack, got: %v", err)
	}
	if !strings.Contains(err.Error(), "manifests upgrade") {
		t.Fatalf("error should point at the upgrade verb, got: %v", err)
	}

	// A name used in ANOTHER stack still collides: manifest names are unique
	// per cluster, so reporting it here beats a server-side rejection.
	if err := refuseIfManifestExists(doc, "loki-namespace"); err == nil {
		t.Fatal("expected a refusal for a name taken by another stack")
	}

	if err := refuseIfManifestExists(doc, "langfuse-secrets"); err != nil {
		t.Fatalf("a free name must be accepted, got: %v", err)
	}
}

func TestBuildCreatedManifestCarriesContentAndParents(t *testing.T) {
	raw := []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: langfuse\n")
	created, err := buildCreatedManifest("langfuse-namespace", raw, manifestsCreateFlags{
		Namespace: "langfuse",
		Parents:   []string{"name=root-ns,kind=manifest"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Name != "langfuse-namespace" || created.Namespace != "langfuse" {
		t.Fatalf("unexpected identity: %+v", created)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(created.ManifestBase64)
	if decodeErr != nil || string(decoded) != string(raw) {
		t.Fatalf("content must round-trip verbatim, got %q (err %v)", string(decoded), decodeErr)
	}
	if len(created.Parents) != 1 || created.Parents[0].Name != "root-ns" {
		t.Fatalf("parents not carried: %+v", created.Parents)
	}
	if len(created.EncryptedPaths) != 0 {
		t.Fatalf("a plaintext manifest must declare no encrypted paths, got %v", created.EncryptedPaths)
	}
}

// An explicitly declared encrypted key must survive even when the content is
// not (yet) SOPS ciphertext, because that is how a Secret is created before
// `cluster encrypt manifest` runs over it.
func TestBuildCreatedManifestKeepsDeclaredEncryptedPaths(t *testing.T) {
	raw := []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\ndata:\n  salt: c2FsdA==\n")
	created, err := buildCreatedManifest("s", raw, manifestsCreateFlags{EncryptedPaths: []string{"salt"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created.EncryptedPaths) != 1 || created.EncryptedPaths[0] != "salt" {
		t.Fatalf("declared encrypted path not kept: %+v", created.EncryptedPaths)
	}
}

// The whole point of the command: the patch carries exactly ONE member, so
// the server's partial merge leaves every existing member untouched. Sending
// the full stack is what makes two writes revert each other (ankra-aiv3y).
func TestCreatePatchCarriesOnlyTheNewMember(t *testing.T) {
	stack := &client.StackSpec{
		Name: "langfuse",
		Manifests: []client.ManifestSpec{
			{Name: "langfuse-namespace", ManifestBase64: "b2xk"},
		},
		Addons: []client.AddonSpec{{Name: "langfuse", ChartVersion: "2.1.0"}},
	}

	patchStack := copyStackMetadata(stack)
	patchStack.Manifests = []client.ManifestSpec{{Name: "langfuse-secrets", ManifestBase64: "bmV3"}}
	req := buildPartialStackPatch(patchStack)

	if !req.PartialStack {
		t.Fatal("partial_stack must be true or the server REPLACES the stack")
	}
	if len(req.Spec.Stacks) != 1 {
		t.Fatalf("expected exactly one stack in the patch, got %d", len(req.Spec.Stacks))
	}
	sent := req.Spec.Stacks[0]
	if len(sent.Manifests) != 1 || sent.Manifests[0].Name != "langfuse-secrets" {
		t.Fatalf("patch must carry only the new manifest, got %+v", sent.Manifests)
	}
	if len(sent.Addons) != 0 {
		t.Fatalf("existing addons must NOT be resent (the server preserves them), got %+v", sent.Addons)
	}
}
