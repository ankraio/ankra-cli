package cmd

// The organisation-wide custom DNS zone verbs. The platform half
// (cluster#1852) renders one isolated external-dns per zone on every cluster
// in the organisation; these pin the CLI's contract with it: the request
// shapes, the refusal details surfaced verbatim, and the source column the
// per-cluster listing gained.

import (
	"net/http"
	"strings"
	"testing"
)

func TestOrgCustomDNSListRendersZonesAndTheEmptyAnswer(t *testing.T) {
	recorder := newCustomDNSServer(t)
	listPath := "GET /api/v1/org/custom-dns-zones"
	recorder.zonesByPath[listPath] = `{"success":true,"zones":[
		{"zone":"smartoptics.dev","credential_name":"avura-smartoptics"}]}`

	output := captureStdout(t, func() {
		if runError := orgCustomDNSListCmd.RunE(orgCustomDNSListCmd, nil); runError != nil {
			t.Fatalf("list returned an error: %v", runError)
		}
	})
	for _, want := range []string{"smartoptics.dev", "avura-smartoptics"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want it to mention %q", output, want)
		}
	}

	recorder.zonesByPath[listPath] = `{"success":true,"zones":[]}`
	output = captureStdout(t, func() {
		if runError := orgCustomDNSListCmd.RunE(orgCustomDNSListCmd, nil); runError != nil {
			t.Fatalf("empty list returned an error: %v", runError)
		}
	})
	if !strings.Contains(output, "No organisation-wide custom DNS zones declared") {
		t.Fatalf("output = %q, want the empty answer stated, not a bare table", output)
	}
}

func TestOrgCustomDNSAddSendsTheDeclarationAndReportsItsReach(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["POST /api/v1/org/custom-dns-zones"] =
		`{"success":true,"zone":"smartoptics.dev","credential_name":"avura-smartoptics"}`

	orgCustomDNSZone = "Smartoptics.DEV."
	orgCustomDNSCredential = "avura-smartoptics"
	output := captureStdout(t, func() {
		if runError := orgCustomDNSAddCmd.RunE(orgCustomDNSAddCmd, nil); runError != nil {
			t.Fatalf("add returned an error: %v", runError)
		}
	})

	if recorder.lastBody["zone"] != "Smartoptics.DEV." || recorder.lastBody["credential_name"] != "avura-smartoptics" {
		t.Fatalf("request body = %v, want the zone and credential as given (the platform normalises)", recorder.lastBody)
	}
	if !strings.Contains(output, "smartoptics.dev") || !strings.Contains(output, "every cluster") {
		t.Fatalf("output = %q, want the normalised zone and its organisation-wide reach reported", output)
	}
	if !strings.Contains(output, "created from now on") {
		t.Fatalf("output = %q, want it said that future clusters inherit the zone too", output)
	}
}

// The refusals are the platform's, surfaced verbatim - an overlap with the
// domain Ankra already serves for the organisation is an explanation, not a
// generic failure.
func TestOrgCustomDNSAddSurfacesTheRefusalDetail(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.refusalStatus = http.StatusBadRequest
	recorder.refusalDetail = "Custom DNS zone overlaps the zone Ankra already serves for this organisation"

	orgCustomDNSZone = "app.ankra.cc"
	orgCustomDNSCredential = "avura-smartoptics"
	runError := orgCustomDNSAddCmd.RunE(orgCustomDNSAddCmd, nil)
	if runError == nil {
		t.Fatal("a refused declaration must return an error")
	}
	if !strings.Contains(runError.Error(), "already serves for this organisation") {
		t.Fatalf("error = %q, want the platform's refusal detail surfaced", runError)
	}
}

func TestOrgCustomDNSRemoveEscapesTheZoneAndReportsTheWithdrawal(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["DELETE /api/v1/org/custom-dns-zones/smartoptics.dev"] =
		`{"success":true,"zone":"smartoptics.dev"}`

	orgCustomDNSZone = "smartoptics.dev"
	output := captureStdout(t, func() {
		if runError := orgCustomDNSRemoveCmd.RunE(orgCustomDNSRemoveCmd, nil); runError != nil {
			t.Fatalf("remove returned an error: %v", runError)
		}
	})
	if !strings.Contains(output, "withdrawn") || !strings.Contains(output, "records are untouched") {
		t.Fatalf("output = %q, want the withdrawal and the records-untouched promise stated", output)
	}
	if !strings.Contains(output, "every inheriting cluster") {
		t.Fatalf("output = %q, want the organisation-wide reach of the withdrawal stated", output)
	}
}

// A cluster listing tells the two scopes apart, and reads a platform that
// predates the organisation-wide lane (no source member) as the cluster's own.
func TestClusterCustomDNSListShowsWhereEachZoneWasDeclared(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["GET /api/v1/clusters/"+customDNSClusterID+"/custom-dns-zones"] = `{"success":true,"zones":[
		{"zone":"smartoptics.dev","credential_name":"avura-smartoptics","source":"organisation"},
		{"zone":"launch.example.com","credential_name":"example-dns","source":"cluster"},
		{"zone":"legacy.example.net","credential_name":"example-dns"}]}`

	output := captureStdout(t, func() {
		if runError := clusterCustomDNSListCmd.RunE(clusterCustomDNSListCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("list returned an error: %v", runError)
		}
	})
	if !strings.Contains(output, "organisation") {
		t.Fatalf("output = %q, want the inherited zone marked as the organisation's", output)
	}
	if strings.Count(output, "cluster") < 2 {
		t.Fatalf("output = %q, want both the declared and the source-less zone marked as the cluster's own", output)
	}
}
