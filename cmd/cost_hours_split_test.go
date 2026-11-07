package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// How a day's hours divide (cluster#5909, ankra-bn86k4.4.8.4.2): hours_metered
// keeps counting every hour with a figure, an hour a later metering pass
// filled in included, and hours_measured, hours_estimated and hours_missing
// say how they divide. Behind several clusters each is one cluster's worst.

// setTrendDayFields sets the given fields on trend days of a projection
// document, as a platform with cluster#5909 serves them.
func setTrendDayFields(t *testing.T, document string, fields map[int]map[string]any) string {
	t.Helper()
	var projection map[string]any
	if decodeError := json.Unmarshal([]byte(document), &projection); decodeError != nil {
		t.Fatalf("decode: %v", decodeError)
	}
	trend := projection["trend_30d"].([]any)
	for index, set := range fields {
		for key, value := range set {
			trend[index].(map[string]any)[key] = value
		}
	}
	encoded, encodeError := json.Marshal(projection)
	if encodeError != nil {
		t.Fatalf("encode: %v", encodeError)
	}
	return string(encoded)
}

// TestCostObjectTrendSaysHowADaysHoursDivide pins that a day with hours a
// later pass filled in says how many were measured and how many estimated,
// never calling an estimated hour measured: today on its own line, a floor
// day beside what it is missing, and each whole day by name, keeping its
// height. A day with nothing estimated is not named, and -o json keeps the
// fields as served.
func TestCostObjectTrendSaysHowADaysHoursDivide(t *testing.T) {
	pinCostObjectToday(t, time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	days := todayStillMetering(8100, 6748, 17)
	days[22] = objectCostMeteredDay{cents: namespaceCents(7800), hours: 22, clustersMetered: 1}
	document := setTrendDayFields(t, meteredClusterProjection(days, 1), map[int]map[string]any{
		22: {"incomplete": true, "hours_measured": 20, "hours_estimated": 2, "hours_missing": 2},
		25: {"hours_measured": 22, "hours_estimated": 2, "hours_missing": 0},
		28: {"hours_measured": 24, "hours_estimated": 0, "hours_missing": 0},
		29: {"hours_measured": 15, "hours_estimated": 2, "hours_missing": 0},
	})
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	for _, want := range []string{
		// The whole day with estimated hours keeps its full height.
		"  " + strings.Repeat("█", 22) + "≥" + strings.Repeat("█", 6) + "…\n",
		"  Today so far (2026-09-25, 17 of 24 h: 15 measured, 2 estimated): €67.48, still metering, so not comparable to a whole day.\n",
		"  2026-09-18 is missing 2 metered hours, so it is at least €78.00. Its metered hours: 20 measured, 2 estimated.\n",
		"  2026-09-21 was metered 24 of 24 h: 22 measured, 2 estimated.\n",
		costObjectEstimatedNote + "\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "2026-09-24 was metered") || strings.Contains(output, "24 measured") {
		t.Fatalf("a day with nothing estimated needs no words:\n%s", output)
	}
	if strings.Count(output, costObjectEstimatedNote) != 1 {
		t.Fatalf("what an estimated hour is is said once:\n%s", output)
	}
	resetTreeFlags(t, costObjectClusterCmd)
	jsonOutput, jsonError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID, "-o", "json")
	if jsonError != nil {
		t.Fatalf("run -o json: %v", jsonError)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(document), &want); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	if err := json.Unmarshal([]byte(jsonOutput), &got); err != nil {
		t.Fatalf("decoding %s: %v", jsonOutput, err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("-o json is not the projection as served:\nwant %v\ngot  %v", want, got)
	}
}

// TestCostObjectTrendSaysEachCountAsOneClustersWorst pins the fold behind
// several clusters: the counts are said per cluster as bounds and never
// added up, and a floor day's missing hours are the platform's
// hours_missing, never 24 minus the fewest hours metered.
func TestCostObjectTrendSaysEachCountAsOneClustersWorst(t *testing.T) {
	pinCostObjectToday(t, time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	days := todayStillMetering(8100, 6748, 17)
	for index := range days {
		days[index].clustersMetered = 2
	}
	days[22] = objectCostMeteredDay{cents: namespaceCents(7800), hours: 20, clustersMetered: 2}
	document := setTrendDayFields(t, meteredClusterProjection(days, 2), map[int]map[string]any{
		22: {"incomplete": true, "hours_measured": 20, "hours_estimated": 0, "hours_missing": 1},
		25: {"hours_measured": 20, "hours_estimated": 4, "hours_missing": 0},
	})
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	for _, want := range []string{
		"  2026-09-18 is missing 1 metered hour, so it is at least €78.00.\n",
		"  2026-09-21 was metered 24 of 24 h: per cluster at least 20 measured, at most 4 estimated.\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "missing 4 metered hours") {
		t.Fatalf("24 minus the fewest hours metered is not the hours missing behind several clusters:\n%s", output)
	}
}

// TestCostObjectTrendSaysNothingOfASplitThePlatformDidNotReport pins absent
// as unknown: a platform or a row that predates cluster#5909 sends no split,
// and the trend says what it said before, never that every hour was
// measured. Today's missing hours are named once the platform counts them.
func TestCostObjectTrendSaysNothingOfASplitThePlatformDidNotReport(t *testing.T) {
	pinCostObjectToday(t, time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	days := todayStillMetering(8100, 3000, 9)
	days[22] = objectCostMeteredDay{cents: namespaceCents(7800), hours: 22, clustersMetered: 1}
	older := markIncomplete(t, meteredClusterProjection(days, 1), 22, 29)
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, older)},
		"cost", "object", "cluster", costObjectClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	for _, want := range []string{
		"  2026-09-18 is missing 2 metered hours, so it is at least €78.00.\n",
		"  Today so far (2026-09-25, 9 of 24 h): at least €30.00, an earlier hour of today was not metered, and it is still metering, so not comparable to a whole day.\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "measured") || strings.Contains(output, "estimated") {
		t.Fatalf("an unreported split says nothing about measured or estimated hours:\n%s", output)
	}
	counted := setTrendDayFields(t, older, map[int]map[string]any{
		10: {"hours_measured": 24, "hours_estimated": 0, "hours_missing": 0},
		29: {"hours_measured": 9, "hours_estimated": 0, "hours_missing": 2},
	})
	resetTreeFlags(t, costObjectClusterCmd)
	output, runError = runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, counted)},
		"cost", "object", "cluster", costObjectClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	want := "  Today so far (2026-09-25, 9 of 24 h): at least €30.00, 2 earlier hours of today were not metered, and it is still metering, so not comparable to a whole day.\n"
	if !strings.Contains(output, want) || strings.Contains(output, "estimated") {
		t.Fatalf("output lacks %q or names an estimate nobody made:\n%s", want, output)
	}
}

