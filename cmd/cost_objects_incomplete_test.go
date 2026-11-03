package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// markIncomplete sets incomplete on the given trend days of a projection
// document, as a platform with cluster ankra-bn86k4.4.8 serves them.
func markIncomplete(t *testing.T, document string, indexes ...int) string {
	t.Helper()
	var projection map[string]any
	if decodeError := json.Unmarshal([]byte(document), &projection); decodeError != nil {
		t.Fatalf("decode: %v", decodeError)
	}
	trend := projection["trend_30d"].([]any)
	for _, index := range indexes {
		trend[index].(map[string]any)["incomplete"] = true
	}
	encoded, encodeError := json.Marshal(projection)
	if encodeError != nil {
		t.Fatalf("encode: %v", encodeError)
	}
	return string(encoded)
}

// TestCostObjectTrendMarksIncompleteDaysAsFloors pins ankra-bn86k4.4.8.3: a
// day the platform marks incomplete was metered for fewer of its hours than
// it had, so its figure is a floor. It is marked apart and never drawn at its
// height, where 12.00 against whole days of 81.00 would read as a drop; it
// never sets the peak; each one is named with the hours it is missing and
// its floor; the latest whole day and today say "at least"; and -o json
// keeps the field as served.
func TestCostObjectTrendMarksIncompleteDaysAsFloors(t *testing.T) {
	pinCostObjectToday(t, time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	days := todayStillMetering(8100, 3000, 9)
	days[22] = objectCostMeteredDay{cents: namespaceCents(7800), hours: 22, clustersMetered: 1}
	days[25] = objectCostMeteredDay{cents: namespaceCents(1200), hours: 3, clustersMetered: 1}
	days[28] = objectCostMeteredDay{cents: namespaceCents(9900), hours: 23, clustersMetered: 1}
	document := markIncomplete(t, meteredClusterProjection(days, 1), 22, 25, 28, 29)
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	for _, want := range []string{
		"  " + strings.Repeat("█", 22) + "≥██≥██≥…\n",
		"… today, still metering   ≥ short of metered hours, a floor\n",
		// 9900 on 2026-09-24 is higher than any whole day, but it is a floor, so the peak stays a whole day.
		"30 of 30 days metered · peak €81.00 on 2026-08-27 · latest whole day at least €99.00 on 2026-09-24 (metered 23 of 24 h)\n",
		"  Today so far (2026-09-25, 9 of 24 h): at least €30.00, an earlier hour of today was not metered, and it is still metering, so not comparable to a whole day.\n",
		"  2026-09-18 is missing 2 metered hours, so it is at least €78.00.\n",
		"  2026-09-21 is missing 21 metered hours, so it is at least €12.00.\n",
		"  2026-09-24 is missing 1 metered hour, so it is at least €99.00.\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}

	resetTreeFlags(t, costObjectClusterCmd)
	jsonOutput, jsonError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID, "-o", "json")
	var want, got map[string]any
	if jsonError != nil || json.Unmarshal([]byte(document), &want) != nil || json.Unmarshal([]byte(jsonOutput), &got) != nil ||
		!reflect.DeepEqual(want, got) {
		t.Fatalf("-o json must keep incomplete as served (%v):\nwant %s\ngot  %s", jsonError, document, jsonOutput)
	}
	if !strings.Contains(jsonOutput, `"incomplete": true`) {
		t.Fatalf("-o json lacks incomplete:\n%s", jsonOutput)
	}
}

// TestCostObjectTrendCountsIncompleteDaysPastTheListed pins the cap: the
// first costObjectFloorDaysListed days short of metered hours are named, the
// rest counted, and a day whose hours were not reported names no count.
func TestCostObjectTrendCountsIncompleteDaysPastTheListed(t *testing.T) {
	pinCostObjectToday(t, time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	days := todayStillMetering(8100, 3000, 9)
	short := []int{1, 3, 5, 7, 9, 11, 13}
	for _, index := range short {
		days[index] = objectCostMeteredDay{cents: namespaceCents(4000), hours: 12, clustersMetered: 1}
	}
	document := markIncomplete(t, meteredClusterProjection(days, 1), short...)
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	if strings.Count(output, "is missing 12 metered hours, so it is at least €40.00.\n") != costObjectFloorDaysListed ||
		!strings.Contains(output, "  2 more days are short of metered hours, so they are floors too.\n") {
		t.Fatalf("output:\n%s", output)
	}

	unreported := strings.Replace(document, `"hours_metered":12,`, ``, 1)
	resetTreeFlags(t, costObjectClusterCmd)
	unreportedOutput, unreportedError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, unreported)},
		"cost", "object", "cluster", costObjectClusterID)
	if unreportedError != nil || !strings.Contains(unreportedOutput, "  2026-08-28 is missing metered hours, so it is at least €40.00.\n") {
		t.Fatalf("a day whose hours were not reported = %v:\n%s", unreportedError, unreportedOutput)
	}
}
