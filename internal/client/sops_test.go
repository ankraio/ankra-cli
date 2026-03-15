package client

import (
	"net/http"
	"testing"
)

func TestGetSopsConfig(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/sops/config" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, SopsConfigResult{
			OrganisationID: "org1",
			AgePublicKey:   "age1xxx",
			Enabled:        true,
			Initialized:    true,
		})
	})
	got, err := testClient.GetSopsConfig()
	if err != nil {
		t.Fatalf("GetSopsConfig() error = %v", err)
	}
	if got.OrganisationID != "org1" || !got.Enabled {
		t.Errorf("GetSopsConfig() got = %v", got)
	}
}

func TestEncryptYAML(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/org/sops/encrypt" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, EncryptContentResponse{
					EncryptedYaml: "ENC[AES256_GCM,data:xxx]",
					Success:       true,
				})
			},
			wantErr: false,
		},
		{
			name: "API error with Detail",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusBadRequest, APIErrorResponse{Detail: "invalid path specified"})
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.EncryptYAML("key: value", []string{"key"})
			if (err != nil) != tt.wantErr {
				t.Errorf("EncryptYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != "ENC[AES256_GCM,data:xxx]" {
				t.Errorf("EncryptYAML() got = %v", got)
			}
		})
	}
}

func TestDecryptYAML(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/sops/decrypt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, DecryptContentResponse{
			DecryptedContent: "key: secret-value",
			IsEncrypted:      true,
		})
	})
	got, err := testClient.DecryptYAML("ENC[AES256_GCM,data:xxx]")
	if err != nil {
		t.Fatalf("DecryptYAML() error = %v", err)
	}
	if got != "key: secret-value" {
		t.Errorf("DecryptYAML() got = %v", got)
	}
}
