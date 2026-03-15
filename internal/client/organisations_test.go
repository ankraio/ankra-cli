package client

import (
	"net/http"
	"testing"
)

func TestListOrganisations(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/organisation" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, []OrganisationSummary{
			{OrganisationID: "org1", Name: strPtr("Org One"), UserCurrent: true},
		})
	})
	got, err := testClient.ListOrganisations()
	if err != nil {
		t.Fatalf("ListOrganisations() error = %v", err)
	}
	if len(got) != 1 || got[0].OrganisationID != "org1" {
		t.Errorf("ListOrganisations() got = %v", got)
	}
}

func TestSwitchOrganisation(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/org/organisation/switch" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, SwitchOrganisationResponse{Success: true, Message: "switched"})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("switch failed"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.SwitchOrganisation("org-id")
			if (err != nil) != tt.wantErr {
				t.Errorf("SwitchOrganisation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("SwitchOrganisation() got.Success = %v, want true", got.Success)
			}
		})
	}
}

func TestCreateOrganisation(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/organisation" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, CreateOrganisationResponse{
			OrganisationID: "new-org-id",
			Message:        "created",
		})
	})
	got, err := testClient.CreateOrganisation("New Org", nil)
	if err != nil {
		t.Fatalf("CreateOrganisation() error = %v", err)
	}
	if got.OrganisationID != "new-org-id" {
		t.Errorf("CreateOrganisation() got.OrganisationID = %v, want new-org-id", got.OrganisationID)
	}
}
