package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// The worker-removing writes carry force_drain only when a person asks
// (ankra-r9nng, the CLI side of ankraio/cluster#3596). Unset, each request
// must be byte-identical to what it was before the field existed, so an older
// platform sees nothing new; set, the body carries a JSON true (the platform
// refuses a non-bool with a 422) and the node-group delete, which has no
// body, carries force_drain=true as a query flag. These tests pin that for
// every provider the CLI can scale, resize or delete node groups on.

// forceDrainProvider is one provider's four worker-removing writes, bound as
// method expressions so every exported method is exercised, not only the
// shared helper behind it.
type forceDrainProvider struct {
	kind               string
	scaleWorkers       func(*Client, string, int, DrainOptions) (*ScaleWorkersResult, error)
	scaleNodeGroup     func(*Client, context.Context, string, string, int, DrainOptions, bool) (*ScaleNodeGroupResult, bool, error)
	updateInstanceType func(*Client, context.Context, string, string, string, DrainOptions, bool) (*UpdateNodeGroupResult, bool, error)
	deleteNodeGroup    func(*Client, context.Context, string, string, DrainOptions, bool) (*DeleteNodeGroupResult, bool, error)
}

var forceDrainProviders = []forceDrainProvider{
	{"hetzner", (*Client).ScaleHetznerWorkers, (*Client).ScaleHetznerNodeGroup, (*Client).UpdateHetznerNodeGroupInstanceType, (*Client).DeleteHetznerNodeGroup},
	{"ovh", (*Client).ScaleOvhWorkers, (*Client).ScaleOvhNodeGroup, (*Client).UpdateOvhNodeGroupInstanceType, (*Client).DeleteOvhNodeGroup},
	{"upcloud", (*Client).ScaleUpcloudWorkers, (*Client).ScaleUpcloudNodeGroup, (*Client).UpdateUpcloudNodeGroupInstanceType, (*Client).DeleteUpcloudNodeGroup},
	{"digitalocean", (*Client).ScaleDigitaloceanWorkers, (*Client).ScaleDigitaloceanNodeGroup, (*Client).UpdateDigitaloceanNodeGroupInstanceType, (*Client).DeleteDigitaloceanNodeGroup},
	{"scaleway", (*Client).ScaleScalewayWorkers, (*Client).ScaleScalewayNodeGroup, (*Client).UpdateScalewayNodeGroupInstanceType, (*Client).DeleteScalewayNodeGroup},
	{"aws", (*Client).ScaleAwsWorkers, (*Client).ScaleAwsNodeGroup, (*Client).UpdateAwsNodeGroupInstanceType, (*Client).DeleteAwsNodeGroup},
	{"proxmox", (*Client).ScaleProxmoxWorkers, (*Client).ScaleProxmoxNodeGroup, (*Client).UpdateProxmoxNodeGroupInstanceType, (*Client).DeleteProxmoxNodeGroup},
	{"morpheus", (*Client).ScaleMorpheusWorkers, (*Client).ScaleMorpheusNodeGroup, (*Client).UpdateMorpheusNodeGroupInstanceType, (*Client).DeleteMorpheusNodeGroup},
	{"ankracloud", (*Client).ScaleAnkraCloudWorkers, (*Client).ScaleAnkraCloudNodeGroup, (*Client).UpdateAnkraCloudNodeGroupInstanceType, (*Client).DeleteAnkraCloudNodeGroup},
}

// recordedRequest is what the fake platform saw of one request.
type recordedRequest struct {
	method string
	path   string
	query  url.Values
	body   string
}

// recordingClient returns a client whose requests are answered with
// response and recorded into the returned slot.
func recordingClient(t *testing.T, response interface{}) (*Client, *recordedRequest) {
	t.Helper()
	recorded := &recordedRequest{}
	handler := func(writer http.ResponseWriter, request *http.Request) {
		bodyBytes, readError := io.ReadAll(request.Body)
		if readError != nil {
			t.Errorf("reading request body: %v", readError)
		}
		*recorded = recordedRequest{
			method: request.Method,
			path:   request.URL.Path,
			query:  request.URL.Query(),
			body:   string(bodyBytes),
		}
		jsonResponse(t, writer, http.StatusOK, response)
	}
	return newTestClient(t, handler), recorded
}

