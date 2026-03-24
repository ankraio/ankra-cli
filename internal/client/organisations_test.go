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

func TestGetOrganisation(t *testing.T) {
	orgName := "Test Org"
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/organisation/org-123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, OrganisationFull{
			OrganisationID: "org-123",
			Name:           &orgName,
			CreatedAt:      "2025-01-01T00:00:00Z",
			Members: []OrganisationMember{
				{UserID: "user-1", Email: "admin@test.com", Role: "admin", JoinedAt: "2025-01-01T00:00:00Z"},
			},
		})
	})
	got, err := testClient.GetOrganisation("org-123")
	if err != nil {
		t.Fatalf("GetOrganisation() error = %v", err)
	}
	if got.OrganisationID != "org-123" || len(got.Members) != 1 {
		t.Errorf("GetOrganisation() got = %v", got)
	}
}

func TestInviteUserToOrganisation(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/organisation/invite" {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, InviteUserResponse{Success: true, Message: "invited"})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("invalid email"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.InviteUserToOrganisation(InviteUserRequest{
				OrganisationID: "org-123",
				InviteeEmail:   "new@test.com",
				Role:           "member",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("InviteUserToOrganisation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("InviteUserToOrganisation() got.Success = false, want true")
			}
		})
	}
}

func TestRemoveUserFromOrganisation(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/org/organisation/user" {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, RemoveUserResponse{Success: true, Message: "removed"})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("not allowed"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.RemoveUserFromOrganisation(RemoveUserRequest{
				UserID:         "user-456",
				OrganisationID: "org-123",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("RemoveUserFromOrganisation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("RemoveUserFromOrganisation() got.Success = false, want true")
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
