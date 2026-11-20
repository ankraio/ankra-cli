package cmd

import (
	"errors"
	"testing"

	"ankra/internal/client"
)

// adaptMockClient keeps real complete clients intact and adapts narrow test
// doubles once. Stateless defaults live on the one adapter; narrow doubles
// define only the handlers they override, without promoted-method wrappers.
func adaptMockClient(target any) APIClient {
	if target == nil {
		return nil
	}
	if complete, ok := target.(APIClient); ok {
		return complete
	}
	return mockClientAdapter{target: target}
}

type adapterRecordingMock struct {
	calls    int
	page     int
	pageSize int
	err      error
}

func (m *adapterRecordingMock) ListClusters(page, pageSize int) (*client.ClusterListResponse, error) {
	m.calls++
	m.page, m.pageSize = page, pageSize
	return &client.ClusterListResponse{}, m.err
}

type adapterValueMock struct{}

func (adapterValueMock) OrganisationOverride() string { return "explicit-value-override" }

func TestMockClientAdapterDelegatesAndRetainsDefaults(t *testing.T) {
	mock := &adapterRecordingMock{}
	adapted := adaptMockClient(mock)
	result, err := adapted.ListClusters(7, 19)
	if err != nil || result == nil || mock.calls != 1 || mock.page != 7 || mock.pageSize != 19 {
		t.Fatalf("override did not retain arguments, result and pointer state: result=%v err=%v mock=%+v", result, err, mock)
	}
	if _, err := adapted.CreateChatSession(client.CreateChatSessionRequest{}); err != client.ErrChatSessionsUnavailable {
		t.Fatalf("legacy chat fallback changed: %v", err)
	}
	if err := adapted.CancelChatSession("missing"); err == nil || err.Error() != "not implemented" {
		t.Fatalf("unimplemented fallback changed: %v", err)
	}
	if adaptMockClient(adapted) != adapted {
		t.Fatal("complete client was wrapped again")
	}
	if adaptMockClient(nil) != nil {
		t.Fatal("nil client must remain nil")
	}
	mock.err = errors.New("recorded handler error")
	if _, err := adapted.ListClusters(1, 2); !errors.Is(err, mock.err) {
		t.Fatalf("handler error changed: %v", err)
	}
	if adaptMockClient(adapterValueMock{}).OrganisationOverride() != "explicit-value-override" {
		t.Fatal("value receiver override was lost")
	}
	real := &client.Client{}
	if adaptMockClient(real) != real {
		t.Fatal("real complete client must pass through unchanged")
	}
}
