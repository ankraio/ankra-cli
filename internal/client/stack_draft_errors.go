package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// stackDraftErrorsFromBody turns a stack-draft error body into per-resource
// errors, whichever of the platform's shapes it uses:
//
//   - {"detail": [{"name", "kind", "errors": [{"key", "message"}]}]}: the
//     resource validation result;
//   - {"detail": [{"loc": [...], "msg", "type"}]}: FastAPI request
//     validation, where loc is resolved against the submitted stack so the
//     error names the addon or manifest rather than an index;
//   - {"detail": "..."}: a plain refusal;
//   - {"errors": [...]}: any of the above at the top level.
//
// It returns nil when the body carries none of them.
func stackDraftErrorsFromBody(body []byte, stack Stack) []ImportResponseResourceError {
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
		Errors json.RawMessage `json:"errors"`
	}
	if decodeError := json.Unmarshal(body, &envelope); decodeError != nil {
		return nil
	}
	resourceErrors := resourceErrorsFromDetail(envelope.Detail, stack)
	return append(resourceErrors, resourceErrorsFromDetail(envelope.Errors, stack)...)
}

// stackDraftEmbeddedErrors reads the per-resource errors a successful (2xx)
// draft response can still carry in its top-level "errors" member. The
// "detail" member is deliberately not read here: on a success it is not a
// refusal.
func stackDraftEmbeddedErrors(body []byte, stack Stack) []ImportResponseResourceError {
	var envelope struct {
		Errors json.RawMessage `json:"errors"`
	}
	if decodeError := json.Unmarshal(body, &envelope); decodeError != nil {
		return nil
	}
	return resourceErrorsFromDetail(envelope.Errors, stack)
}

type stackDraftDetailItem struct {
	Name    string                    `json:"name"`
	Kind    string                    `json:"kind"`
	Errors  []ImportResponseErrorItem `json:"errors"`
	Loc     []any                     `json:"loc"`
	Msg     string                    `json:"msg"`
	Message string                    `json:"message"`
}

func resourceErrorsFromDetail(detail json.RawMessage, stack Stack) []ImportResponseResourceError {
	trimmed := bytes.TrimSpace(detail)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var detailMessage string
	if json.Unmarshal(trimmed, &detailMessage) == nil {
		if strings.TrimSpace(detailMessage) == "" {
			return nil
		}
		return []ImportResponseResourceError{stackLevelError(stack, "", detailMessage)}
	}

	var items []stackDraftDetailItem
	if json.Unmarshal(trimmed, &items) != nil {
		var single stackDraftDetailItem
		if json.Unmarshal(trimmed, &single) != nil {
			return nil
		}
		items = []stackDraftDetailItem{single}
	}

	resourceErrors := make([]ImportResponseResourceError, 0, len(items))
	for _, item := range items {
		switch {
		case item.Name != "" || item.Kind != "" || len(item.Errors) > 0:
			resourceErrors = append(resourceErrors, normalizeResourceError(ImportResponseResourceError{
				Name:   item.Name,
				Kind:   item.Kind,
				Errors: item.Errors,
			}, stack))
		case len(item.Loc) > 0 || item.Msg != "":
			resourceErrors = append(resourceErrors, resourceErrorFromLocation(item.Loc, item.Msg, stack))
		case item.Message != "":
			resourceErrors = append(resourceErrors, stackLevelError(stack, "", item.Message))
		}
	}
	return resourceErrors
}

// normalizeResourceError keeps an error that names no individual problem
// from rendering as a bare resource name.
func normalizeResourceError(resourceError ImportResponseResourceError, stack Stack) ImportResponseResourceError {
	if resourceError.Kind == "" && resourceError.Name == "" {
		resourceError.Kind = "stack"
		resourceError.Name = stack.Name
	}
	if resourceError.Kind == "" {
		resourceError.Kind = "resource"
	}
	if len(resourceError.Errors) == 0 {
		resourceError.Errors = []ImportResponseErrorItem{{Message: "rejected by the platform without a reason"}}
	}
	return resourceError
}

func stackLevelError(stack Stack, key string, message string) ImportResponseResourceError {
	return ImportResponseResourceError{
		Kind:   "stack",
		Name:   stack.Name,
		Errors: []ImportResponseErrorItem{{Key: key, Message: message}},
	}
}

// resourceErrorFromLocation resolves a FastAPI loc such as
// ["body", "spec", "stacks", 0, "addons", 1, "registry_name"] to the addon
// or manifest it points at in the submitted stack, keeping the remaining
// members as the field path.
func resourceErrorFromLocation(location []any, message string, stack Stack) ImportResponseResourceError {
	if message == "" {
		message = "invalid value"
	}
	members := make([]string, 0, len(location))
	for _, member := range location {
		members = append(members, locationMember(member))
	}
	if len(members) > 0 && members[0] == "body" {
		members = members[1:]
	}
	if len(members) > 0 && members[0] == "spec" {
		members = members[1:]
	}
	if len(members) >= 2 && members[0] == "stacks" {
		members = members[2:]
	}

	kind, name := "stack", stack.Name
	if len(members) >= 2 {
		if index, convertError := strconv.Atoi(members[1]); convertError == nil {
			switch members[0] {
			case "addons":
				kind, name = "addon", indexedName(addonNames(stack), index)
				members = members[2:]
			case "manifests":
				kind, name = "manifest", indexedName(manifestNames(stack), index)
				members = members[2:]
			}
		}
	}
	return ImportResponseResourceError{
		Kind:   kind,
		Name:   name,
		Errors: []ImportResponseErrorItem{{Key: strings.Join(members, "."), Message: message}},
	}
}

func locationMember(member any) string {
	switch typed := member.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func indexedName(names []string, index int) string {
	if index >= 0 && index < len(names) && names[index] != "" {
		return names[index]
	}
	return "#" + strconv.Itoa(index)
}

func addonNames(stack Stack) []string {
	names := make([]string, 0, len(stack.Addons))
	for _, addon := range stack.Addons {
		names = append(names, addon.Name)
	}
	return names
}

func manifestNames(stack Stack) []string {
	names := make([]string, 0, len(stack.Manifests))
	for _, manifest := range stack.Manifests {
		names = append(names, manifest.Name)
	}
	return names
}
