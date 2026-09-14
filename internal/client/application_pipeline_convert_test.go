package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The one-click body is `{}`: the platform removes the generated workflow by
// default, and the flag is only sent when the caller asked to keep the file.
func TestConvertApplicationPipelineSendsTheOneClickBody(t *testing.T) {
	var bodies []string
	var paths []string
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		bodies = append(bodies, string(body))
		paths = append(paths, request.Method+" "+request.URL.Path)
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"outcome":"started","message":"Converted to an Ankra pipeline: it builds from the next push.",` +
			`"pull_request_url":"https://github.com/acme/shop/pull/42","pipeline_source":"ankra_pipeline",` +
			`"removed_paths":[".github/workflows/build-and-publish.yml"],` +
			`"disabled_workflows":[".github/workflows/build-and-publish.yml"]}`))
	})

	conversion, convertError := testClient.ConvertApplicationPipeline(context.Background(), "app-1", false)
	if convertError != nil {
		t.Fatalf("convert error = %v", convertError)
	}
	if conversion.Outcome != "started" || conversion.PipelineSource != "ankra_pipeline" ||
		conversion.PullRequestURL != "https://github.com/acme/shop/pull/42" ||
		len(conversion.RemovedPaths) != 1 || len(conversion.DisabledWorkflows) != 1 {
		t.Fatalf("conversion = %+v", conversion)
	}
	if _, keepError := testClient.ConvertApplicationPipeline(context.Background(), "app-1", true); keepError != nil {
		t.Fatalf("convert (keep) error = %v", keepError)
	}
	if len(paths) != 2 || paths[0] != "POST /api/v1/org/applications/app-1/pipeline-migration" {
		t.Fatalf("paths = %v", paths)
	}
	if bodies[0] != "{}" || bodies[1] != `{"remove_workflows":false}` {
		t.Fatalf("bodies = %v", bodies)
	}
}

// A refusal rides verbatim, whether the platform wrote it as `detail` (409,
// no pipeline cluster) or as the result's `message` (422, nothing to
// convert).
func TestConvertApplicationPipelineNamesTheRefusal(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusConflict, `{"detail":"The organisation has no pipeline cluster; set one with ankra org ci."}`,
			"no pipeline cluster"},
		{http.StatusUnprocessableEntity,
			`{"outcome":"nothing_to_convert","message":"This application already runs on an Ankra pipeline."}`,
			"already runs on an Ankra pipeline"},
	}
	for _, testCase := range cases {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(testCase.status)
			_, _ = writer.Write([]byte(testCase.body))
		})
		_, convertError := testClient.ConvertApplicationPipeline(context.Background(), "app-1", false)
		if convertError == nil || !strings.Contains(convertError.Error(), testCase.want) {
			t.Fatalf("status %d: error = %v, want it to name %q", testCase.status, convertError, testCase.want)
		}
		var unexpected *UnexpectedResponseError
		if !errors.As(convertError, &unexpected) || unexpected.StatusCode != testCase.status {
			t.Fatalf("status %d: error type = %T (%v)", testCase.status, convertError, convertError)
		}
	}
}

func TestConvertApplicationPipelineUnauthorized(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	})
	_, convertError := testClient.ConvertApplicationPipeline(context.Background(), "app-1", false)
	if !errors.Is(convertError, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", convertError)
	}
}
