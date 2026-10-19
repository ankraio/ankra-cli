package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func previewRowsByName(rows []bindingPreview) map[string]bindingPreview {
	byName := map[string]bindingPreview{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	return byName
}

// PLA-910: an input whose default is "" deploys with the empty string, so the
// preview must show it as a default rather than "unset".
func TestPreviewResolvedBindingsShowsAnEmptyDefault(t *testing.T) {
	parameters := []client.StackProfileParameter{{Name: "proxy_url", Type: "string", Default: stringPointer("")}}

	row := previewRowsByName(previewResolvedBindings(parameters, nil))["proxy_url"]

	if row.Source != "default" || row.Value != "" {
		t.Errorf("proxy_url = %+v, want an empty default", row)
	}
}

// PLA-910: `--set proxy_url=` is an explicit empty value and wins over a
// non-empty default.
func TestPreviewResolvedBindingsLetsAnExplicitEmptySetWin(t *testing.T) {
	parameters := []client.StackProfileParameter{{Name: "proxy_url", Type: "string", Default: stringPointer("http://proxy:3128")}}

	row := previewRowsByName(previewResolvedBindings(parameters,
		[]client.ParameterBinding{{Name: "proxy_url", Value: "", Explicit: true}}))["proxy_url"]

	if row.Source != "--set" || row.Value != "" {
		t.Errorf("proxy_url = %+v, want the explicit empty --set", row)
	}
}

func TestUnsetRequiredInputsCountsAnEmptyRequiredValueAsMissing(t *testing.T) {
	parameters := []client.StackProfileParameter{{Name: "chat_host", Type: "string", Required: true}}

	rows := previewResolvedBindings(parameters, []client.ParameterBinding{{Name: "chat_host", Value: "", Explicit: true}})

	if missing := unsetRequiredInputs(rows); len(missing) != 1 || missing[0] != "chat_host" {
		t.Errorf("unset required = %v, want chat_host: the platform refuses a blank required input", missing)
	}
}

func TestBuildParameterBindingsMarksEveryBindingExplicit(t *testing.T) {
	t.Setenv("PROXY_FROM_ENV", "")
	bindings, err := buildParameterBindings([]string{"proxy_url=", "region=eu"}, nil, []string{"token=PROXY_FROM_ENV"})
	if err != nil {
		t.Fatalf("buildParameterBindings: %v", err)
	}
	for _, binding := range bindings {
		if !binding.Explicit {
			t.Errorf("binding %+v must be explicit: every --set is the caller's own value", binding)
		}
	}
	encoded, _ := json.Marshal(bindings[0])
	if !strings.Contains(string(encoded), `"explicit":true`) || !strings.Contains(string(encoded), `"value":""`) {
		t.Errorf("wire form = %s, want the empty value sent with explicit:true", encoded)
	}
}

func TestMergeParameterBindingsKeepsTheExplicitMarkerOfAnOverride(t *testing.T) {
	merged := mergeParameterBindings(
		[]client.ParameterBinding{{Name: "proxy_url", Value: "http://proxy:3128"}},
		[]client.ParameterBinding{{Name: "proxy_url", Value: "", Explicit: true}})

	if len(merged) != 1 || merged[0].Value != "" || !merged[0].Explicit {
		t.Errorf("merged = %+v, want the explicit empty override", merged)
	}
}
