package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type ankraCloudQuotaMock struct {
	baseMock
	report      *client.ClusterQuotaReport
	readError   error
	requestedID string
	cluster     client.ClusterListItem
	lookupError error
	lookups     int
}

func (mock *ankraCloudQuotaMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	mock.lookups++
	if mock.lookupError != nil {
		return client.ClusterListItem{}, mock.lookupError
	}
	return mock.cluster, nil
}

func (mock *ankraCloudQuotaMock) GetAnkraCloudQuotas(clusterID string) (*client.ClusterQuotaReport, error) {
	mock.requestedID = clusterID
	return mock.report, mock.readError
}

func quotaFloat(value float64) *float64 {
	return &value
}

func nearlyFullQuotaReport() *client.ClusterQuotaReport {
	storage := client.QuotaDimension{Dimension: "storage_gb", Unit: "GB", Used: 275, Limit: quotaFloat(300),
		Remaining: quotaFloat(25), UtilisationPercent: quotaFloat(91.7)}
	servers := client.QuotaDimension{Dimension: "servers", Unit: "count", Used: 6, Limit: quotaFloat(6),
		Remaining: quotaFloat(0), UtilisationPercent: quotaFloat(100), Exhausted: true}
	return &client.ClusterQuotaReport{
		Provider:      "ankracloud",
		LimitsExposed: true,
		Dimensions:    []client.QuotaDimension{servers, storage},
		Pressured:     []client.QuotaDimension{servers, storage},
		Increase: client.QuotaIncreasePath{Mechanism: "provider_support", ConsoleURL: "https://cloud.ankra.app/settings/quotas",
			Instructions: "open https://cloud.ankra.app/settings/quotas and request a higher value"},
	}
}

func TestAnkraCloudQuotaShowsWhatIsLeft(t *testing.T) {
	mock := &ankraCloudQuotaMock{report: nearlyFullQuotaReport()}
	output, runError := runAnkraCloudCommand(t, mock, "quota", testClusterID)
	if runError != nil {
		t.Fatalf("quota: %v\n%s", runError, output)
	}
	if mock.requestedID != testClusterID || mock.lookups != 0 {
		t.Fatalf("quota read cluster %q after %d cluster lookups, want %q and none", mock.requestedID, mock.lookups, testClusterID)
	}
	for _, fragment := range []string{"Storage", "275 GiB", "300 GiB", "25 GiB", "Servers", "(full)",
		"Near or at the limit: servers, storage.", "https://cloud.ankra.app/settings/quotas"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output must carry %q, got:\n%s", fragment, output)
		}
	}
}

func TestAnkraCloudQuotaStructuredOutputIsTheReport(t *testing.T) {
	mock := &ankraCloudQuotaMock{report: nearlyFullQuotaReport()}
	output, runError := runAnkraCloudCommand(t, mock, "quota", testClusterID, "-o", "json")
	if runError != nil {
		t.Fatalf("quota -o json: %v\n%s", runError, output)
	}
	var decoded client.ClusterQuotaReport
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout must be the JSON report: %v\n%s", decodeError, output)
	}
	if len(decoded.Dimensions) != 2 || *decoded.Dimensions[1].Remaining != 25 {
		t.Fatalf("decoded report = %+v", decoded)
	}
}

func TestAnkraCloudQuotaReportsAnUnreachableCloud(t *testing.T) {
	mock := &ankraCloudQuotaMock{readError: errors.New("Failed to read Ankra Cloud quotas")}
	if _, runError := runAnkraCloudCommand(t, mock, "quota", testClusterID); runError == nil ||
		!strings.Contains(runError.Error(), "fetching the Ankra Cloud quota") {
		t.Fatalf("an unreadable quota must fail with its cause, got %v", runError)
	}
}

