package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrChartNotFound is returned (wrapped) by GetChartDefaultValues and
// TemplateChart when the chart version is unknown to the organisation or its
// documents have not been indexed yet.
var ErrChartNotFound = errors.New("chart not found")

// ChartTemplateError carries the server-side Helm error a template request
// failed with (bad values YAML, schema violations, template execution
// errors) - the exact text a failed deploy would have produced.
type ChartTemplateError struct {
	Message string
}

func (e *ChartTemplateError) Error() string { return e.Message }

// GetChartDefaultValuesResult mirrors the backend's
// GetHelmChartDefaultValuesResult payload.
type GetChartDefaultValuesResult struct {
	ValuesBase64 string `json:"values_base64"`
}

// TemplateChartRequest mirrors the backend's TemplateHelmChartRequest body.
type TemplateChartRequest struct {
	RepositoryName string  `json:"repository_name"`
	ChartName      string  `json:"chart_name"`
	ChartVersion   string  `json:"chart_version"`
	ValuesBase64   *string `json:"values_base64,omitempty"`
	ReleaseName    *string `json:"release_name,omitempty"`
	Namespace      *string `json:"namespace,omitempty"`
}

// RenderedChartManifest is one rendered template file, identified by its
// path inside the chart archive.
type RenderedChartManifest struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
}

// TemplateChartResult mirrors the backend's TemplateHelmChartResult payload.
type TemplateChartResult struct {
	Rendered []RenderedChartManifest `json:"rendered"`
	Notes    *string                 `json:"notes"`
}

// GetChartDefaultValues fetches a chart version's default values document
// (the `helm show values` equivalent) from the catalog.
func (c *Client) GetChartDefaultValues(repositoryName, chartName, chartVersion string) (*GetChartDefaultValuesResult, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/helm/charts/chart/%s/%s/%s/values",
		c.BaseURL, url.PathEscape(repositoryName), url.PathEscape(chartName), url.PathEscape(chartVersion))
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer closeBody(resp)
	body, readErr := readResponseBody(resp)
	if readErr != nil {
		return nil, fmt.Errorf("read response: %w", readErr)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, fmt.Errorf("chart %q version %q in repository %q: %w",
			chartName, chartVersion, repositoryName, ErrChartNotFound)
	default:
		if denied := PermissionDeniedFromResponse(resp.StatusCode, body); denied != nil {
			return nil, denied
		}
		return nil, newUnexpectedResponseError("fetch chart values", resp.StatusCode, redactedBodyForError(body, 500))
	}
	var result GetChartDefaultValuesResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

// TemplateChart renders a chart version's manifests server-side (the `helm
// template` equivalent, no cluster connection). A 422 surfaces as a
// *ChartTemplateError carrying the Helm error text verbatim.
func (c *Client) TemplateChart(request TemplateChartRequest) (*TemplateChartResult, error) {
	requestURL := c.BaseURL + "/api/v1/org/helm/charts/template"
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequest("POST", requestURL, strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer closeBody(resp)
	body, readErr := readResponseBody(resp)
	if readErr != nil {
		return nil, fmt.Errorf("read response: %w", readErr)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, fmt.Errorf("chart %q version %q in repository %q: %w",
			request.ChartName, request.ChartVersion, request.RepositoryName, ErrChartNotFound)
	case http.StatusUnprocessableEntity:
		return nil, &ChartTemplateError{Message: helmErrorFromValidationBody(body)}
	default:
		if denied := PermissionDeniedFromResponse(resp.StatusCode, body); denied != nil {
			return nil, denied
		}
		return nil, newUnexpectedResponseError("template chart", resp.StatusCode, redactedBodyForError(body, 500))
	}
	var result TemplateChartResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

// helmErrorFromValidationBody extracts the human-readable messages from a
// FastAPI-style 422 body ({"detail": [{"msg": "Value error, ..."}, ...]}),
// stripping pydantic's "Value error, " prefix so the user sees the Helm
// error exactly as a failed deploy would have reported it.
func helmErrorFromValidationBody(body []byte) string {
	var parsed struct {
		Detail []struct {
			Message string `json:"msg"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Detail) == 0 {
		return truncateForError(body, 500)
	}
	messages := make([]string, 0, len(parsed.Detail))
	for _, entry := range parsed.Detail {
		messages = append(messages, strings.TrimPrefix(entry.Message, "Value error, "))
	}
	return strings.Join(messages, "\n")
}