func TestWorkerRemovingWritesCarryForceDrainOnlyWhenAsked(t *testing.T) {
	const clusterID = "cluster-123"
	const groupName = "workers"

	drainCases := []struct {
		name         string
		drainOptions DrainOptions
		// bodySuffix is what the JSON body carries after its existing
		// fields: nothing unset, a real JSON bool set.
		bodySuffix string
		wantQuery  string
	}{
		{name: "unset", drainOptions: DrainOptions{}, bodySuffix: "", wantQuery: ""},
		{name: "force", drainOptions: DrainOptions{ForceDrain: true}, bodySuffix: `,"force_drain":true`, wantQuery: "true"},
	}

	for _, provider := range forceDrainProviders {
		for _, drainCase := range drainCases {
			prefix := "/api/v1/clusters/" + provider.kind + "/" + clusterID
			t.Run(provider.kind+"/"+drainCase.name, func(t *testing.T) {
				t.Run("scale-workers", func(t *testing.T) {
					testClient, recorded := recordingClient(t, ScaleWorkersResult{PreviousCount: 3, NewCount: 2})
					if _, scaleError := provider.scaleWorkers(testClient, clusterID, 2, drainCase.drainOptions); scaleError != nil {
						t.Fatalf("scale workers: %v", scaleError)
					}
					assertRecordedRequest(t, recorded, http.MethodPost, prefix+"/scale-workers",
						`{"worker_count":2`+drainCase.bodySuffix+`}`)
					assertNoForceDrainQuery(t, recorded)
				})
				t.Run("node-group scale", func(t *testing.T) {
					testClient, recorded := recordingClient(t, ScaleNodeGroupResult{GroupName: groupName, PreviousCount: 3, NewCount: 2})
					if _, _, scaleError := provider.scaleNodeGroup(testClient, context.Background(), clusterID, groupName, 2, drainCase.drainOptions, true); scaleError != nil {
						t.Fatalf("scale node group: %v", scaleError)
					}
					assertRecordedRequest(t, recorded, http.MethodPut, prefix+"/node-groups/"+groupName+"/scale",
						`{"count":2`+drainCase.bodySuffix+`}`)
					assertNoForceDrainQuery(t, recorded)
				})
				t.Run("node-group instance-type", func(t *testing.T) {
					testClient, recorded := recordingClient(t, UpdateNodeGroupResult{GroupName: groupName, Updated: 3})
					if _, _, updateError := provider.updateInstanceType(testClient, context.Background(), clusterID, groupName, "large", drainCase.drainOptions, true); updateError != nil {
						t.Fatalf("update node group instance type: %v", updateError)
					}
					assertRecordedRequest(t, recorded, http.MethodPut, prefix+"/node-groups/"+groupName+"/instance-type",
						`{"instance_type":"large"`+drainCase.bodySuffix+`}`)
					assertNoForceDrainQuery(t, recorded)
				})
				t.Run("node-group delete", func(t *testing.T) {
					testClient, recorded := recordingClient(t, DeleteNodeGroupResult{GroupName: groupName, Deleted: 3})
					if _, _, deleteError := provider.deleteNodeGroup(testClient, context.Background(), clusterID, groupName, drainCase.drainOptions, true); deleteError != nil {
						t.Fatalf("delete node group: %v", deleteError)
					}
					assertRecordedRequest(t, recorded, http.MethodDelete, prefix+"/node-groups/"+groupName, "")
					if drainCase.wantQuery == "" {
						assertNoForceDrainQuery(t, recorded)
					} else if got := recorded.query["force_drain"]; len(got) != 1 || got[0] != drainCase.wantQuery {
						t.Errorf("force_drain query = %v, want [%s]", got, drainCase.wantQuery)
					}
					if got := recorded.query.Get("wait"); got != "true" {
						t.Errorf("wait query = %q, want the flag kept beside force_drain", got)
					}
				})
			})
		}
	}
}

