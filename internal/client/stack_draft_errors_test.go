package client

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func draftErrorTestStack() Stack {
	return Stack{
		Name:      "monitoring",
		Addons:    []Addon{{Name: "prometheus"}, {Name: "grafana"}},
		Manifests: []Manifest{{Name: "namespace"}},
	}
}

func TestCreateStackDraftDecodesEveryRefusalShape(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		body       string
		want       []ImportResponseResourceError
	}{
		{
			name:       "422 FastAPI validation resolves addon index to its name",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":[{"loc":["body","spec","stacks",0,"addons",1,"registry_name"],"msg":"Field required","type":"missing"},{"loc":["body","spec","stacks",0,"addons",1,"registry_url"],"msg":"Field required","type":"missing"}]}`,
			want: []ImportResponseResourceError{
				{Kind: "addon", Name: "grafana", Errors: []ImportResponseErrorItem{{Key: "registry_name", Message: "Field required"}}},
				{Kind: "addon", Name: "grafana", Errors: []ImportResponseErrorItem{{Key: "registry_url", Message: "Field required"}}},
			},
		},
		{
			name:       "422 FastAPI validation on a manifest",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":[{"loc":["body","spec","stacks",0,"manifests",0,"manifest_base64"],"msg":"Invalid base64","type":"value_error"}]}`,
			want: []ImportResponseResourceError{
				{Kind: "manifest", Name: "namespace", Errors: []ImportResponseErrorItem{{Key: "manifest_base64", Message: "Invalid base64"}}},
			},
		},
		{
			name:       "422 FastAPI validation with an index past the submitted stack",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":[{"loc":["body","spec","stacks",0,"addons",7,"chart_name"],"msg":"Field required","type":"missing"}]}`,
			want: []ImportResponseResourceError{
				{Kind: "addon", Name: "#7", Errors: []ImportResponseErrorItem{{Key: "chart_name", Message: "Field required"}}},
			},
		},
		{
			name:       "422 FastAPI validation on the stack itself",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":[{"loc":["body","spec","stacks",0,"name"],"msg":"String should have at least 1 character","type":"string_too_short"}]}`,
			want: []ImportResponseResourceError{
				{Kind: "stack", Name: "monitoring", Errors: []ImportResponseErrorItem{{Key: "name", Message: "String should have at least 1 character"}}},
			},
		},
		{
			name:       "422 plain string detail",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":"Addon grafana is missing registry_name or registry_url"}`,
			want: []ImportResponseResourceError{
				{Kind: "stack", Name: "monitoring", Errors: []ImportResponseErrorItem{{Message: "Addon grafana is missing registry_name or registry_url"}}},
			},
		},
		{
			name:       "422 resource error list",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":[{"name":"grafana","kind":"addon","errors":[{"key":"chart_name","message":"not found"}]}]}`,
			want: []ImportResponseResourceError{
				{Kind: "addon", Name: "grafana", Errors: []ImportResponseErrorItem{{Key: "chart_name", Message: "not found"}}},
			},
		},
		{
			name:       "422 resource error without items still says something",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":[{"name":"grafana","errors":[]}]}`,
			want: []ImportResponseResourceError{
				{Kind: "resource", Name: "grafana", Errors: []ImportResponseErrorItem{{Message: "rejected by the platform without a reason"}}},
			},
		},
		{
			name:       "422 detail object with a message",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"detail":{"message":"stack is locked"}}`,
			want: []ImportResponseResourceError{
				{Kind: "stack", Name: "monitoring", Errors: []ImportResponseErrorItem{{Message: "stack is locked"}}},
			},
		},
		{
			name:       "422 top-level errors list",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"errors":[{"name":"namespace","kind":"manifest","errors":[{"key":"parents","message":"unknown parent"}]}]}`,
			want: []ImportResponseResourceError{
				{Kind: "manifest", Name: "namespace", Errors: []ImportResponseErrorItem{{Key: "parents", Message: "unknown parent"}}},
			},
		},
		{
			name:       "200 with embedded resource errors",
			statusCode: http.StatusOK,
			body:       `{"draft_id":"","errors":[{"name":"grafana","kind":"addon","errors":[{"key":"registry_url","message":"required"}]}]}`,
			want: []ImportResponseResourceError{
				{Kind: "addon", Name: "grafana", Errors: []ImportResponseErrorItem{{Key: "registry_url", Message: "required"}}},
			},
		},
		{
			name:       "200 with embedded FastAPI-style errors",
			statusCode: http.StatusOK,
			body:       `{"errors":[{"loc":["spec","stacks",0,"addons",0,"chart_version"],"msg":"Field required"}]}`,
			want: []ImportResponseResourceError{
				{Kind: "addon", Name: "prometheus", Errors: []ImportResponseErrorItem{{Key: "chart_version", Message: "Field required"}}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(testCase.statusCode)
				_, _ = writer.Write([]byte(testCase.body))
			}
			result, draftError := newTestClient(t, handler).CreateStackDraft(context.Background(), "cluster-1", draftErrorTestStack())
			if draftError != nil {
				t.Fatalf("CreateStackDraft() unexpected error: %v", draftError)
			}
			if !reflect.DeepEqual(result.Errors, testCase.want) {
				t.Errorf("CreateStackDraft() errors =\n  %+v\nwant\n  %+v", result.Errors, testCase.want)
			}
		})
	}
}

