package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type alertIngestCredentialsMock struct {
	baseMock
	credentials   []client.AlertIngestCredential
	clusters      map[string]client.ClusterListItem
	rebound       string
	rebindRequest *client.RebindAlertIngestCredentialRequest
}

func (mock *alertIngestCredentialsMock) ListAlertIngestCredentials() (*client.AlertIngestCredentialList, error) {
	return &client.AlertIngestCredentialList{Items: mock.credentials}, nil
}

func (mock *alertIngestCredentialsMock) RebindAlertIngestCredential(credentialID string,
	request client.RebindAlertIngestCredentialRequest) (*client.AlertIngestCredential, error) {
	mock.rebound = credentialID
	mock.rebindRequest = &request
	scope := "cluster"
	if request.Scope != nil {
		scope = *request.Scope
	}
	credential := client.AlertIngestCredential{ID: credentialID, Name: "hel1-alertmanager", Scope: scope, Enabled: true}
	if request.ClusterIDSet && request.ClusterID != nil {
		credential.ClusterID = request.ClusterID
		name := "prod-hel1"
		credential.ClusterName = &name
	}
	return &credential, nil
}

func (mock *alertIngestCredentialsMock) GetCluster(name string) (client.ClusterListItem, error) {
	cluster, found := mock.clusters[name]
	if !found {
		return client.ClusterListItem{}, errors.New("cluster not found")
	}
	return cluster, nil
}

func sampleIngestCredentials() []client.AlertIngestCredential {
	pinnedName, brokenName, lastUsed := "prod-hel1", "old-prod", "2026-09-04T10:00:00Z"
	pinnedID, brokenID := "1fa75b8f-f87c-4d21-ab01-d97cfb4d795c", "d8f216ef-b627-4d38-b0f8-ef963d3e24bf"
	return []client.AlertIngestCredential{
		{ID: "c1", Name: "hel1-alertmanager", ClusterID: &pinnedID, ClusterName: &pinnedName, Scope: "mixed", Enabled: true, LastUsedAt: &lastUsed},
		{ID: "c2", Name: "old-alertmanager", ClusterID: &brokenID, ClusterName: &brokenName, ClusterUnavailable: true, Scope: "cluster", Enabled: true},
		{ID: "c3", Name: "opensearch-monitor", Scope: "platform", Enabled: false},
	}
}

func TestAlertsIngestCredentialsListRendersPinsAndBrokenOnes(t *testing.T) {
	mock := &alertIngestCredentialsMock{credentials: sampleIngestCredentials()}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	for _, fragment := range []string{"hel1-alertmanager", "prod-hel1", "mixed", "old-alertmanager", "BROKEN",
		"opensearch-monitor", "platform", "never", "2026-09-04T10:00:00Z"} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected the table to contain %q, got:\n%s", fragment, stdout)
		}
	}
	empty := &alertIngestCredentialsMock{}
	stdout, _, runError = runAlertsCommand(t, empty, "", "alerts", "ingest-credentials", "list")
	if runError != nil || !strings.Contains(stdout, "No alert ingest credentials found.") {
		t.Fatalf("empty list: %v %q", runError, stdout)
	}
}

func TestAlertsIngestCredentialsRebindPinsByClusterNameWithoutTouchingTheToken(t *testing.T) {
	mock := &alertIngestCredentialsMock{clusters: map[string]client.ClusterListItem{
		"prod-hel1": {ID: "1fa75b8f-f87c-4d21-ab01-d97cfb4d795c", Name: "prod-hel1"},
	}}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "rebind", "c1",
		"--cluster", "prod-hel1", "--scope", "mixed")
	if runError != nil {
		t.Fatalf("rebind failed: %v", runError)
	}
	if mock.rebound != "c1" || mock.rebindRequest == nil || !mock.rebindRequest.ClusterIDSet ||
		mock.rebindRequest.ClusterID == nil || *mock.rebindRequest.ClusterID != "1fa75b8f-f87c-4d21-ab01-d97cfb4d795c" ||
		mock.rebindRequest.Scope == nil || *mock.rebindRequest.Scope != "mixed" {
		t.Fatalf("rebind request = %+v", mock.rebindRequest)
	}
	if !strings.Contains(stdout, "rebound: scope mixed, cluster prod-hel1") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAlertsIngestCredentialsRebindUnpinsAndRefusesConflictingFlags(t *testing.T) {
	mock := &alertIngestCredentialsMock{}
	if _, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "rebind", "c2",
		"--unpin", "--scope", "platform"); runError != nil {
		t.Fatalf("unpin failed: %v", runError)
	}
	if mock.rebindRequest == nil || !mock.rebindRequest.ClusterIDSet || mock.rebindRequest.ClusterID != nil ||
		mock.rebindRequest.Scope == nil || *mock.rebindRequest.Scope != "platform" {
		t.Fatalf("unpin request = %+v", mock.rebindRequest)
	}
	for name, args := range map[string][]string{
		"cluster and unpin together": {"rebind", "c2", "--cluster", "x", "--unpin"},
		"nothing to change":          {"rebind", "c2"},
		"an unknown scope":           {"rebind", "c2", "--scope", "everything"},
	} {
		if _, _, runError := runAlertsCommand(t, &alertIngestCredentialsMock{}, "",
			append([]string{"alerts", "ingest-credentials"}, args...)...); runError == nil {
			t.Errorf("%s: expected a usage error", name)
		}
	}
}

func TestRebindAlertIngestCredentialRequestSendsOnlyWhatWasSet(t *testing.T) {
	scope := "platform"
	for name, testCase := range map[string]struct {
		request client.RebindAlertIngestCredentialRequest
		want    string
	}{
		"unpin":      {client.RebindAlertIngestCredentialRequest{ClusterIDSet: true}, `{"cluster_id":null}`},
		"scope only": {client.RebindAlertIngestCredentialRequest{Scope: &scope}, `{"scope":"platform"}`},
	} {
		encoded, marshalError := testCase.request.MarshalJSON()
		if marshalError != nil || string(encoded) != testCase.want {
			t.Errorf("%s: %s %v", name, encoded, marshalError)
		}
	}
}
