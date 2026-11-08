package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The bounds `node-group autoscaling set` sends for one the user leaves out
// when the group has none recorded: what the command always sent for an
// omitted bound, so leaving --max out never raises a group's ceiling to the
// platform's 100.
const (
	autoscalingDefaultMinCount = 1
	autoscalingDefaultMaxCount = 5
)

// The platform's autoscaling read answers a group that has no settings
// recorded as disabled with min 1 and max 100 (GetNodeGroupAutoscaling). A
// disabled group recorded with exactly those bounds reads the same and is
// treated the same: they are what the platform saves for a write that names
// no bound, so nobody chose them.
const (
	unrecordedAutoscalingMinCount = 1
	unrecordedAutoscalingMaxCount = 100
)

// autoscalingBounds are the min and max an autoscaling write sends, and one
// line per bound the user left out saying where its value came from.
type autoscalingBounds struct {
	minCount int
	maxCount int
	notes    []string
}

// resolveAutoscalingBounds works out both bounds of an autoscaling write.
// The platform does not keep the recorded bound for one the body leaves out:
// it saves its default (min 1, max 100) in its place (parseAutoscalingFields,
// then upsertNodeGroupSettings). Sending only the flags given would therefore
// raise a group capped at 5 to 100 on a plain --enabled, and reset both
// bounds on --enabled=false. So both bounds are always sent: the one the user
// passed, else the one recorded for the group, else the CLI default (min 1,
// max 5) when the group has none recorded. The record is read only when a
// bound was left out, and a record that cannot be read refuses the write
// rather than guessing a bound that could move the group's node count.
func resolveAutoscalingBounds(cmd *cobra.Command, read nodeGroupAutoscalingGetFunc, clusterID, groupName string) (autoscalingBounds, error) {
	minCount, _ := cmd.Flags().GetInt("min")
	maxCount, _ := cmd.Flags().GetInt("max")
	minGiven, maxGiven := cmd.Flags().Changed("min"), cmd.Flags().Changed("max")
	bounds := autoscalingBounds{minCount: minCount, maxCount: maxCount}
	if minGiven && maxGiven {
		return bounds, nil
	}

	current, readError := read(clusterID, groupName)
	if readError == nil && current == nil {
		readError = errors.New("the platform answered no settings")
	}
	if readError != nil {
		return autoscalingBounds{}, fmt.Errorf(
			"node group %q not changed: a bound left out (%s) keeps the one recorded for the group, and reading the "+
				"recorded bounds failed: %w; pass both --min and --max to set them without reading",
			groupName, omittedBoundFlags(minGiven, maxGiven), readError)
	}

	recorded := current.Enabled || current.MinCount != unrecordedAutoscalingMinCount ||
		current.MaxCount != unrecordedAutoscalingMaxCount
	verb, source := "using", "the CLI default, as the group has no autoscaling bounds recorded"
	if recorded {
		verb, source = "keeping", "recorded for the group"
	}
	if !minGiven {
		bounds.minCount = autoscalingDefaultMinCount
		if recorded {
			bounds.minCount = current.MinCount
		}
		bounds.notes = append(bounds.notes, fmt.Sprintf("--min not given: %s min %d, %s.", verb, bounds.minCount, source))
	}
	if !maxGiven {
		bounds.maxCount = autoscalingDefaultMaxCount
		if recorded {
			bounds.maxCount = current.MaxCount
		}
		bounds.notes = append(bounds.notes, fmt.Sprintf("--max not given: %s max %d, %s.", verb, bounds.maxCount, source))
	}

	if bounds.minCount > bounds.maxCount {
		if minGiven {
			return autoscalingBounds{}, withExitCode(exitUsage, fmt.Errorf(
				"--min %d is above the max of %d the group would keep (%s); pass --max as well",
				bounds.minCount, bounds.maxCount, source))
		}
		return autoscalingBounds{}, withExitCode(exitUsage, fmt.Errorf(
			"--max %d is below the min of %d the group would keep (%s); pass --min as well",
			bounds.maxCount, bounds.minCount, source))
	}
	return bounds, nil
}

// omittedBoundFlags names the bound flags the user left out, for messages.
func omittedBoundFlags(minGiven, maxGiven bool) string {
	var omitted []string
	if !minGiven {
		omitted = append(omitted, "--min")
	}
	if !maxGiven {
		omitted = append(omitted, "--max")
	}
	return strings.Join(omitted, " and ")
}

// request is the write body for these bounds.
func (bounds autoscalingBounds) request(enabled bool) client.NodeGroupAutoscalingRequest {
	return client.NodeGroupAutoscalingRequest{Enabled: enabled, MinCount: bounds.minCount, MaxCount: bounds.maxCount}
}
