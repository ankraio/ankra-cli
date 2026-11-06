package cmd

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/cachenotice"
)

func objectWithFieldManagers() map[string]interface{} {
	return map[string]interface{}{"metadata": map[string]interface{}{
		"name": "fleet-0",
		"managedFields": []interface{}{
			map[string]interface{}{"manager": "kube-controller-manager", "operation": "Update", "time": "2026-11-03T10:15:29Z"},
			map[string]interface{}{"manager": "someone-edit", "operation": "Update", "subresource": "resize", "time": "2026-11-05T08:00:00Z"},
			map[string]interface{}{"manager": "kubelet", "operation": "Update", "subresource": "status", "time": "2026-11-04T05:47:26Z"},
		},
	}}
}

func TestFieldManagersAreOrderedNewestFirst(t *testing.T) {
	managers := fieldManagersNewestFirst(objectWithFieldManagers())
	if len(managers) != 3 {
		t.Fatalf("got %d managers, want 3", len(managers))
	}
	if managers[0].manager != "someone-edit" || managers[0].subresource != "resize" {
		t.Errorf("newest manager = %+v, want the resize", managers[0])
	}
	if managers[2].manager != "kube-controller-manager" {
		t.Errorf("oldest manager = %+v", managers[2])
	}
}

func TestRenderFieldManagersShowsTheResizeWithItsTime(t *testing.T) {
	var out bytes.Buffer
	renderFieldManagers(&out, objectWithFieldManagers(), cachenotice.Answer{})
	for _, want := range []string{"someone-edit", "resize", "2026-11-05T08:00:00Z"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, out.String())
		}
	}
}

// A live read that degraded to the cached copy holds no field managers
// whatever the object's history is, so it must not read as "nobody wrote it".
func TestRenderFieldManagersDoesNotCallACachedCopyEmpty(t *testing.T) {
	var out bytes.Buffer
	object := map[string]interface{}{"metadata": map[string]interface{}{"name": "fleet-0"}}
	renderFieldManagers(&out, object, cachenotice.Answer{ServedFromCache: true, Stale: true})
	if !strings.Contains(out.String(), fieldManagersUnavailableNotice) {
		t.Errorf("cached answer printed %q", out.String())
	}
	if strings.Contains(out.String(), "None recorded") {
		t.Errorf("a cached copy was reported as having no managers: %q", out.String())
	}
}

func TestRenderFieldManagersSaysSoWhenALiveObjectHasNone(t *testing.T) {
	var out bytes.Buffer
	renderFieldManagers(&out, map[string]interface{}{"metadata": map[string]interface{}{}}, cachenotice.Answer{})
	if !strings.Contains(out.String(), "None recorded.") {
		t.Errorf("live object without managers printed %q", out.String())
	}
}