func TestCreateStackDraftSuccessIgnoresEmptyErrorsAndDetail(t *testing.T) {
	testCases := map[string]string{
		"empty errors list":               `{"draft_id":"draft-1","errors":[]}`,
		"null errors":                     `{"draft_id":"draft-1","errors":null}`,
		"informational detail on success": `{"draft_id":"draft-1","detail":"saved"}`,
	}
	for name, body := range testCases {
		t.Run(name, func(t *testing.T) {
			handler := func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write([]byte(body))
			}
			result, draftError := newTestClient(t, handler).CreateStackDraft(context.Background(), "cluster-1", draftErrorTestStack())
			if draftError != nil {
				t.Fatalf("CreateStackDraft() unexpected error: %v", draftError)
			}
			if result.DraftID != "draft-1" || len(result.Errors) != 0 {
				t.Errorf("CreateStackDraft() = %+v, want draft-1 with no errors", result)
			}
		})
	}
}

func TestCreateStackDraftUnreadable422KeepsTheBody(t *testing.T) {
	testCases := map[string]string{
		"not JSON":     `upstream exploded`,
		"empty detail": `{"detail":[]}`,
		"blank string": `{"detail":"  "}`,
	}
	for name, body := range testCases {
		t.Run(name, func(t *testing.T) {
			handler := func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = writer.Write([]byte(body))
			}
			if _, draftError := newTestClient(t, handler).CreateStackDraft(context.Background(), "cluster-1", draftErrorTestStack()); draftError == nil {
				t.Error("CreateStackDraft() expected an error for an unreadable 422")
			}
		})
	}
}

func TestResourceErrorFromLocationFallbacks(t *testing.T) {
	stack := draftErrorTestStack()
	testCases := []struct {
		name     string
		location []any
		message  string
		want     ImportResponseResourceError
	}{
		{
			name:     "no location and no message",
			location: nil,
			message:  "",
			want:     ImportResponseResourceError{Kind: "stack", Name: "monitoring", Errors: []ImportResponseErrorItem{{Message: "invalid value"}}},
		},
		{
			name:     "non-numeric addon index stays a field path",
			location: []any{"body", "spec", "stacks", float64(0), "addons", "grafana", "values"},
			message:  "bad",
			want:     ImportResponseResourceError{Kind: "stack", Name: "monitoring", Errors: []ImportResponseErrorItem{{Key: "addons.grafana.values", Message: "bad"}}},
		},
		{
			name:     "unexpected member type",
			location: []any{"body", true},
			message:  "odd",
			want:     ImportResponseResourceError{Kind: "stack", Name: "monitoring", Errors: []ImportResponseErrorItem{{Key: "true", Message: "odd"}}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := resourceErrorFromLocation(testCase.location, testCase.message, stack)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("resourceErrorFromLocation() = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

func TestStackDraftErrorsFromBodyIgnoresNonJSON(t *testing.T) {
	if got := stackDraftErrorsFromBody([]byte("not json"), draftErrorTestStack()); got != nil {
		t.Errorf("stackDraftErrorsFromBody() = %+v, want nil", got)
	}
	if got := stackDraftEmbeddedErrors([]byte("not json"), draftErrorTestStack()); got != nil {
		t.Errorf("stackDraftEmbeddedErrors() = %+v, want nil", got)
	}
	if got := stackDraftErrorsFromBody([]byte(`{"detail":42}`), draftErrorTestStack()); len(got) != 0 {
		t.Errorf("stackDraftErrorsFromBody() = %+v, want none", got)
	}
}
