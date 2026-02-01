package cmd

import (
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
