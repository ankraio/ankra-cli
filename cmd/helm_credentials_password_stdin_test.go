package cmd

import (
	"testing"

	"ankra/internal/client"
)

type helmCredentialPasswordMock struct {
	baseMock
	createRequests []client.CreateHelmCredentialRequest
	updateRequests []client.UpdateHelmCredentialRequest
}

func (m *helmCredentialPasswordMock) CreateHelmRegistryCredential(request client.CreateHelmCredentialRequest) (*client.CreateHelmCredentialResponse, error) {
	m.createRequests = append(m.createRequests, request)
	return &client.CreateHelmCredentialResponse{}, nil
}

func (m *helmCredentialPasswordMock) UpdateHelmRegistryCredential(_ string, request client.UpdateHelmCredentialRequest) error {
	m.updateRequests = append(m.updateRequests, request)
	return nil
}

func TestHelmCredentialsCreate_PasswordStdinSkipsPrompt(t *testing.T) {
	resetConfirmFlag(t, helmCredentialsCreateCmd)
	mock := &helmCredentialPasswordMock{}
	out, err := runWithInput(t, mock, "s3cret token\n", "helm", "credentials", "create",
		"--name", "harbor", "--username", "robot$ci", "--password-stdin")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.createRequests) != 1 {
		t.Fatalf("expected one create call, got %d", len(mock.createRequests))
	}
	request := mock.createRequests[0]
	if request.Name != "harbor" || request.Username != "robot$ci" || request.Password != "s3cret token" {
		t.Errorf("request = %+v, want the piped password with only its trailing newline dropped", request)
	}
}

func TestHelmCredentialsCreate_EmptyPasswordStdinExitsUsage(t *testing.T) {
	resetConfirmFlag(t, helmCredentialsCreateCmd)
	mock := &helmCredentialPasswordMock{}
	_, err := runWithInput(t, mock, "\n", "helm", "credentials", "create",
		"--name", "harbor", "--username", "robot", "--password-stdin")
	if got := exitCodeFor(err); got != exitUsage {
		t.Fatalf("an empty stdin password should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.createRequests) != 0 {
		t.Error("no credential may be created without a password")
	}
}

func TestHelmCredentialsUpdate_PasswordStdin(t *testing.T) {
	resetConfirmFlag(t, helmCredentialsUpdateCmd)
	mock := &helmCredentialPasswordMock{}
	out, err := runWithInput(t, mock, "rotated\r\n", "helm", "credentials", "update", "harbor",
		"--username", "robot", "--password-stdin")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.updateRequests) != 1 || mock.updateRequests[0].Password != "rotated" {
		t.Errorf("update requests = %+v, want password \"rotated\"", mock.updateRequests)
	}
}
