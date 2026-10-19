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
	if mock.requestedID != testClusterID {
		t.Fatalf("quota read cluster %q, want %q", mock.requestedID, testClusterID)
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
