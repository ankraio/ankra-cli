package cmd

import (
	"encoding/json"
	"testing"

	"ankra/internal/client"
)

// The retention view says how many of the registry's rule slots are used,
// hints at reusing keep values near the limit, and says plainly when the
// limit is exceeded. A platform that reports no slots gets no line.
func TestRegistryRetentionShowsRuleSlots(t *testing.T) {
	cases := []struct {
		name      string
		slots     *client.RegistryRetentionRuleSlots
		expected  []string
		forbidden []string
	}{
		{"absent", nil, nil, []string{"Rule slots"}},
		{"room", &client.RegistryRetentionRuleSlots{Used: 7, Limit: 15},
			[]string{"Rule slots: 7 of 15 used (rules with the same keep values share a slot)."},
			[]string{"Nearly full", "NOT being applied"}},
		{"nearly full", &client.RegistryRetentionRuleSlots{Used: 13, Limit: 15},
			[]string{"Rule slots: 13 of 15 used", "Nearly full: a new rule that reuses the --keep-days or --keep-latest"},
			[]string{"NOT being applied"}},
		{"full", &client.RegistryRetentionRuleSlots{Used: 15, Limit: 15},
			[]string{"Rule slots: 15 of 15 used", "Nearly full"}, []string{"NOT being applied"}},
		{"over", &client.RegistryRetentionRuleSlots{Used: 17, Limit: 15},
			[]string{"Rule slots: 17 of 15 used", "the newest retention is NOT being applied until rules are reduced"},
			[]string{"Nearly full"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &registryStorageMock{retention: registryRetentionFixture()}
			mock.retention.RuleSlots = testCase.slots
			output, runError := runRegistryCommand(t, mock, "", "storage", "retention")
			if runError != nil {
				t.Fatalf("retention: %v", runError)
			}
			mustContain(t, output, testCase.expected...)
			mustNotContain(t, output, testCase.forbidden...)
		})
	}
}

// -o json passes rule_slots through, and leaves it out when the platform
// did not report it.
func TestRegistryRetentionJSONCarriesRuleSlots(t *testing.T) {
	mock := &registryStorageMock{retention: registryRetentionFixture()}
	mock.retention.RuleSlots = &client.RegistryRetentionRuleSlots{Used: 7, Limit: 15}
	stdout, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "retention", "-o", "json")
	var decoded map[string]any
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil {
		t.Fatalf("retention -o json: %v\n%s", runError, stdout)
	}
	slots, ok := decoded["rule_slots"].(map[string]any)
	if !ok || slots["used"] != float64(7) || slots["limit"] != float64(15) {
		t.Fatalf("rule_slots = %v", decoded["rule_slots"])
	}

	absent := &registryStorageMock{retention: registryRetentionFixture()}
	stdout, _, runError = runRegistryCommandSplit(t, absent, "", "storage", "retention", "-o", "json")
	decoded = nil
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil {
		t.Fatalf("retention -o json: %v\n%s", runError, stdout)
	}
	if _, present := decoded["rule_slots"]; present {
		t.Fatalf("an unreported rule_slots must stay absent: %s", stdout)
	}
}
