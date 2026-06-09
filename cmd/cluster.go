package cmd

import (
	"regexp"
	"time"

	"github.com/dustin/go-humanize"
)

// formatTimeAgo converts an RFC3339 timestamp string to a human-readable relative time
func formatTimeAgo(tStr string) string {
	t, err := time.Parse(time.RFC3339, tStr)
	if err != nil {
		return tStr
	}
	return humanize.Time(t)
}

var clusterIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isLikelyClusterID reports whether value has the shape of a cluster UUID. It
// is used to decide whether an unresolved --cluster value can be forwarded to
// the API as an ID, or should be rejected as an unknown name rather than
// producing an opaque server-side UUID-parsing error.
func isLikelyClusterID(value string) bool {
	return clusterIDPattern.MatchString(value)
}
