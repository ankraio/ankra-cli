package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"
)

// namedReadItems answers the objects a read of one named resource returned,
// or the error that says why there are none. Four answers look alike on the
// wire and mean different things: the cluster's apiserver said the object
// does not exist (not_found), the platform served its last record of an
// object the cluster has since deleted (cache_metadata.is_deleted, with the
// old body still in items), the read itself failed (status "error": lost
// RBAC, a downed apiserver), or the object is there. Reading only the item
// list printed a deleted pod's old body as if it were running, and reported
// a refused read as a missing object.
//
// Not found and deleted exit with the not-found code; a failed read is an
// ordinary error carrying the platform's reason.
func namedReadItems(response *client.GetResourcesResponse, kind string, name string, namespace string) ([]interface{}, error) {
	notFound := withExitCode(exitNotFound, fmt.Errorf("%s %q not found%s", kind, name, namespaceSuffix(namespace)))
	if response == nil || len(response.ResourceResponses) == 0 {
		// One item per request item is the wire contract: a reply with none
		// was never an answer about the object, so it is not a not-found.
		return nil, fmt.Errorf("reading %s %q%s failed: the platform answered no item for it",
			kind, name, namespaceSuffix(namespace))
	}
	item := response.ResourceResponses[0]
	if item.NotFound || isAgentNotFoundText(item) {
		return nil, notFound
	}
	if item.CacheMetadata != nil && item.CacheMetadata.IsDeleted {
		deletedAt := ""
		if item.CacheMetadata.DeletedAt != nil && strings.TrimSpace(*item.CacheMetadata.DeletedAt) != "" {
			deletedAt = " at " + strings.TrimSpace(*item.CacheMetadata.DeletedAt)
		}
		return nil, withExitCode(exitNotFound, fmt.Errorf("%s %q%s was deleted from the cluster%s",
			kind, name, namespaceSuffix(namespace), deletedAt))
	}
	if readError := failedReadError(item, fmt.Sprintf("%s %q%s", kind, name, namespaceSuffix(namespace))); readError != nil {
		return nil, readError
	}
	if len(item.Items) == 0 {
		return nil, notFound
	}
	return item.Items, nil
}

// agentNotFoundText is the agent's own answer for an apiserver 404. A
// platform that predates the not_found flag sets only this, so it is read as
// the same absence (an exact match: any other error text is a failed read).
const agentNotFoundText = "Resource not found"

func isAgentNotFoundText(item client.ResourceResponseItem) bool {
	return item.Status == "error" && item.ErrorResponse != nil && strings.TrimSpace(*item.ErrorResponse) == agentNotFoundText
}

// failedReadError answers the error for an item the platform could not read
// (status "error": lost RBAC, an apiserver that did not answer), nil for any
// other item. A listing that failed is not an empty one, so callers report
// this instead of "nothing found".
func failedReadError(item client.ResourceResponseItem, subject string) error {
	if item.Status != "error" {
		return nil
	}
	reason := "the platform gave no reason"
	if item.ErrorResponse != nil && strings.TrimSpace(*item.ErrorResponse) != "" {
		reason = strings.TrimSpace(*item.ErrorResponse)
	}
	return fmt.Errorf("reading %s failed: %s", subject, reason)
}