func assertRecordedRequest(t *testing.T, recorded *recordedRequest, wantMethod, wantPath, wantBody string) {
	t.Helper()
	if recorded.method != wantMethod {
		t.Errorf("method = %s, want %s", recorded.method, wantMethod)
	}
	if recorded.path != wantPath {
		t.Errorf("path = %s, want %s", recorded.path, wantPath)
	}
	if recorded.body != wantBody {
		t.Errorf("body = %s, want %s", recorded.body, wantBody)
	}
}

func assertNoForceDrainQuery(t *testing.T, recorded *recordedRequest) {
	t.Helper()
	if recorded.query.Has("force_drain") {
		t.Errorf("query carries force_drain=%q, want it absent", recorded.query.Get("force_drain"))
	}
}

// controlPlaneResizeProviders is every provider's control-plane instance-type
// change, bound as a method expression so each exported method is exercised
// and not only the shared request behind it (ankra-6w1gt).
var controlPlaneResizeProviders = []struct {
	kind         string
	changeMethod func(*Client, string, string, DrainOptions) (*ChangeControlPlaneInstanceTypeResult, error)
}{
	{"hetzner", (*Client).ChangeHetznerControlPlaneInstanceType},
	{"ovh", (*Client).ChangeOvhControlPlaneInstanceType},
	{"upcloud", (*Client).ChangeUpcloudControlPlaneInstanceType},
	{"digitalocean", (*Client).ChangeDigitaloceanControlPlaneInstanceType},
	{"scaleway", (*Client).ChangeScalewayControlPlaneInstanceType},
	{"aws", (*Client).ChangeAwsControlPlaneInstanceType},
	{"proxmox", (*Client).ChangeProxmoxControlPlaneInstanceType},
	{"morpheus", (*Client).ChangeMorpheusControlPlaneInstanceType},
	{"ankracloud", (*Client).ChangeAnkraCloudControlPlaneInstanceType},
}

// TestControlPlaneInstanceTypeCarriesForceDrainOnlyWhenAsked pins the
// control-plane resize body for every provider: unset, it is only
// instance_type, byte-identical to the body before force_drain existed; set,
// it carries force_drain as a JSON true (the platform refuses a non-bool with
// a 422), and never as a query flag.
func TestControlPlaneInstanceTypeCarriesForceDrainOnlyWhenAsked(t *testing.T) {
	const clusterID = "cluster-123"

	drainCases := []struct {
		name         string
		drainOptions DrainOptions
		wantBody     string
	}{
		{name: "unset", drainOptions: DrainOptions{}, wantBody: `{"instance_type":"large"}`},
		{name: "force", drainOptions: DrainOptions{ForceDrain: true}, wantBody: `{"instance_type":"large","force_drain":true}`},
	}

	for _, provider := range controlPlaneResizeProviders {
		for _, drainCase := range drainCases {
			t.Run(provider.kind+"/"+drainCase.name, func(t *testing.T) {
				testClient, recorded := recordingClient(t, ChangeControlPlaneInstanceTypeResult{
					PreviousInstanceType: "small", NewInstanceType: "large", Updated: 3, Mode: ControlPlaneChangeModeRolling,
				})
				if _, changeError := provider.changeMethod(testClient, clusterID, "large", drainCase.drainOptions); changeError != nil {
					t.Fatalf("change control plane instance type: %v", changeError)
				}
				assertRecordedRequest(t, recorded, http.MethodPut,
					"/api/v1/clusters/"+provider.kind+"/"+clusterID+"/control-plane/instance-type", drainCase.wantBody)
				assertNoForceDrainQuery(t, recorded)
			})
		}
	}
}

// TestBastionResizeBodyCarriesNoForceDrain pins that the bastion resize,
// whose route reads no force_drain, keeps its own body shape: the node-group
// resize got a separate request type rather than a field on this one.
func TestBastionResizeBodyCarriesNoForceDrain(t *testing.T) {
	testClient, recorded := recordingClient(t, UpdateBastionInstanceTypeResult{Name: "bastion", InstanceType: "cx33"})
	if _, _, resizeError := testClient.UpdateHetznerBastionInstanceType(context.Background(), "cluster-123", "cx33", true); resizeError != nil {
		t.Fatalf("bastion resize: %v", resizeError)
	}
	if recorded.body != `{"instance_type":"cx33"}` {
		t.Errorf("body = %s, want only instance_type", recorded.body)
	}
}