// TestCostMonthHoursNamesMeasuredAndEstimatedHours pins the month line once
// the platform counts the measured hours, and that without the count only
// the estimated hours are named, as before.
func TestCostMonthHoursNamesMeasuredAndEstimatedHours(t *testing.T) {
	measured := func(count int) *int { return &count }
	for name, scenario := range map[string]struct {
		hours *client.MonthHours
		want  string
	}{
		"short, measured counted": {
			hours: &client.MonthHours{HoursElapsed: 720, HoursMetered: 718, HoursMeasured: measured(715), HoursEstimated: 3,
				IsShort: true, ClusterCount: 1, ShortClusterCount: 1},
			want: "Month so far covers 718 of 720 hours; 2 hours were not metered, so the figure is a floor. " +
				"Of the covered hours, 715 were measured and 3 estimated: no metering ran for the estimated hours, so the next reading was copied.",
		},
		"whole, one estimated": {
			hours: &client.MonthHours{HoursElapsed: 720, HoursMetered: 720, HoursMeasured: measured(719), HoursEstimated: 1, ClusterCount: 1},
			want: "Month so far covers all 720 hours. " +
				"Of the covered hours, 719 were measured and 1 estimated: no metering ran for the estimated hour, so the next reading was copied.",
		},
		"measured not counted": {
			hours: shortClusterMonth(),
			want: "Month so far covers 718 of 720 hours; 2 hours were not metered, so the figure is a floor. " +
				"3 of the covered hours are estimates: no metering ran then, so the next reading was copied.",
		},
		"every covered hour measured": {
			hours: &client.MonthHours{HoursElapsed: 720, HoursMetered: 718, HoursMeasured: measured(718), IsShort: true,
				ClusterCount: 1, ShortClusterCount: 1},
			want: "Month so far covers 718 of 720 hours; 2 hours were not metered, so the figure is a floor.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if line, _ := costMonthHours(scenario.hours); line != scenario.want {
				t.Fatalf("line = %q, want %q", line, scenario.want)
			}
		})
	}
	if line, _ := costMonthHours(&client.MonthHours{HoursElapsed: 720, HoursMetered: 720, HoursMeasured: measured(720), ClusterCount: 1}); line != "" {
		t.Fatalf("a whole month measured needs no line, got %q", line)
	}
}

// TestCostNamespacesNamesEstimatedHours pins the namespace history naming
// the buckets with hours a later pass filled in, by the day and by the hour,
// and saying nothing when none was or the platform did not say.
func TestCostNamespacesNamesEstimatedHours(t *testing.T) {
	estimated := func(count int) *int { return &count }
	history := threeDayHistory()
	history.Buckets[1].EstimatedHours = estimated(3)
	history.Buckets[2].EstimatedHours = estimated(2)
	output, runError := runCostNamespacesCommand(t, &costNamespacesMock{history: history},
		"cost", "namespaces", costNamespacesClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	want := "2 days include estimated hours (5 in all): no metering pass ran for them, so a later pass filled them in and their allocation is an estimate, not a measurement.\n"
	if !strings.Contains(output, want) {
		t.Fatalf("output lacks %q:\n%s", want, output)
	}
	if line := costNamespacesEstimated(history.Buckets[2:], "hour"); !strings.HasPrefix(line, "2 hours were estimated: ") {
		t.Fatalf("by the hour = %q", line)
	}
	if line := costNamespacesEstimated(history.Buckets[1:2], "day"); !strings.HasPrefix(line, "1 day includes estimated hours (3 in all): ") {
		t.Fatalf("one day = %q", line)
	}
	if line := costNamespacesEstimated(threeDayHistory().Buckets, "day"); line != "" {
		t.Fatalf("buckets that say nothing of estimates must say nothing, got %q", line)
	}
	history.Buckets[1].EstimatedHours = estimated(0)
	history.Buckets[2].EstimatedHours = nil
	if line := costNamespacesEstimated(history.Buckets, "day"); line != "" {
		t.Fatalf("no estimated hour needs no line, got %q", line)
	}
}
