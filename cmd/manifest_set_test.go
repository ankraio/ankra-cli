package cmd

import (
	"encoding/base64"
	"strings"
	"testing"
)

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func decodeB64(t *testing.T, s string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	return string(raw)
}

const deploymentYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: app
          image: nginx:1.0
        - name: sidecar
          image: busybox:1.0
`

const serviceYAML = `apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  ports:
    - port: 80
`

func mustSet(t *testing.T, raws []string) []SetAssignment {
	t.Helper()
	a, err := ParseSetAssignments(raws, setKindCoerce)
	if err != nil {
		t.Fatalf("ParseSetAssignments: %v", err)
	}
	return a
}

func TestApplyManifestSet_SingleDocImageTag(t *testing.T) {
	in := b64(deploymentYAML)
	a := mustSet(t, []string{"spec.template.spec.containers[name=app].image=nginx:1.27"})
	out, err := applyManifestSet(in, a, "", "")
	if err != nil {
		t.Fatalf("applyManifestSet: %v", err)
	}
	got := decodeB64(t, out)
	if !strings.Contains(got, "nginx:1.27") {
		t.Errorf("expected app image updated, got:\n%s", got)
	}
	if !strings.Contains(got, "busybox:1.0") {
		t.Errorf("expected sidecar image preserved, got:\n%s", got)
	}
}

func TestApplyManifestSet_MultiDocRequiresTarget(t *testing.T) {
	in := b64(deploymentYAML + "---\n" + serviceYAML)
	a := mustSet(t, []string{"spec.replicas=3"})
	_, err := applyManifestSet(in, a, "", "")
	if err == nil {
		t.Fatal("expected error when multiple docs and no target")
	}
	if !strings.Contains(err.Error(), "contains 2 documents") {
		t.Errorf("error should mention doc count, got: %v", err)
	}
}

func TestApplyManifestSet_MultiDocTargetByKindName(t *testing.T) {
	in := b64(deploymentYAML + "---\n" + serviceYAML)
	a := mustSet(t, []string{"spec.replicas=3"})
	out, err := applyManifestSet(in, a, "Deployment", "web")
	if err != nil {
		t.Fatalf("applyManifestSet: %v", err)
	}
	got := decodeB64(t, out)
	if !strings.Contains(got, "replicas: 3") {
		t.Errorf("expected Deployment replicas updated, got:\n%s", got)
	}
	// Service doc must be preserved untouched.
	if !strings.Contains(got, "kind: Service") || !strings.Contains(got, "port: 80") {
		t.Errorf("expected Service doc preserved, got:\n%s", got)
	}
	// Both documents must remain present, separated by ---.
	if strings.Count(got, "---") < 1 {
		t.Errorf("expected document separator preserved, got:\n%s", got)
	}
}

func TestApplyManifestSet_KindCaseInsensitive(t *testing.T) {
	in := b64(deploymentYAML + "---\n" + serviceYAML)
	a := mustSet(t, []string{"spec.replicas=5"})
	out, err := applyManifestSet(in, a, "deployment", "")
	if err != nil {
		t.Fatalf("applyManifestSet: %v", err)
	}
	if !strings.Contains(decodeB64(t, out), "replicas: 5") {
		t.Errorf("expected case-insensitive kind match to update replicas")
	}
}

func TestApplyManifestSet_AmbiguousTargetErrors(t *testing.T) {
	second := strings.Replace(deploymentYAML, "name: web", "name: api", 1)
	in := b64(deploymentYAML + "---\n" + second)
	a := mustSet(t, []string{"spec.replicas=3"})
	_, err := applyManifestSet(in, a, "Deployment", "")
	if err == nil {
		t.Fatal("expected ambiguous-target error")
	}
	if !strings.Contains(err.Error(), "matches 2 documents") {
		t.Errorf("error should mention ambiguity, got: %v", err)
	}
}

func TestApplyManifestSet_TargetNotFoundErrors(t *testing.T) {
	in := b64(deploymentYAML + "---\n" + serviceYAML)
	a := mustSet(t, []string{"spec.replicas=3"})
	_, err := applyManifestSet(in, a, "ConfigMap", "")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "no document in the manifest matches") {
		t.Errorf("error should mention no match, got: %v", err)
	}
}

func TestParseManifestDocs_KindAndName(t *testing.T) {
	docs, err := parseManifestDocs(b64(deploymentYAML + "---\n" + serviceYAML))
	if err != nil {
		t.Fatalf("parseManifestDocs: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
	if docs[0].kind != "Deployment" || docs[0].name != "web" {
		t.Errorf("doc0 = %s/%s, want Deployment/web", docs[0].kind, docs[0].name)
	}
	if docs[1].kind != "Service" || docs[1].name != "web" {
		t.Errorf("doc1 = %s/%s, want Service/web", docs[1].kind, docs[1].name)
	}
}

func TestParseManifestDocs_SkipsEmptyTrailingDoc(t *testing.T) {
	docs, err := parseManifestDocs(b64(deploymentYAML + "---\n"))
	if err != nil {
		t.Fatalf("parseManifestDocs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1 (trailing --- should be skipped)", len(docs))
	}
}
