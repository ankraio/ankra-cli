package client

import (
	"net/http"
	"testing"
)

func TestDriftResourcesFromStepResult(t *testing.T) {
	result := map[string]any{
		"tasks": []any{
			map[string]any{
				"json_output": map[string]any{
					"detail": map[string]any{
						"drift_resources": []any{
							map[string]any{
								"api_version": "apps/v1",
								"kind":        "DaemonSet",
								"namespace":   "fluent-bit",
								"name":        "fluent-bit",
								"drift_type":  "extra_field",
								"paths":       []any{"/spec/template/spec/hostNetwork"},
							},
						},
					},
				},
			},
		},
	}
	driftResources := DriftResourcesFromStepResult(result)
	if len(driftResources) != 1 {
		t.Fatalf("driftResources = %#v", driftResources)
	}
	if driftResources[0].Kind != "DaemonSet" || driftResources[0].Paths[0] != "/spec/template/spec/hostNetwork" {
		t.Fatalf("unexpected drift resource: %#v", driftResources[0])
	}
}

func TestDriftResourcesFromStepResultKeepsMissingResourceEntries(t *testing.T) {
	result := map[string]any{
		"tasks": []any{
			map[string]any{
				"json_output": map[string]any{
					"detail": map[string]any{
						"drift_resources": []any{
							map[string]any{
								"api_version": "v1",
								"kind":        "ConfigMap",
								"namespace":   "monitoring",
								"name":        "grafana-dashboards",
								"drift_type":  "missing",
							},
						},
					},
				},
			},
		},
	}
	driftResources := DriftResourcesFromStepResult(result)
	if len(driftResources) != 1 {
		t.Fatalf("driftResources = %#v", driftResources)
	}
	if driftResources[0].DriftType != "missing" || len(driftResources[0].Paths) != 0 {
		t.Fatalf("unexpected drift resource: %#v", driftResources[0])
	}
}

func TestEnrichExecutionDetailWithDrift(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/org/executions/exec-1/result":
			jsonResponse(t, writer, http.StatusOK, ExecutionResultResponse{
				ExecutionID: "exec-1",
				Results: []StepResult{
					{
						StepID: "step-1",
						Result: map[string]any{
							"tasks": []any{
								map[string]any{
									"json_output": map[string]any{
										"detail": map[string]any{
											"drift_resources": []any{
												map[string]any{
													"kind":       "Service",
													"name":       "prometheus",
													"namespace":  "monitoring",
													"drift_type": "extra_field",
													"paths":      []any{"/spec/publishNotReadyAddresses"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})

	detail := ExecutionDetail{
		Execution: ExecutionSummary{ID: "exec-1"},
		Steps: []ExecutionStep{
			{ID: "step-1", Name: "reconcile fluent-bit"},
		},
	}
	if enrichError := testClient.EnrichExecutionDetailWithDrift(&detail); enrichError != nil {
		t.Fatalf("EnrichExecutionDetailWithDrift error = %v", enrichError)
	}
	if len(detail.Steps[0].DriftResources) != 1 {
		t.Fatalf("DriftResources = %#v", detail.Steps[0].DriftResources)
	}
}

func TestEnrichExecutionDetailWithDriftReturnsErrorWhenRouteMissing(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})

	detail := ExecutionDetail{
		Execution: ExecutionSummary{ID: "exec-1"},
		Steps: []ExecutionStep{
			{ID: "step-1", Name: "reconcile fluent-bit"},
		},
	}
	if enrichError := testClient.EnrichExecutionDetailWithDrift(&detail); enrichError == nil {
		t.Fatal("expected error when /result route is missing")
	}
	if len(detail.Steps[0].DriftResources) != 0 {
		t.Fatalf("DriftResources should stay empty, got %#v", detail.Steps[0].DriftResources)
	}
}
