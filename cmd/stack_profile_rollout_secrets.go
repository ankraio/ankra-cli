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
// version that carries secrets, or for targets whose deployment carries
// inputs of its own (ankra-c42hih). That lane applies the exported
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
//
// The same lane resets every deployment's own inputs: the export carries the
// values the version stores, not the ones each deployment was applied with,
// so a target whose deployment recorded inputs (a hostname, a replica count)
// would have them replaced. Such targets are refused too; the in-place
// rollout carries recorded inputs forward.
func viaApplySecretRefusal(profileName string, version int, export *client.StackProfileIacExport, request client.CreateImportClusterRequest, targets []rolloutTarget) error {
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
		return viaApplyRecordedInputsRefusal(profileName, version, targets)
	}
	reason := fmt.Sprintf("it carries the placeholder %s where a secret goes", stackProfileSecretPlaceholder)
	if len(secretInputs) > 0 {
		reason = fmt.Sprintf("it declares secret %s (%s), which a published version stores as the placeholder %s, never as a cluster's value",
			pluralise(len(secretInputs), "input", "inputs"), strings.Join(secretInputs, ", "), stackProfileSecretPlaceholder)
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"profile '%s' v%d cannot be rolled out with --via-apply: %s, and applying the export would replace "+
			"the secret each cluster has bound with that text. Roll it out in place instead (drop --via-apply): "+
			"the platform binds the version there and keeps each deployment's secrets",
		profileName, version, reason))
}

// viaApplyRecordedInputsRefusal refuses the --via-apply lane when any target's
// deployment recorded inputs of its own, naming those targets.
func viaApplyRecordedInputsRefusal(profileName string, version int, targets []rolloutTarget) error {
	var withInputs []string
	for _, target := range targets {
		if len(target.recordedBindings) == 0 {
			continue
		}
		names := make([]string, 0, len(target.recordedBindings))
		for _, binding := range target.recordedBindings {
			names = append(names, binding.Name)
		}
		withInputs = append(withInputs, fmt.Sprintf("%s/%s (%s)", target.ClusterName, target.StackName, strings.Join(names, ", ")))
	}
	if len(withInputs) == 0 {
		return nil
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"profile '%s' v%d cannot be rolled out with --via-apply: the export carries the values the version stores, "+
			"so it would reset the inputs these deployments were applied with: %s. Roll it out in place instead "+
			"(drop --via-apply): the platform carries each deployment's recorded inputs forward",
		profileName, version, strings.Join(withInputs, "; ")))
}

// requestCarriesSecretPlaceholder reports whether any string in the request,
// or the decoded text of any base64 string in it, carries the placeholder.
//
// It is a backstop for a version whose secret declaration was lost, sized to
// the shapes an export produces: the placeholder as written, as its own
// base64 encoding (a Secret data value), or inside a string that is entirely
// base64 (a manifest or values body). A placeholder encoded as one token inside
// a larger string that is not itself base64 is not found. It is not the
// guarantee: the platform refuses any stack write that would put the
// placeholder over a stored value, whatever the client sent.
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

// encodedSecretPlaceholder is the placeholder base64-encoded, padding
// dropped. It is the same in the standard and the URL-safe alphabets.
var encodedSecretPlaceholder = strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(stackProfileSecretPlaceholder)), "=")

// valueCarriesSecretPlaceholder walks a decoded JSON request. A string carries
// the placeholder when it holds it as written or base64-encoded (a Secret data
// value inside a manifest), or when it decodes - in any base64 alphabet,
// padded or not - to text that does.
func valueCarriesSecretPlaceholder(value any) bool {
	switch typed := value.(type) {
	case string:
		return textCarriesSecretPlaceholder(typed, 2)
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

// textCarriesSecretPlaceholder checks text, then the text it base64-decodes
// to, down to depth encoding levels.
func textCarriesSecretPlaceholder(text string, depth int) bool {
	if strings.Contains(text, stackProfileSecretPlaceholder) || strings.Contains(text, encodedSecretPlaceholder) {
		return true
	}
	if depth == 0 {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, decodeError := encoding.DecodeString(trimmed)
		if decodeError == nil && textCarriesSecretPlaceholder(string(decoded), depth-1) {
			return true
		}
	}
	return false
}
