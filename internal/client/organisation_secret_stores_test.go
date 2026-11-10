package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type organisationStoreCall struct {
	method string
	path   string
	query  string
	raw    string
	body   map[string]any
}

// recordingOrganisationClient answers every request with status and response
// and records what was sent.
func recordingOrganisationClient(t *testing.T, status int, response string) (*Client, *organisationStoreCall) {
	t.Helper()
	seen := &organisationStoreCall{}
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		seen.method, seen.path, seen.query, seen.raw = request.Method, request.URL.EscapedPath(), request.URL.RawQuery, string(raw)
		seen.body = nil
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &seen.body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(response))
	})
	return testClient, seen
}

func requireOrganisationCall(t *testing.T, seen *organisationStoreCall, method string, path string) {
	t.Helper()
	if seen.method != method || seen.path != path {
		t.Fatalf("got %s %s, want %s %s", seen.method, seen.path, method, path)
	}
}

const testAddonDefinition = `"id":"d1","name":"gateway","display_name":"Gateway","description":null,"website_url":null,"documentation_url":null,"icon_url":null,"category":"networking","match_chart_names":["gateway"],"match_keywords":[],"match_home_url_prefixes":[],"source":"organisation","created_at":"t","updated_at":"t"`

func TestHelmAddonDefinitionsLaneParity(t *testing.T) {
	ctx := context.Background()

	testClient, seen := recordingOrganisationClient(t, http.StatusOK, `{"result":[{`+testAddonDefinition+`},null]}`)
	definitions, listError := testClient.ListHelmAddonDefinitions(ctx)
	if listError != nil || len(definitions) != 1 || definitions[0].Name != "gateway" || *definitions[0].Category != "networking" {
		t.Fatalf("definitions = %+v err = %v", definitions, listError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/helm/addon-definitions")

	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{`+testAddonDefinition+`,"chart_links":[{"id":"l1","chart_name":"edge","registry_url":"https://r","link_type":"include","source":"organisation","created_at":"t"},null]}`)
	detail, getError := testClient.GetHelmAddonDefinition(ctx, "gate way")
	if getError != nil || detail.DisplayName != "Gateway" || len(detail.ChartLinks) != 1 || detail.ChartLinks[0].ChartName != "edge" {
		t.Fatalf("detail = %+v err = %v", detail, getError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/helm/addon-definitions/gate%20way")

	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{`+testAddonDefinition+`}`)
	if _, createError := testClient.CreateHelmAddonDefinition(ctx, HelmAddonDefinitionWrite{Name: "gateway", DisplayName: "Gateway"}); createError != nil {
		t.Fatal(createError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/helm/addon-definitions")
	if lists, isList := seen.body["match_keywords"].([]any); seen.body["name"] != "gateway" || !isList || len(lists) != 0 {
		t.Fatalf("a create names the definition and sends empty lists as [], got %v", seen.body)
	}
	if _, updateError := testClient.UpdateHelmAddonDefinition(ctx, "gateway", HelmAddonDefinitionWrite{Name: "ignored", DisplayName: "Gateway"}); updateError != nil {
		t.Fatal(updateError)
	}
	requireOrganisationCall(t, seen, http.MethodPut, "/api/v1/org/helm/addon-definitions/gateway")
	if _, sentName := seen.body["name"]; sentName || seen.body["display_name"] != "Gateway" {
		t.Fatalf("an update is named by its path, not its body: %v", seen.body)
	}
	if deleteError := testClient.DeleteHelmAddonDefinition(ctx, "gateway"); deleteError != nil {
		t.Fatal(deleteError)
	}
	requireOrganisationCall(t, seen, http.MethodDelete, "/api/v1/org/helm/addon-definitions/gateway")

	link := HelmAddonChartLinkRequest{ChartName: "edge", RegistryURL: "https://r.example.com", LinkType: "exclude"}
	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{"id":"l1","chart_name":"edge","registry_url":"https://r.example.com","link_type":"exclude","source":"organisation","created_at":"t"}`)
	created, linkError := testClient.LinkHelmAddonChart(ctx, "gateway", link)
	if linkError != nil || created.LinkType != "exclude" {
		t.Fatalf("link = %+v err = %v", created, linkError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/helm/addon-definitions/gateway/chart-links")
	if seen.body["chart_name"] != "edge" || seen.body["link_type"] != "exclude" {
		t.Fatalf("body = %v", seen.body)
	}
	if unlinkError := testClient.UnlinkHelmAddonChart(ctx, "gateway", link); unlinkError != nil {
		t.Fatal(unlinkError)
	}
	requireOrganisationCall(t, seen, http.MethodDelete, "/api/v1/org/helm/addon-definitions/gateway/chart-links")
	if seen.query != "chart_name=edge&link_type=exclude&registry_url=https%3A%2F%2Fr.example.com" || seen.raw != "" {
		t.Fatalf("an unlink names the link by query parameters and sends no body, got %q and %q", seen.query, seen.raw)
	}

	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{"result":[{"type":"addon","name":"gateway","display_name":"Gateway","chart_count":1,"installed_count":2,"charts":[null]}],"pagination":{"page":2,"page_size":5,"total_pages":3,"total_count":11},"categories":[],"category_options":["networking"]}`)
	page, catalogError := testClient.ListHelmAddonCatalog(ctx, HelmAddonCatalogOptions{Page: 2, PageSize: 5, Category: "networking", Types: "addon,manifest_addon"})
	if catalogError != nil || len(page.Result) != 1 || page.Pagination.TotalCount != 11 || page.CategoryOptions[0] != "networking" {
		t.Fatalf("page = %+v err = %v", page, catalogError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/helm/addon-catalog")
	if seen.query != "category=networking&page=2&page_size=5&types=addon%2Cmanifest_addon" {
		t.Fatalf("only the filters that were set are sent, got %q", seen.query)
	}

	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{"addon":{`+testAddonDefinition+`},"charts":[],"clusters":[{"cluster_id":"c1","cluster_name":"prod"}],"installed_count":1,"category_options":[]}`)
	entry, entryError := testClient.GetHelmAddonCatalogEntry(ctx, "gateway")
	if entryError != nil || entry.Addon == nil || entry.Addon.Name != "gateway" || entry.Clusters[0].ClusterName != "prod" {
		t.Fatalf("entry = %+v err = %v", entry, entryError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/helm/addon-catalog/gateway")
}

func TestDatasourceCredentialsLaneParity(t *testing.T) {
	ctx := context.Background()
	testClient, seen := recordingOrganisationClient(t, http.StatusOK, `[{"id":"c1","name":"grafana","provider":"datasource","available":true}]`)
	credentials, listError := testClient.ListDatasourceCredentials(ctx)
	if listError != nil || len(credentials) != 1 || credentials[0].Name != "grafana" {
		t.Fatalf("credentials = %+v err = %v", credentials, listError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/credentials/datasource")

	testClient, seen = recordingOrganisationClient(t, http.StatusCreated, `{"success":true,"id":"c1","name":"grafana","errors":[]}`)
	result, createError := testClient.CreateDatasourceCredential(ctx, CreateDatasourceCredentialRequest{Name: "grafana", Token: "tok"})
	if createError != nil || !result.Success || result.ID != "c1" {
		t.Fatalf("result = %+v err = %v", result, createError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/credentials/datasource")
	if seen.raw != `{"name":"grafana","token":"tok"}` {
		t.Fatalf("a token credential sends its name and token and nothing else, got %s", seen.raw)
	}
}

// A refusal that echoes the request must not carry the secret into the
// error: the body is redacted before it is put into a message.
func TestOrganisationSecretStoreErrorsRedactAnEchoedBody(t *testing.T) {
	ctx := context.Background()
	testClient, _ := recordingOrganisationClient(t, http.StatusBadRequest, `{"name":"grafana","token":"tok-must-not-leak","password":"pw-must-not-leak"}`)
	_, createError := testClient.CreateDatasourceCredential(ctx, CreateDatasourceCredentialRequest{Name: "grafana", Token: "tok-must-not-leak"})
	if createError == nil || strings.Contains(createError.Error(), "must-not-leak") {
		t.Fatalf("the echoed secret must be redacted, got %v", createError)
	}
	forbiddenClient, _ := recordingOrganisationClient(t, http.StatusForbidden, `{"detail":"permission_denied","permission":"credentials.write"}`)
	_, deniedError := forbiddenClient.CreateDatasourceCredential(ctx, CreateDatasourceCredentialRequest{Name: "grafana", Token: "tok"})
	var denied *PermissionDeniedError
	if !errors.As(deniedError, &denied) || denied.Permission != "credentials.write" {
		t.Fatalf("an RBAC refusal must name the permission, got %v", deniedError)
	}
	unauthorisedClient, _ := recordingOrganisationClient(t, http.StatusUnauthorized, `{}`)
	if _, listError := unauthorisedClient.ListSecurityEvidenceDestinations(ctx); !errors.Is(listError, ErrUnauthorized) {
		t.Fatalf("a 401 must be ErrUnauthorized, got %v", listError)
	}
}

func TestSecurityEvidenceLaneParity(t *testing.T) {
	ctx := context.Background()
	destination := `{"id":"d1","name":"siem","kind":"webhook","config":{"url":"https://siem"},"credential_configured":true,"enabled":true,"revoked_at":null}`

	testClient, seen := recordingOrganisationClient(t, http.StatusOK, `{"result":[`+destination+`]}`)
	destinations, listError := testClient.ListSecurityEvidenceDestinations(ctx)
	if listError != nil || len(destinations) != 1 || destinations[0].Config.URL != "https://siem" || !destinations[0].CredentialConfigured {
		t.Fatalf("destinations = %+v err = %v", destinations, listError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/security/evidence/destinations")

	testClient, seen = recordingOrganisationClient(t, http.StatusCreated, destination)
	if _, createError := testClient.CreateSecurityEvidenceDestination(ctx, CreateSecurityEvidenceDestinationRequest{
		Name: "siem", Kind: "webhook", Config: SecurityEvidenceDestinationConfig{URL: "https://siem"},
		Credential: map[string]string{"signing_secret": "s"},
	}); createError != nil {
		t.Fatal(createError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/security/evidence/destinations")
	credential, _ := seen.body["credential"].(map[string]any)
	configuration, _ := seen.body["config"].(map[string]any)
	if credential["signing_secret"] != "s" || configuration["url"] != "https://siem" || len(configuration) != 1 {
		t.Fatalf("body = %v", seen.body)
	}
	if _, revokeError := testClient.RevokeSecurityEvidenceDestination(ctx, "d1"); revokeError != nil {
		t.Fatal(revokeError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/security/evidence/destinations/d1/revoke")

	delivery := `{"id":"e1","destination_id":"d1","status":"uploaded","framework":null,"period":"2026-10","complete":true,"attempts":1,"receipt":{"vanta":"x"}}`
	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{"result":[`+delivery+`]}`)
	deliveries, deliveriesError := testClient.ListSecurityEvidenceDeliveries(ctx, "d1", 10)
	if deliveriesError != nil || len(deliveries) != 1 || deliveries[0].Status != "uploaded" || deliveries[0].Receipt["vanta"] != "x" {
		t.Fatalf("deliveries = %+v err = %v", deliveries, deliveriesError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/security/evidence/deliveries")
	if seen.query != "destination_id=d1&limit=10" {
		t.Fatalf("query = %q", seen.query)
	}
	if _, unfilteredError := testClient.ListSecurityEvidenceDeliveries(ctx, "", 0); unfilteredError != nil || seen.query != "" {
		t.Fatalf("no filter sends no query, got %q err = %v", seen.query, unfilteredError)
	}

	testClient, seen = recordingOrganisationClient(t, http.StatusAccepted, delivery)
	month := "2026-10"
	if _, sendError := testClient.SendSecurityEvidence(ctx, SendSecurityEvidenceRequest{DestinationID: "d1", Month: &month}); sendError != nil {
		t.Fatal(sendError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/security/evidence/deliveries")
	if seen.raw != `{"destination_id":"d1","month":"2026-10"}` {
		t.Fatalf("an omitted framework is not sent, got %s", seen.raw)
	}
	if _, submitError := testClient.SubmitSecurityEvidenceDelivery(ctx, "e1"); submitError != nil {
		t.Fatal(submitError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/security/evidence/deliveries/e1/submit")
}

func TestServiceCredentialsLaneParity(t *testing.T) {
	ctx := context.Background()
	family := `{"id":"f1","name":"orders-db","created_at":"t","active_value_id":null}`
	grant := `{"id":"g1","value_id":"v1","package_digest":"sha256:aa","input_name":"PASSWORD","mode":"existing","created_at":"t","revoked_at":null}`

	testClient, seen := recordingOrganisationClient(t, http.StatusOK, `{"items":[`+family+`],"next_cursor":"f1"}`)
	families, listError := testClient.ListServiceCredentialFamilies(ctx, ServicePageOptions{Limit: 50, After: "f0"})
	if listError != nil || len(families.Items) != 1 || families.Items[0].ActiveValueID != nil || *families.NextCursor != "f1" {
		t.Fatalf("families = %+v err = %v", families, listError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/service-admission/credentials")
	if seen.query != "after=f0&limit=50" {
		t.Fatalf("query = %q", seen.query)
	}

	testClient, seen = recordingOrganisationClient(t, http.StatusCreated, family)
	if _, createError := testClient.CreateServiceCredentialFamily(ctx, "orders-db"); createError != nil {
		t.Fatal(createError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/service-admission/credentials")
	if seen.raw != `{"name":"orders-db"}` {
		t.Fatalf("body = %s", seen.raw)
	}
	if _, getError := testClient.GetServiceCredentialFamily(ctx, "f1"); getError != nil {
		t.Fatal(getError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/service-admission/credentials/f1")

	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{"items":[{"id":"v1","state":"active","created_at":"t","activated_at":"t","retired_at":null,"retained":true}],"next_cursor":null}`)
	values, valuesError := testClient.ListServiceCredentialValues(ctx, "f1", ServicePageOptions{})
	if valuesError != nil || len(values.Items) != 1 || values.Items[0].State != "active" || values.NextCursor != nil {
		t.Fatalf("values = %+v err = %v", values, valuesError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/service-admission/credentials/f1/values")

	testClient, seen = recordingOrganisationClient(t, http.StatusCreated, `{"value":{"id":"v2","state":"active","created_at":"t","activated_at":"t","retired_at":null,"retained":true},"replaced":[`+grant+`],"granted":[]}`)
	rotation, firstError := testClient.AddServiceCredentialValue(ctx, "f1", "the-value", nil)
	if firstError != nil || rotation.Value.ID != "v2" || len(rotation.Replaced) != 1 {
		t.Fatalf("rotation = %+v err = %v", rotation, firstError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/service-admission/credentials/f1/values")
	if seen.raw != `{"value":"the-value","expected_value_id":null}` {
		t.Fatalf("a first value sends expected_value_id null, not nothing, got %s", seen.raw)
	}
	expected := "v1"
	if _, rotateError := testClient.AddServiceCredentialValue(ctx, "f1", "the-value", &expected); rotateError != nil {
		t.Fatal(rotateError)
	}
	if seen.body["expected_value_id"] != "v1" {
		t.Fatalf("body = %v", seen.body)
	}

	testClient, seen = recordingOrganisationClient(t, http.StatusOK, `{"items":[`+grant+`],"next_cursor":null}`)
	grants, grantsError := testClient.ListServiceCredentialGrants(ctx, "f1", ServicePageOptions{Limit: 25})
	if grantsError != nil || len(grants.Items) != 1 || grants.Items[0].InputName != "PASSWORD" {
		t.Fatalf("grants = %+v err = %v", grants, grantsError)
	}
	requireOrganisationCall(t, seen, http.MethodGet, "/api/v1/org/service-admission/credentials/f1/grants")

	testClient, seen = recordingOrganisationClient(t, http.StatusCreated, grant)
	if _, issueError := testClient.IssueServiceCredentialGrant(ctx, IssueServiceCredentialGrantRequest{
		FamilyID: "f1", PackageDigest: "sha256:aa", InputName: "PASSWORD", Mode: "existing",
	}); issueError != nil {
		t.Fatal(issueError)
	}
	requireOrganisationCall(t, seen, http.MethodPost, "/api/v1/org/service-admission/credential-grants")
	if seen.body["value_id"] != ServiceCredentialActiveValue || seen.body["family_id"] != "f1" {
		t.Fatalf("an empty value id names the active value, got %v", seen.body)
	}

	revokeClient, revokeSeen := recordingOrganisationClient(t, http.StatusNoContent, "")
	if revokeError := revokeClient.RevokeServiceCredentialGrant(ctx, "g1"); revokeError != nil {
		t.Fatal(revokeError)
	}
	requireOrganisationCall(t, revokeSeen, http.MethodDelete, "/api/v1/org/service-admission/credential-grants/g1")

	conflictClient, _ := recordingOrganisationClient(t, http.StatusConflict, `{"detail":"Credential state changed"}`)
	_, conflictError := conflictClient.AddServiceCredentialValue(ctx, "f1", "the-value", nil)
	var unexpected *UnexpectedResponseError
	if !errors.As(conflictError, &unexpected) || unexpected.StatusCode != http.StatusConflict || unexpected.Detail != "Credential state changed" {
		t.Fatalf("a 409 must keep its status and detail, got %v", conflictError)
	}
}
