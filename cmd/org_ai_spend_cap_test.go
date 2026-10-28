package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type aiSpendCapMock struct {
	baseMock
	spendCap client.AISpendCap
	update   *client.AISpendCapUpdate
}

func (mock *aiSpendCapMock) GetAISpendCap() (*client.AISpendCap, error) {
	spendCap := mock.spendCap
	return &spendCap, nil
}

func (mock *aiSpendCapMock) UpdateAISpendCap(update client.AISpendCapUpdate) (*client.AISpendCap, error) {
	mock.update = &update
	spendCap := mock.spendCap
	if update.DailyTokenSoftCapSet {
		spendCap.DailyTokenSoftCap = update.DailyTokenSoftCap
	}
	if update.DailyTokenHardCapSet {
		spendCap.DailyTokenHardCap = update.DailyTokenHardCap
	}
	if update.DailyChatUSDCapSet {
		spendCap.DailyChatUSDCap = update.DailyChatUSDCap
	}
	return &spendCap, nil
}

func runOrgAISpendCapCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	// Flags keep their Changed markers between Execute calls on the shared
	// tree, so reset before every run as well as after the test.
	resetTreeFlags(t, orgAISpendCapShowCmd, orgAISpendCapSetCmd)
	t.Cleanup(func() { resetTreeFlags(t, orgAISpendCapShowCmd, orgAISpendCapSetCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func sampleAISpendCap() client.AISpendCap {
	usd, planDefault, soft, today := 25.0, 10.0, int64(2000000), int64(345678)
	return client.AISpendCap{DailyChatUSDCap: &usd, PlanDefaultUSD: &planDefault, EffectiveUSD: &usd, SpentTodayUSD: 3.5,
		DailyTokenSoftCap: &soft, TokensToday: &today, TokenBudgetState: "under", MonthlyResetsAt: "2026-10-01T00:00:00Z"}
}

func TestOrgAISpendCapShowRendersCapsAndTodaysUsage(t *testing.T) {
	mock := &aiSpendCapMock{spendCap: sampleAISpendCap()}
	stdout, runError := runOrgAISpendCapCommand(t, mock, "org", "ai-spend-cap", "show")
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	for _, fragment := range []string{"25.00", "3.50 spent", "2000000", "345678 tokens", "none", "budget under"} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected the table to contain %q, got:\n%s", fragment, stdout)
		}
	}
	unreadable := sampleAISpendCap()
	unreadable.TokensToday, unreadable.TokenBudgetState = nil, "unknown"
	stdout, _ = runOrgAISpendCapCommand(t, &aiSpendCapMock{spendCap: unreadable}, "org", "ai-spend-cap", "show")
	if !strings.Contains(stdout, "usage unreadable") || !strings.Contains(stdout, "budget unknown") {
		t.Fatalf("an unreadable usage is said, not shown as zero:\n%s", stdout)
	}
}

func TestOrgAISpendCapSetSendsOnlyTheFlagsPassed(t *testing.T) {
	mock := &aiSpendCapMock{spendCap: sampleAISpendCap()}
	stdout, runError := runOrgAISpendCapCommand(t, mock, "org", "ai-spend-cap", "set", "--token-hard-cap", "5m")
	if runError != nil || mock.update == nil {
		t.Fatalf("set failed: %v", runError)
	}
	if mock.update.DailyChatUSDCapSet || mock.update.DailyTokenSoftCapSet || !mock.update.DailyTokenHardCapSet ||
		mock.update.DailyTokenHardCap == nil || *mock.update.DailyTokenHardCap != 5000000 {
		t.Fatalf("update = %+v", *mock.update)
	}
	if !strings.Contains(stdout, "AI caps updated.") || !strings.Contains(stdout, "5000000") {
		t.Fatalf("stdout = %s", stdout)
	}
	encoded, _ := json.Marshal(*mock.update)
	if string(encoded) != `{"daily_token_hard_cap":5000000}` {
		t.Fatalf("body = %s", encoded)
	}

	mock = &aiSpendCapMock{spendCap: sampleAISpendCap()}
	if _, runError = runOrgAISpendCapCommand(t, mock, "org", "ai-spend-cap", "set", "--daily-usd", "none", "--token-soft-cap", "250k"); runError != nil {
		t.Fatalf("clear failed: %v", runError)
	}
	encoded, _ = json.Marshal(*mock.update)
	if string(encoded) != `{"daily_chat_usd_cap":null,"daily_token_soft_cap":250000}` {
		t.Fatalf("clear body = %s", encoded)
	}
}

func TestOrgAISpendCapSetRefusesNothingAndBadNumbers(t *testing.T) {
	mock := &aiSpendCapMock{spendCap: sampleAISpendCap()}
	if _, runError := runOrgAISpendCapCommand(t, mock, "org", "ai-spend-cap", "set"); runError == nil || !strings.Contains(runError.Error(), "nothing to set") {
		t.Fatalf("no flags: %v", runError)
	}
	if _, runError := runOrgAISpendCapCommand(t, mock, "org", "ai-spend-cap", "set", "--token-soft-cap", "lots"); runError == nil || !strings.Contains(runError.Error(), "whole number") {
		t.Fatalf("bad tokens: %v", runError)
	}
	if mock.update != nil {
		t.Fatal("nothing was sent")
	}
}
