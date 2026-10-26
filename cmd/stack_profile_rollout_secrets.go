package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"ankra/internal/client"
)

// stackProfileSecretPlaceholder is the literal a published stack-profile
// version carries wherever a secret input is bound per cluster at
// instantiation. An export of the version renders it as written.
const stackProfileSecretPlaceholder = "ANKRA_SECRET_PARAMETER"

// viaApplySecretRefusal refuses 'stack-profiles rollout --via-apply' for a
// version that carries secrets (ankra-c42hih). That lane applies the exported
// version through the cluster apply route, and the export carries the
// placeholder where each secret is bound per cluster: applying it replaced
// every target's live secret (a database password, an API token) with the
// placeholder text. The lane has no way to carry each target's bound value
// forward - --set and friends are refused on it, and one value would be wrong
// for every cluster anyway - so it refuses instead and points at the in-place
// rollout, where the platform binds the version and keeps the bound secrets.
//
// It refuses on either signal: a declared secret input, or the placeholder
// anywhere in the request it would send (as written, or inside any base64
// body), so a version whose declaration was lost is caught as well.
func viaApplySecretRefusal(profileName string, version int, export *client.StackProfileIacExport, request client.CreateImportClusterRequest) error {
	var secretInputs []string
	for _, parameter := range export.Parameters {
		if parameter.Type == "secret" {
			secretInputs = append(secretInputs, parameter.Name)
		}
	}
	carriesPlaceholder, inspectError := requestCarriesSecretPlaceholder(request)
	if inspectError != nil {
		return fmt.Errorf("checking the exported profile version for secret placeholders: %w", inspectError)
	}
	if len(secretInputs) == 0 && !carriesPlaceholder {
		return nil
	}
	reason := fmt.Sprintf("it carries the placeholder %s where a secret goes", stackProfileSecretPlaceholder)
	if len(secretInputs) > 0 {
		reason = fmt.Sprintf("it declares secret %s (%s), which its export carries as the placeholder %s",
			pluralise(len(secretInputs), "input", "inputs"), strings.Join(secretInputs, ", "), stackProfileSecretPlaceholder)
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"profile '%s' v%d cannot be rolled out with --via-apply: %s, and applying the export would replace "+
			"the secret each cluster has bound with that text. Roll it out in place instead (drop --via-apply): "+
			"the platform binds the version there and keeps each deployment's secrets",
		profileName, version, reason))
}

// requestCarriesSecretPlaceholder reports whether any string in the request,
// or the decoded text of any base64 string in it, carries the placeholder.
func requestCarriesSecretPlaceholder(request client.CreateImportClusterRequest) (bool, error) {
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return false, marshalError
	}
	var document any
	if unmarshalError := json.Unmarshal(encoded, &document); unmarshalError != nil {
		return false, unmarshalError
	}
	return valueCarriesSecretPlaceholder(document), nil
}

func valueCarriesSecretPlaceholder(value any) bool {
	switch typed := value.(type) {
	case string:
		if strings.Contains(typed, stackProfileSecretPlaceholder) {
			return true
		}
		decoded, decodeError := base64.StdEncoding.DecodeString(strings.TrimSpace(typed))
		return decodeError == nil && strings.Contains(string(decoded), stackProfileSecretPlaceholder)
	case map[string]any:
		for _, member := range typed {
			if valueCarriesSecretPlaceholder(member) {
				return true
			}
		}
	case []any:
		for _, member := range typed {
			if valueCarriesSecretPlaceholder(member) {
				return true
			}
		}
	}
	return false
}
