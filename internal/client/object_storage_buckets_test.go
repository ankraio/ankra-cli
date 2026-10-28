package client

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestListObjectStorageBuckets_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/object-storage-buckets" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, ObjectStorageBucketListResult{Items: []ObjectStorageBucket{
			{ID: "bucket-1", Name: "registry", Provider: "hetzner", Region: "fsn1", Bucket: "ankra-registry-1a2b",
				Endpoint: "https://fsn1.your-objectstorage.com", PathStyle: true, Status: "ready"},
		}})
	}
	result, listError := newTestClient(t, handler).ListObjectStorageBuckets()
	if listError != nil {
		t.Fatalf("ListObjectStorageBuckets: %v", listError)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "registry" || result.Items[0].Endpoint == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGetObjectStorageBucket_EscapesTheID(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/org/object-storage-buckets/a%2Fb" {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		jsonResponse(t, w, http.StatusOK, ObjectStorageBucket{ID: "a/b", Status: "provisioning"})
	}
	bucket, getError := newTestClient(t, handler).GetObjectStorageBucket("a/b")
	if getError != nil || bucket.Status != "provisioning" {
		t.Fatalf("GetObjectStorageBucket = %+v (%v)", bucket, getError)
	}
}

func TestCreateObjectStorageBucket_OmitsAbsentKeys(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/object-storage-buckets" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatal(decodeError)
		}
		if body["name"] != "registry" || body["credential_id"] != "cred-1" || body["region"] != "fsn1" {
			t.Errorf("body = %v", body)
		}
		// A Hetzner pair stored on the credential is the no-keys case: the
		// fields must be absent, not empty strings the API would read as
		// half a pair.
		for _, field := range []string{"bucket", "access_key_id", "secret_access_key"} {
			if _, present := body[field]; present {
				t.Errorf("%s must be omitted when empty, body = %v", field, body)
			}
		}
		jsonResponse(t, w, http.StatusAccepted, ObjectStorageBucket{ID: "bucket-1", Name: "registry", Status: "provisioning"})
	}
	bucket, createError := newTestClient(t, handler).CreateObjectStorageBucket(CreateObjectStorageBucketRequest{
		Name: "registry", CredentialID: "cred-1", Region: "fsn1",
	})
	if createError != nil || bucket.ID != "bucket-1" {
		t.Fatalf("CreateObjectStorageBucket = %+v (%v)", bucket, createError)
	}
}

func TestDeleteObjectStorageBucket_OnlyDestroysWhenAsked(t *testing.T) {
	for destroy, wantQuery := range map[bool]string{false: "", true: "destroy_provider_resources=true"} {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/org/object-storage-buckets/bucket-1" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			if r.URL.RawQuery != wantQuery {
				t.Errorf("destroy=%v query = %q, want %q", destroy, r.URL.RawQuery, wantQuery)
			}
			w.WriteHeader(http.StatusNoContent)
		}
		if deleteError := newTestClient(t, handler).DeleteObjectStorageBucket("bucket-1", destroy); deleteError != nil {
			t.Fatalf("destroy=%v: %v", destroy, deleteError)
		}
	}
}
