package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The bounds `node-group autoscaling set` sends for one the user leaves out
// when the group has none recorded: on an enable, and for the max on a
// disable that sets --min (resolveAutoscalingBounds). They are what the
// command always sent for an omitted bound, so leaving --max out never raises
// a group's ceiling to the platform's 100.
const (
	autoscalingDefaultMinCount = 1
	autoscalingDefaultMaxCount = 5
)

// The platform's autoscaling read answers a group that has no settings
// recorded as disabled with min 1 and max 100 (GetNodeGroupAutoscaling). A
// disabled group recorded with exactly those bounds reads the same, and the
// read carries nothing that tells the two apart (ankra-v3qdto.46), so an
// enable treats them the same: an omitted bound takes the CLI default, and
// the note it prints says so and says to pass the bound to set another
// (--max 100 keeps 100). Those are also the bounds the platform saves for a
// write that names none.
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
// The platform does not keep the current bound for one the body leaves out:
// it saves its default (min 1, max 100) in its place (parseAutoscalingFields,
// then upsertNodeGroupSettings), on a disable as on an enable. Sending only
// the flags given would therefore raise a group capped at 5 to 100 on a plain
// --enabled, and reset both bounds on --enabled=false. So both bounds are
// always sent, the one the user passed or else:
//
//   - on a disable, exactly what the group reads as now, so a disable
//     changes nothing but the switch and invents no bound the next enable
//     would take for the user's. The one exception is a group that reads as
//     recording none (1/100) given --min alone: sent beside the read max
//     100, the --min would make the group read as recorded with max 100,
//     which a later plain --enabled would keep and could grow the group to.
//     So that max takes the CLI default 5 instead;
//   - on an enable, the bound recorded for the group, or the CLI default
//     (min 1, max 5) when the group reads as recording none.
//
// The current bounds are read only when a bound was left out, and a read
// that fails refuses the write rather than guessing a bound that could move
// the group's node count. An enable without --max is refused when the group
// has more nodes than the max it would send, recorded or default: the
// platform would scale it down to that max, the clamp an 8-node group got
// from a plain --enabled (ankra-v3qdto.6). No max the user typed does that
// unasked.
func resolveAutoscalingBounds(cmd *cobra.Command, calls nodeGroupCalls, clusterID, groupName string, enabled bool) (autoscalingBounds, error) {
	minCount, _ := cmd.Flags().GetInt("min")
	maxCount, _ := cmd.Flags().GetInt("max")
	minGiven, maxGiven := cmd.Flags().Changed("min"), cmd.Flags().Changed("max")
	bounds := autoscalingBounds{minCount: minCount, maxCount: maxCount}
	if minGiven && maxGiven {
		return bounds, nil
	}

	current, readError := calls.autoscalingGet(clusterID, groupName)
	if readError == nil && current == nil {
		readError = errors.New("the platform answered no settings")
	}
	if readError != nil {
		return autoscalingBounds{}, fmt.Errorf(
			"node group %q not changed: could not read the group's current autoscaling bounds (if the cluster is stopped, "+
				"pass both --min and --max), which a bound left out (%s) keeps: %w",
			groupName, omittedBoundFlags(minGiven, maxGiven), readError)
	}

	recorded := current.Enabled || current.MinCount != unrecordedAutoscalingMinCount ||
		current.MaxCount != unrecordedAutoscalingMaxCount
	// Which omitted bound takes the CLI default rather than the one read: on
	// a group that reads as recording none, both on an enable, and the max on
	// a disable that sets --min (see above).
	minDefaulted := enabled && !recorded
	maxDefaulted := !recorded && (enabled || minGiven)
	sourceOf := func(defaulted bool) string {
		switch {
		case defaulted:
			return "the CLI default: the group reads as disabled with min 1 and max 100, " +
				"the answer for a group with no bounds recorded and for one that recorded exactly those"
		case recorded:
			return "recorded for the group"
		default:
			return "what the group reads as now"
		}
	}
	note := func(flag, bound string, value int, defaulted bool) string {
		verb := "keeping"
		if defaulted {
			verb = "using"
		}
		line := fmt.Sprintf("%s not given: %s %s %d, %s", flag, verb, bound, value, sourceOf(defaulted))
		if defaulted {
			line += "; pass " + flag + " to set another"
		}
		return line + "."
	}
	if !minGiven {
		bounds.minCount = current.MinCount
		if minDefaulted {
			bounds.minCount = autoscalingDefaultMinCount
		}
		bounds.notes = append(bounds.notes, note("--min", "min", bounds.minCount, minDefaulted))
	}
	if !maxGiven {
		bounds.maxCount = current.MaxCount
		if maxDefaulted {
			bounds.maxCount = autoscalingDefaultMaxCount
		}
		bounds.notes = append(bounds.notes, note("--max", "max", bounds.maxCount, maxDefaulted))
	}

	if bounds.minCount > bounds.maxCount {
		if minGiven {
			return autoscalingBounds{}, withExitCode(exitUsage, fmt.Errorf(
				"--min %d is above the max of %d the group would keep (%s); pass --max as well",
				bounds.minCount, bounds.maxCount, sourceOf(maxDefaulted)))
		}
		return autoscalingBounds{}, withExitCode(exitUsage, fmt.Errorf(
			"--max %d is below the min of %d the group would keep (%s); pass --min as well",
			bounds.maxCount, bounds.minCount, sourceOf(minDefaulted)))
	}
	if enabled && !maxGiven {
		maxSource := fmt.Sprintf("the max of %d recorded for the group", bounds.maxCount)
		if maxDefaulted {
			maxSource = fmt.Sprintf("the CLI default max of %d (the group reads as recording no bounds)", bounds.maxCount)
		}
		if refusal := refuseAnUntypedMaxThatScalesDown(calls, clusterID, groupName, bounds.maxCount, maxSource); refusal != nil {
			return autoscalingBounds{}, refusal
		}
	}
	return bounds, nil
}

// refuseAnUntypedMaxThatScalesDown refuses an enable whose max the user did
// not type when the group has more nodes than that max: the platform scales
// a group down to its max as soon as autoscaling is on, so a bound nobody
// typed would remove nodes. The refusal exits 8, a preflight that found a
// block with nothing changed, and names the --max that keeps the group's
// size. A node count that cannot be read refuses too, keeping the listing's
// exit class.
func refuseAnUntypedMaxThatScalesDown(calls nodeGroupCalls, clusterID, groupName string, maxCount int, maxSource string) error {
	nodeCount, countKnown := 0, false
	var listError error
	if calls.list == nil {
		listError = errors.New("this cluster kind has no node-group listing")
	} else {
		var groups *client.NodeGroupListResult
		groups, listError = calls.list(clusterID)
		if listError == nil && groups != nil {
			for _, group := range groups.NodeGroups {
				if group.Name == groupName {
					nodeCount, countKnown = group.Count, true
					break
				}
			}
		}
	}
	if listError == nil && !countKnown {
		listError = fmt.Errorf("node group %q is not in the cluster's node-group listing", groupName)
	}
	if listError != nil {
		return fmt.Errorf("node group %q not changed: --max was left out, so %s would apply, and whether that scales "+
			"the group down could not be checked: %w; pass --max", groupName, maxSource, listError)
	}
	if nodeCount > maxCount {
		return withExitCode(exitBlocked, fmt.Errorf(
			"node group %q not changed: --max was left out, so %s would apply, and the group has %d nodes: "+
				"enabling would scale it down to %d; pass --max (--max %d keeps its size)",
			groupName, maxSource, nodeCount, maxCount, nodeCount))
	}
	return nil
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
