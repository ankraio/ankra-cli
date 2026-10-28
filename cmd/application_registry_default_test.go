package cmd

import "testing"

func TestRegistryHostOfImageURL(t *testing.T) {
	for input, expected := range map[string]string{
		"oci://artifact.smartoptics.dev/smart-hub-images":     "artifact.smartoptics.dev",
		"artifact.smartoptics.dev/smart-hub-images/smart-hub": "artifact.smartoptics.dev",
		"registry.ankra.cloud/org-08267821/beads-ui":          "registry.ankra.cloud",
		"https://artifact.example.com/commerce":               "artifact.example.com",
		"http://artifact.example.com/commerce":                "artifact.example.com",
		"  oci://artifact.example.com/commerce  ":             "artifact.example.com",
		"artifact.example.com":                                "artifact.example.com",
		"artifact.example.com:5000/commerce":                  "artifact.example.com:5000",
		"":                                                    "",
		"   ":                                                 "",
	} {
		if actual := registryHostOfImageURL(input); actual != expected {
			t.Fatalf("registryHostOfImageURL(%q) = %q, want %q", input, actual, expected)
		}
	}
}

// The host constant is compared against what the platform stores on an
// application, so a drift in either would silently stop the hint telling
// Ankra's own registry apart from one the organisation operates.
func TestAnkraRegistryHostIsTheStoredSpelling(t *testing.T) {
	if ankraRegistryHost != "registry.ankra.cloud" {
		t.Fatalf("unexpected Ankra registry host %q", ankraRegistryHost)
	}
	if registryHostOfImageURL("registry.ankra.cloud/org-1/app") != ankraRegistryHost {
		t.Fatal("an Ankra registry image URL must resolve to the Ankra host")
	}
}