func TestAnkraCloudQuotaOnAnotherKindOfClusterSaysSoAndExitsTwo(t *testing.T) {
	for _, structured := range []bool{false, true} {
		mock := &ankraCloudQuotaMock{readError: &client.UnexpectedResponseError{StatusCode: 404},
			cluster: client.ClusterListItem{ID: testClusterID, Name: "games", Kind: "hetzner"}}
		arguments := []string{"quota", testClusterID}
		if structured {
			arguments = append(arguments, "-o", "json")
		}
		output, runError := runAnkraCloudCommand(t, mock, arguments...)
		want := testClusterID + " is not an Ankra Cloud cluster (its kind is hetzner); quota applies to Ankra Cloud clusters only"
		if runError == nil || runError.Error() != want || exitCodeFor(runError) != exitUsage {
			t.Fatalf("a Hetzner cluster must be refused with exit 2 and a plain sentence:\n got %v (exit %d)\nwant %q", runError, exitCodeFor(runError), want)
		}
		if strings.Contains(runError.Error(), "404") {
			t.Fatalf("the refusal must not carry the raw 404: %v", runError)
		}
		if structured && strings.TrimSpace(output) != "" {
			t.Fatalf("-o json must leave stdout empty on a refusal (the error goes to stderr), got:\n%s", output)
		}
	}
}

func TestAnkraCloudQuotaRefusesAnUnknownOutputFormatBeforeAnyRequest(t *testing.T) {
	mock := &ankraCloudQuotaMock{report: nearlyFullQuotaReport()}
	_, runError := runAnkraCloudCommand(t, mock, "quota", testClusterID, "-o", "bogus")
	if runError == nil || exitCodeFor(runError) != exitUsage || mock.requestedID != "" {
		t.Fatalf("-o bogus must exit 2 before the quota is read: %v (exit %d), read %q", runError, exitCodeFor(runError), mock.requestedID)
	}
}

func TestAnkraCloudQuotaKeepsThePlatformAnswerWhenTheKindDoesNotExplainIt(t *testing.T) {
	notFound := &client.UnexpectedResponseError{StatusCode: 404}
	for name, mock := range map[string]*ankraCloudQuotaMock{
		"an Ankra Cloud cluster": {readError: notFound, cluster: client.ClusterListItem{ID: testClusterID, Kind: "ankracloud"}},
		"an unreadable kind":     {readError: notFound, lookupError: errors.New("listing clusters failed")},
		"an empty kind":          {readError: notFound, cluster: client.ClusterListItem{ID: testClusterID}},
	} {
		_, runError := runAnkraCloudCommand(t, mock, "quota", testClusterID)
		if runError == nil || !strings.Contains(runError.Error(), "fetching the Ankra Cloud quota") || exitCodeFor(runError) != exitNotFound {
			t.Fatalf("%s: the platform's 404 must be kept (exit 3), got %v (exit %d)", name, runError, exitCodeFor(runError))
		}
	}
	serverError := &ankraCloudQuotaMock{readError: &client.UnexpectedResponseError{StatusCode: 502},
		cluster: client.ClusterListItem{ID: testClusterID, Kind: "hetzner"}}
	if _, runError := runAnkraCloudCommand(t, serverError, "quota", testClusterID); runError == nil || exitCodeFor(runError) != exitError || serverError.lookups != 0 {
		t.Fatalf("only a 404 reads the cluster's kind: got %v (exit %d) after %d lookups", runError, exitCodeFor(runError), serverError.lookups)
	}
}

func TestPrintValidationWarningsNamesWhatItCan(t *testing.T) {
	output := &bytes.Buffer{}
	printValidationWarnings(output, nil)
	if output.Len() != 0 {
		t.Fatalf("no warnings must print nothing, got %q", output.String())
	}
	printValidationWarnings(output, []client.ValidationWarning{
		{Kind: "PersistentVolumeClaim", Name: "new claims", Category: "ankracloud_quota",
			Message: "This change asks for 50 GiB of new persistent volume storage, but the Ankra Cloud account may hold at most 300 GiB"},
		{Message: "A warning about nothing in particular."},
	})
	for _, fragment := range []string{"Warnings:", `PersistentVolumeClaim "new claims": This change asks for 50 GiB`,
		"  - A warning about nothing in particular."} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("output must carry %q, got:\n%s", fragment, output.String())
		}
	}
}
