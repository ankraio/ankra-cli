package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateOrgSSHKeyCredentialPostsToTheOrgWideRoute(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.Method + " " + request.URL.Path
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &gotBody)
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"success":true,"id":"aaaaaaaa-1111-4111-8111-111111111111","name":"k","private_key":null,"errors":[]}`))
	}))
	defer server.Close()

	publicKey := "ssh-ed25519 AAAA k@host"
	created, err := New("token", server.URL).CreateOrgSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "k", SSHPublicKey: &publicKey})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if gotPath != "POST /api/v1/credentials/hetzner/ssh-key" {
		t.Errorf("request = %s", gotPath)
	}
	if gotBody["name"] != "k" || gotBody["ssh_public_key"] != publicKey || gotBody["generate"] != false {
		t.Errorf("body = %#v", gotBody)
	}
	if created.ID != "aaaaaaaa-1111-4111-8111-111111111111" || created.PrivateKey != nil {
		t.Errorf("created = %+v", created)
	}
}

func TestCreateOrgSSHKeyCredentialRelaysEveryRefusalShape(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want []string
	}{
		{
			name: "duplicate public key",
			code: http.StatusBadRequest,
			body: `{"detail":{"errors":[{"name":"k","kind":"hetzner_ssh_key","errors":[{"key":"ssh_public_key","message":"An SSH key with the same public key already exists ('maas')"}]}]}}`,
			want: []string{"ssh_public_key: An SSH key with the same public key already exists ('maas')"},
		},
		{
			name: "neither key nor generate",
			code: http.StatusBadRequest,
			body: `{"detail":"Either provide ssh_public_key or set generate=true"}`,
			want: []string{"Either provide ssh_public_key or set generate=true"},
		},
		{
			name: "request validation",
			code: http.StatusUnprocessableEntity,
			body: `{"detail":[{"loc":["body","name"],"msg":"Field required","type":"missing"}]}`,
			want: []string{"name: Field required"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(testCase.code)
				_, _ = writer.Write([]byte(testCase.body))
			}))
			defer server.Close()

			_, err := New("token", server.URL).CreateOrgSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "k", Generate: true})
			var refusal *SSHKeyCredentialRefusedError
			if !errors.As(err, &refusal) {
				t.Fatalf("err = %v (%T), want *SSHKeyCredentialRefusedError", err, err)
			}
			if strings.Join(refusal.Messages, "|") != strings.Join(testCase.want, "|") {
				t.Errorf("messages = %q, want %q", refusal.Messages, testCase.want)
			}
		})
	}
}

func TestCreateOrgSSHKeyCredentialKeepsThePermissionRefusalTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"detail":"permission_denied","permission":"credentials.write"}`))
	}))
	defer server.Close()

	_, err := New("token", server.URL).CreateOrgSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "k", Generate: true})
	var denied *PermissionDeniedError
	if !errors.As(err, &denied) || denied.Permission != "credentials.write" {
		t.Fatalf("err = %v, want a PermissionDeniedError naming credentials.write", err)
	}
}

func TestListOrgSSHKeyCredentialsReadsTheOrgWideList(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.Method + " " + request.URL.Path
		_, _ = writer.Write([]byte(`[{"id":"1","name":"maas","provider":"ssh-key","available":true,"state":"up","created_at":"2026-04-11T19:33:28Z"}]`))
	}))
	defer server.Close()

	credentials, err := New("token", server.URL).ListOrgSSHKeyCredentials()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if gotPath != "GET /api/v1/credentials/hetzner/ssh-keys" {
		t.Errorf("request = %s", gotPath)
	}
	if len(credentials) != 1 || credentials[0].Name != "maas" || credentials[0].Fingerprint != nil {
		t.Errorf("credentials = %+v", credentials)
	}
}
