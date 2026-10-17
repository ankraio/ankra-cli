package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func decodeRequestBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	raw, readError := io.ReadAll(request.Body)
	if readError != nil {
		t.Fatalf("read request body: %v", readError)
	}
	decoded := map[string]any{}
	if unmarshalError := json.Unmarshal(raw, &decoded); unmarshalError != nil {
		t.Fatalf("decode request body %q: %v", raw, unmarshalError)
	}
	return decoded
}

func TestCreateOrganisationVariableSendsIsSecret(t *testing.T) {
	var received map[string]any
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/variables" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		received = decodeRequestBody(t, request)
		jsonResponse(t, writer, http.StatusOK, map[string]any{
			"variable": map[string]any{"name": "API_TOKEN", "value": nil, "is_secret": true},
		})
	})

	result, createError := apiClient.CreateOrganisationVariable(context.Background(), "API_TOKEN", "s3cret", "", true)
	if createError != nil {
		t.Fatalf("create: %v", createError)
	}
	if received["is_secret"] != true || received["value"] != "s3cret" {
		t.Errorf("request body = %v, want value s3cret and is_secret true", received)
	}
	if !result.Variable.IsSecret || result.Variable.Value != "" {
		t.Errorf("variable = %+v, want a secret with no value", result.Variable)
	}
}

func TestCreateOrganisationVariableOmitsIsSecretForPlainVariable(t *testing.T) {
	var received map[string]any
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		received = decodeRequestBody(t, request)
		jsonResponse(t, writer, http.StatusOK, map[string]any{"variable": map[string]any{"name": "DB_HOST", "value": "db"}})
	})

	if _, createError := apiClient.CreateOrganisationVariable(context.Background(), "DB_HOST", "db", "", false); createError != nil {
		t.Fatalf("create: %v", createError)
	}
	if _, present := received["is_secret"]; present {
		t.Errorf("request body = %v, want no is_secret for a plain variable", received)
	}
}

func TestUpdateOrganisationVariableSecretFlag(t *testing.T) {
	isSecret := true
	isPlain := false
	cases := []struct {
		name         string
		secret       *bool
		wantPresent  bool
		wantIsSecret bool
	}{
		{name: "nil keeps the stored flag", secret: nil, wantPresent: false},
		{name: "true marks it secret", secret: &isSecret, wantPresent: true, wantIsSecret: true},
		{name: "false makes it plain", secret: &isPlain, wantPresent: true, wantIsSecret: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var received map[string]any
			apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPut || request.URL.Path != "/api/v1/org/variables/API_TOKEN" {
					t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
				}
				received = decodeRequestBody(t, request)
				jsonResponse(t, writer, http.StatusOK, map[string]any{"variable": map[string]any{"name": "API_TOKEN"}})
			})
			if _, updateError := apiClient.UpdateOrganisationVariable(context.Background(), "API_TOKEN", "v", "", testCase.secret); updateError != nil {
				t.Fatalf("update: %v", updateError)
			}
			value, present := received["is_secret"]
			if present != testCase.wantPresent {
				t.Fatalf("is_secret present = %v, want %v (body %v)", present, testCase.wantPresent, received)
			}
			if present && value != testCase.wantIsSecret {
				t.Errorf("is_secret = %v, want %v", value, testCase.wantIsSecret)
			}
		})
	}
}

func TestListOrganisationVariablesDecodesSecretWithNullValue(t *testing.T) {
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusOK, map[string]any{"variables": []map[string]any{
			{"name": "API_TOKEN", "value": nil, "is_secret": true},
			{"name": "DB_HOST", "value": "db", "is_secret": false},
		}})
	})

	list, listError := apiClient.ListOrganisationVariables(context.Background())
	if listError != nil {
		t.Fatalf("list: %v", listError)
	}
	if len(list.Variables) != 2 || !list.Variables[0].IsSecret || list.Variables[0].Value != "" || list.Variables[1].IsSecret {
		t.Errorf("variables = %+v", list.Variables)
	}
}

func TestRevealOrganisationVariable(t *testing.T) {
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.EscapedPath() != "/api/v1/org/variables/API%20TOKEN/value" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.EscapedPath())
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"name": "API TOKEN", "value": "s3cret"})
	})

	revealed, revealError := apiClient.RevealOrganisationVariable(context.Background(), "API TOKEN")
	if revealError != nil {
		t.Fatalf("reveal: %v", revealError)
	}
	if revealed.Name != "API TOKEN" || revealed.Value != "s3cret" {
		t.Errorf("revealed = %+v", revealed)
	}
}

func TestRevealOrganisationVariableNotFound(t *testing.T) {
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusNotFound, map[string]any{"detail": "Variable not found"})
	})

	_, revealError := apiClient.RevealOrganisationVariable(context.Background(), "MISSING")
	if !errors.Is(revealError, ErrVariableNotFound) {
		t.Errorf("error = %v, want ErrVariableNotFound", revealError)
	}
}

func TestRevealOrganisationVariableForbidden(t *testing.T) {
	apiClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusForbidden, map[string]any{"detail": "Permission denied"})
	})

	_, revealError := apiClient.RevealOrganisationVariable(context.Background(), "API_TOKEN")
	var unexpected *UnexpectedResponseError
	if !errors.As(revealError, &unexpected) || unexpected.StatusCode != http.StatusForbidden {
		t.Errorf("error = %v, want an unexpected 403 response", revealError)
	}
}
