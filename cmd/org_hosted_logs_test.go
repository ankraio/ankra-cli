package cmd

// `ankra org hosted-logs show|set` against a scripted platform: the real
// client talks to an httptest server that answers GET and PUT
// /api/v1/org/billing/hosted-logs, so the method, path and body each verb
// sends are pinned on the request itself, and what is printed is what the
// platform's answer makes of it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"
)

const hostedLogsBillingTestPath = "/api/v1/org/billing/hosted-logs"

// hostedLogsBillingTestBlocker is one of the platform's 402 sentences.
const hostedLogsBillingTestBlocker = "Add a payment method on the Billing page before turning on paid hosted logs."

type hostedLogsBillingRequest struct {
	method        string
	authorization string
	body          map[string]any
}

// hostedLogsBillingServer serves one organisation's paid hosted logs setting
// and records every request to it.
type hostedLogsBillingServer struct {
	mutex    sync.Mutex
	view     client.HostedLogsBilling
	requests []hostedLogsBillingRequest
	// readStatus/readBody, when set, answer the GET; writeStatus/writeBody
	// the PUT.
	readStatus  int
	readBody    string
	writeStatus int
	writeBody   string
	// ignoreWrites answers a PUT with the setting it already had.
	ignoreWrites bool
}

// hostedLogsNovember2026 is an organisation in the pilot: November 2026, not
// priced, 1.2 GB sent, paid logs never set.
func hostedLogsNovember2026() client.HostedLogsBilling {
	return client.HostedLogsBilling{
		PricedFrom: "2027-01-01", FreeBytesPerMonth: 5_000_000_000, CentsPerGB: 25, BytesPerGB: 1_000_000_000,
		Currency: "eur", PaidReadiness: client.HostedLogsPaidReady,
		Month: client.HostedLogsMonth{MonthStart: "2026-11-01", Bytes: 1_200_000_000, FreeBytes: 1_200_000_000,
			State: client.HostedLogsStateShipping},
	}
}

// hostedLogsJanuary2027PausedFree is a priced month paused at the free 5 GB,
// paid logs off and not possible until a payment method is added.
func hostedLogsJanuary2027PausedFree() client.HostedLogsBilling {
	view := hostedLogsNovember2026()
	blocker := hostedLogsBillingTestBlocker
	reason := client.HostedLogsPausedFreeAllowanceUsed
	view.PaidReadiness, view.PaidBlocker = client.HostedLogsPaidBlocked, &blocker
	view.Month = client.HostedLogsMonth{MonthStart: "2027-01-01", Priced: true, Bytes: 5_200_000_000, FreeBytes: 5_000_000_000,
		BillableBytes: 200_000_000, State: client.HostedLogsStatePausedFree, PausedReason: &reason}
	return view
}

// hostedLogsJanuary2027AtTheLimit is a priced month with paid logs on at
// EUR 20.00, which the month reached.
func hostedLogsJanuary2027AtTheLimit() client.HostedLogsBilling {
	view := hostedLogsNovember2026()
	limit := int64(2000)
	updatedAt := "2027-01-02T09:00:00Z"
	reason := client.HostedLogsPausedMonthlyLimitReached
	view.PaidEnabled, view.MonthlyLimitCents, view.UpdatedAt = true, &limit, &updatedAt
	view.Month = client.HostedLogsMonth{MonthStart: "2027-01-01", Priced: true, Bytes: 85_000_000_000, FreeBytes: 5_000_000_000,
		BillableBytes: 80_000_000_000, AmountCents: 2000, State: client.HostedLogsStatePausedLimit, PausedReason: &reason}
	return view
}

func newHostedLogsBillingServer(t *testing.T, view client.HostedLogsBilling) *hostedLogsBillingServer {
	t.Helper()
	recorder := &hostedLogsBillingServer{view: view}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != hostedLogsBillingTestPath {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"unscripted route in test"}`))
			return
		}
		recorded := hostedLogsBillingRequest{method: request.Method, authorization: request.Header.Get("Authorization")}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		switch request.Method {
		case http.MethodGet:
			if recorder.readStatus != 0 {
				writer.WriteHeader(recorder.readStatus)
				_, _ = writer.Write([]byte(recorder.readBody))
				return
			}
		case http.MethodPut:
			if recorder.writeStatus != 0 {
				writer.WriteHeader(recorder.writeStatus)
				_, _ = writer.Write([]byte(recorder.writeBody))
				return
			}
			paid, isBool := recorded.body["paid_enabled"].(bool)
			if !isBool {
				writer.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = writer.Write([]byte(`{"detail":"paid_enabled is required"}`))
				return
			}
			if !recorder.ignoreWrites {
				recorder.view.PaidEnabled = paid
				if raw, present := recorded.body["monthly_limit_cents"]; present {
					limit := int64(raw.(float64))
					recorder.view.MonthlyLimitCents = &limit
				}
				updatedAt := "2026-11-24T12:00:00Z"
				recorder.view.UpdatedAt = &updatedAt
			}
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(writer).Encode(recorder.view)
	}))
	t.Cleanup(server.Close)
	useTestClient(t, server.URL)
	// Every test answers on a pipe, never a terminal, unless it opts in.
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return false }
	t.Cleanup(func() { promptIsInteractive = previous })
	return recorder
}

func (recorder *hostedLogsBillingServer) seen() []hostedLogsBillingRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]hostedLogsBillingRequest{}, recorder.requests...)
}

func (recorder *hostedLogsBillingServer) writes() []hostedLogsBillingRequest {
	var puts []hostedLogsBillingRequest
	for _, request := range recorder.seen() {
		if request.method == http.MethodPut {
			puts = append(puts, request)
		}
	}
	return puts
}

// runOrgHostedLogs runs `ankra org hosted-logs <arguments>` on a fresh
// command tree with input on stdin, and returns stdout and stderr apart so
// structured output can be checked for being clean.
func runOrgHostedLogs(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	command := newOrgHostedLogsCommand()
	// The root command silences these for every command; this tree has no
	// root above it.
	command.SilenceUsage, command.SilenceErrors = true, true
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetIn(strings.NewReader(input))
	command.SetArgs(arguments)
	runError := command.Execute()
	return stdout.String(), stderr.String(), runError
}

// assertBodyIs checks a PUT body is exactly want.
func assertBodyIs(t *testing.T, request hostedLogsBillingRequest, want map[string]any) {
	t.Helper()
	if len(request.body) != len(want) {
		t.Fatalf("body = %v, want exactly %v", request.body, want)
	}
	for key, value := range want {
		if request.body[key] != value {
			t.Fatalf("body[%q] = %v, want %v (body %v)", key, request.body[key], value, request.body)
		}
	}
}

// Before the priced-from month the month is included: show says so, names
// the date pricing starts, and never reads as paused or charged.
func TestOrgHostedLogsShowBeforePricingSaysNothingIsCharged(t *testing.T) {
	recorder := newHostedLogsBillingServer(t, hostedLogsNovember2026())

	stdout, stderr, runError := runOrgHostedLogs(t, "", "show")
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet || requests[0].authorization != "Bearer test-token" {
		t.Fatalf("expected one bearer GET, got %+v", requests)
	}
	assertContainsAll(t, stdout, []string{
		"Hosted logs for November 2026 (UTC):",
		"Log text:       1.2 GB of the free 5 GB (24%)",
		"Sending:        not paused",
		"Amount so far:  nothing: hosted logs are included until 31 December 2026",
		"Paid logs:      off",
		"Last changed:   never",
		"Hosted logs are included until 31 December 2026, so nothing is charged until 1 January 2027.",
		"From 1 January 2027: 5 GB of log text a month free per organisation, then EUR 0.25 per GB, kept 7 days.",
		"At the free 5 GB, sending pauses for the rest of the month unless an owner turns on paid logs with a monthly limit.",
		"Owners are told at 80% and 100% of the free 5 GB, and of the monthly limit while paid logs are on.",
		"Turn on paid logs with 'ankra org hosted-logs set --paid --limit <euros>'.",
	})
	if strings.Contains(stdout, "paused until") || strings.Contains(stdout, "EUR 0.00") {
		t.Fatalf("an unpriced month read as paused or charged:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("nothing belongs on stderr for a read, got %q", stderr)
	}
}

// A priced month paused at the free amount says why in plain words, what
// ends the pause, and why paid logs cannot be turned on yet.
func TestOrgHostedLogsShowPausedAtTheFreeAmount(t *testing.T) {
	newHostedLogsBillingServer(t, hostedLogsJanuary2027PausedFree())

	stdout, _, runError := runOrgHostedLogs(t, "", "show")
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{
		"Hosted logs for January 2027 (UTC):",
		"Log text:       5.2 GB: the free 5 GB and 200.0 MB above it",
		"Sending:        paused until 1 February 2027: the free 5 GB of this month is used and paid logs are off",
		"Amount so far:  nothing: paid logs are off, so nothing is charged",
		"Paid logs:      off: at the free 5 GB a month, sending pauses until the month ends",
		"Monthly limit:  none set",
		"Turning on:     not possible now: " + hostedLogsBillingTestBlocker,
		"Price: 5 GB of log text a month free per organisation, then EUR 0.25 per GB, kept 7 days.",
		"To keep sending this month, an owner can turn on paid logs: 'ankra org hosted-logs set --paid --limit <euros>'.",
	})
	if strings.Contains(stdout, "included until") {
		t.Fatalf("a priced month still read as included:\n%s", stdout)
	}
}

// With paid logs on, the month reaching its limit is a pause too, and the
// amount is the limit.
func TestOrgHostedLogsShowPaidLogsAtTheLimit(t *testing.T) {
	newHostedLogsBillingServer(t, hostedLogsJanuary2027AtTheLimit())

	stdout, _, runError := runOrgHostedLogs(t, "", "show")
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{
		"Log text:       85 GB: the free 5 GB and 80 GB above it",
		"Sending:        paused until 1 February 2027: this month reached the monthly limit of EUR 20.00",
		"Amount so far:  EUR 20.00",
		"Paid logs:      on, up to EUR 20.00 a month",
		"To keep sending this month, an owner can raise the limit: 'ankra org hosted-logs set --limit <euros>'.",
	})
	if strings.Contains(stdout, "Turning on:") || strings.Contains(stdout, "Turn on paid logs with") ||
		strings.Contains(stdout, "unless an owner turns on paid logs") {
		t.Fatalf("paid logs that are on were offered as something to turn on:\n%s", stdout)
	}
}

// A prepaid organisation out of credit reads as paused for that reason.
func TestOrgHostedLogsShowPausedForWantOfCredit(t *testing.T) {
	view := hostedLogsJanuary2027AtTheLimit()
	reason := client.HostedLogsPausedNoCredit
	view.Month.State, view.Month.PausedReason, view.Month.AmountCents = client.HostedLogsStatePausedCredit, &reason, 150
	newHostedLogsBillingServer(t, view)

	stdout, _, runError := runOrgHostedLogs(t, "", "show")
	if runError != nil {
		t.Fatalf("show failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{
		"Sending:        paused: the organisation has no prepaid credit to pay for logs above the free 5 GB",
		"Amount so far:  EUR 1.50",
		"Sending resumes once the prepaid credit is topped up on the Billing page",
	})
}

func TestOrgHostedLogsShowJSONIsTheResponseBody(t *testing.T) {
	newHostedLogsBillingServer(t, hostedLogsJanuary2027PausedFree())

	stdout, stderr, runError := runOrgHostedLogs(t, "", "show", "-o", "json")
	if runError != nil {
		t.Fatalf("show -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, stdout)
	}
	for _, key := range []string{"paid_enabled", "monthly_limit_cents", "updated_at", "priced_from", "free_bytes_per_month",
		"cents_per_gb", "bytes_per_gb", "currency", "paid_readiness", "paid_blocker", "month"} {
		if _, present := decoded[key]; !present {
			t.Errorf("%s is missing from the JSON (a null member stays, as null):\n%s", key, stdout)
		}
	}
	month, _ := decoded["month"].(map[string]any)
	if month["state"] != "paused_free" || month["paused_reason"] != "free_allowance_used" || month["bytes"] != float64(5_200_000_000) {
		t.Errorf("month = %v, want the platform's raw figures", month)
	}
	if decoded["monthly_limit_cents"] != nil || decoded["priced_from"] != "2027-01-01" {
		t.Errorf("decoded = %v", decoded)
	}
	if stderr != "" {
		t.Errorf("nothing belongs on stderr, got %q", stderr)
	}
}

// Turning paid logs on says what the limit means, then stores it in cents.
func TestOrgHostedLogsSetPaidWithYesExplainsTheChargeAndTurnsItOn(t *testing.T) {
	recorder := newHostedLogsBillingServer(t, hostedLogsNovember2026())

	stdout, stderr, runError := runOrgHostedLogs(t, "", "set", "--paid", "--limit", "20", "--yes")
	if runError != nil {
		t.Fatalf("set --paid --limit 20 --yes failed: %v", runError)
	}
	writes := recorder.writes()
	if len(writes) != 1 {
		t.Fatalf("expected one PUT, got %+v", recorder.seen())
	}
	assertBodyIs(t, writes[0], map[string]any{"paid_enabled": true, "monthly_limit_cents": float64(2000)})
	assertContainsAll(t, stderr, []string{
		"Paid hosted logs: logs above the free 5 GB a month cost EUR 0.25 per GB, up to EUR 20.00 a month.",
		"EUR 20.00 pays for up to 80 GB of log text above the free 5 GB. At EUR 20.00, sending pauses for the rest of the month.",
		"Owners are told at 80% and 100% of the limit.",
		"Nothing is charged until 1 January 2027: hosted logs are included until 31 December 2026.",
	})
	if strings.Contains(stderr, "[y/N]") {
		t.Fatalf("--yes must skip the question:\n%s", stderr)
	}
	assertContainsAll(t, stdout, []string{"Paid hosted logs are on, up to EUR 20.00 a month.", "Paid logs:      on, up to EUR 20.00 a month"})
}

func TestOrgHostedLogsSetPaidWithoutYesIsRefusedWithoutATerminal(t *testing.T) {
	recorder := newHostedLogsBillingServer(t, hostedLogsNovember2026())

	_, stderr, runError := runOrgHostedLogs(t, "y\n", "set", "--paid", "--limit", "20")
	if code := exitCodeFor(runError); runError == nil || code != exitUsage {
		t.Fatalf("set --paid without --yes and without a terminal: %v (exit %d), want exit %d", runError, code, exitUsage)
	}
	if !strings.Contains(runError.Error(), "re-run with --yes to confirm") {
		t.Errorf("the refusal must say how to confirm: %v", runError)
	}
	assertContainsAll(t, stderr, []string{"up to EUR 20.00 a month"})
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("a refused set still wrote the setting: %+v", writes)
	}
}

func TestOrgHostedLogsSetAsksOnATerminal(t *testing.T) {
	t.Run("yes turns paid logs on", func(t *testing.T) {
		recorder := newHostedLogsBillingServer(t, hostedLogsNovember2026())
		answerPromptInteractively(t)

		stdout, stderr, runError := runOrgHostedLogs(t, "y\n", "set", "--paid", "--limit", "12.50")
		if runError != nil {
			t.Fatalf("a confirmed set failed: %v", runError)
		}
		assertContainsAll(t, stderr, []string{"up to EUR 12.50 a month", "EUR 12.50 pays for up to 50 GB", "Turn on paid hosted logs, up to EUR 12.50 a month? [y/N]"})
		if writes := recorder.writes(); len(writes) != 1 || writes[0].body["monthly_limit_cents"] != float64(1250) {
			t.Fatalf("a confirmed set must write once with 1250 cents, got %+v", writes)
		}
		assertContainsAll(t, stdout, []string{"Paid hosted logs are on, up to EUR 12.50 a month."})
	})

	t.Run("anything else cancels", func(t *testing.T) {
		recorder := newHostedLogsBillingServer(t, hostedLogsNovember2026())
		answerPromptInteractively(t)

		stdout, _, runError := runOrgHostedLogs(t, "n\n", "set", "--paid", "--limit", "20")
		if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
			t.Fatalf("a declined set must return errCancelled (exit 4), got %v", runError)
		}
		if writes := recorder.writes(); len(writes) != 0 {
			t.Fatalf("a declined set wrote the setting: %+v", writes)
		}
		if stdout != "" {
			t.Fatalf("a declined set printed a result:\n%s", stdout)
		}
	})
}

// Turning paid logs off charges nothing, so it does not ask, and keeps the
// stored limit by leaving it out.
func TestOrgHostedLogsSetPaidFalseTurnsItOffWithoutAsking(t *testing.T) {
	recorder := newHostedLogsBillingServer(t, hostedLogsJanuary2027AtTheLimit())

	stdout, stderr, runError := runOrgHostedLogs(t, "", "set", "--paid=false")
	if runError != nil {
		t.Fatalf("set --paid=false failed: %v", runError)
	}
	writes := recorder.writes()
	if len(writes) != 1 {
		t.Fatalf("expected one PUT, got %+v", recorder.seen())
	}
	assertBodyIs(t, writes[0], map[string]any{"paid_enabled": false})
	assertContainsAll(t, stderr, []string{
		"Paid hosted logs off: at the free 5 GB a month, sending pauses for the rest of the month. What this month already charged stays charged.",
		"This month already used the free 5 GB, so sending pauses at once.",
	})
	if strings.Contains(stderr, "[y/N]") {
		t.Fatalf("turning paid logs off must not ask:\n%s", stderr)
	}
	assertContainsAll(t, stdout, []string{"Paid hosted logs are off (monthly limit EUR 20.00 stored)."})
}

// --limit on its own changes the limit and leaves paid logs as they are:
// with them on it agrees to a new charge and asks; with them off it charges
// nothing and does not.
func TestOrgHostedLogsSetLimitAloneKeepsPaidLogsAsTheyAre(t *testing.T) {
	t.Run("paid logs on", func(t *testing.T) {
		recorder := newHostedLogsBillingServer(t, hostedLogsJanuary2027AtTheLimit())

		_, stderr, runError := runOrgHostedLogs(t, "", "set", "--limit", "50", "--yes")
		if runError != nil {
			t.Fatalf("set --limit 50 --yes failed: %v", runError)
		}
		writes := recorder.writes()
		if len(writes) != 1 {
			t.Fatalf("expected one PUT, got %+v", recorder.seen())
		}
		assertBodyIs(t, writes[0], map[string]any{"paid_enabled": true, "monthly_limit_cents": float64(5000)})
		assertContainsAll(t, stderr, []string{"Monthly limit: EUR 20.00 -> EUR 50.00.", "up to EUR 50.00 a month"})
	})

	t.Run("paid logs on asks about the new limit on a terminal", func(t *testing.T) {
		recorder := newHostedLogsBillingServer(t, hostedLogsJanuary2027AtTheLimit())
		answerPromptInteractively(t)

		_, stderr, runError := runOrgHostedLogs(t, "yes\n", "set", "--limit", "50")
		if runError != nil {
			t.Fatalf("a confirmed limit change failed: %v", runError)
		}
		assertContainsAll(t, stderr, []string{"Change the monthly limit to EUR 50.00? [y/N]"})
		if writes := recorder.writes(); len(writes) != 1 {
			t.Fatalf("expected one PUT, got %+v", recorder.seen())
		}
	})

	t.Run("paid logs off", func(t *testing.T) {
		recorder := newHostedLogsBillingServer(t, hostedLogsNovember2026())

		stdout, stderr, runError := runOrgHostedLogs(t, "", "set", "--limit", "30")
		if runError != nil {
			t.Fatalf("set --limit 30 failed: %v", runError)
		}
		writes := recorder.writes()
		if len(writes) != 1 {
			t.Fatalf("expected one PUT, got %+v", recorder.seen())
		}
		assertBodyIs(t, writes[0], map[string]any{"paid_enabled": false, "monthly_limit_cents": float64(3000)})
		assertContainsAll(t, stderr, []string{"Monthly limit: none set -> EUR 30.00. Paid hosted logs stay off"})
		assertContainsAll(t, stdout, []string{"Paid hosted logs are off (monthly limit EUR 30.00 stored)."})
	})
}

// A setting the organisation already has is not written again.
func TestOrgHostedLogsSetThatChangesNothingWritesNothing(t *testing.T) {
	recorder := newHostedLogsBillingServer(t, hostedLogsJanuary2027AtTheLimit())

	stdout, stderr, runError := runOrgHostedLogs(t, "", "set", "--paid", "--limit", "20.00")
	if runError != nil {
		t.Fatalf("an unchanged set failed: %v", runError)
	}
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("an unchanged set wrote the setting: %+v", writes)
	}
	assertContainsAll(t, stdout, []string{"Nothing changed. Paid hosted logs are on, up to EUR 20.00 a month."})
	if stderr != "" {
		t.Fatalf("an unchanged set explained or asked:\n%s", stderr)
	}
}

func TestOrgHostedLogsSetJSONStaysParseable(t *testing.T) {
	newHostedLogsBillingServer(t, hostedLogsNovember2026())

	stdout, stderr, runError := runOrgHostedLogs(t, "", "set", "--paid", "--limit", "20", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("set -o json failed: %v", runError)
	}
	var decoded client.HostedLogsBilling
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON (the explanation belongs on stderr): %v\n%s", decodeError, stdout)
	}
	if !decoded.PaidEnabled || decoded.MonthlyLimitCents == nil || *decoded.MonthlyLimitCents != 2000 {
		t.Fatalf("decoded %+v, want the stored setting", decoded)
	}
	assertContainsAll(t, stderr, []string{"up to EUR 20.00 a month"})
}

func TestOrgHostedLogsSetThatDidNotTakeEffectExitsNonZero(t *testing.T) {
	recorder := newHostedLogsBillingServer(t, hostedLogsNovember2026())
	recorder.ignoreWrites = true

	_, _, runError := runOrgHostedLogs(t, "", "set", "--paid", "--limit", "20", "--yes")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "stored paid hosted logs as off, not on") {
		t.Fatalf("a write the platform did not store must fail with exit 1: %v", runError)
	}
}

// Every refusal says what to do. The platform's own refusals of a write are
// printed as it wrote them: they name the remedy.
func TestOrgHostedLogsRefusals(t *testing.T) {
	cases := []struct {
		name        string
		arguments   []string
		view        func() client.HostedLogsBilling
		readStatus  int
		readBody    string
		writeStatus int
		writeBody   string
		wantExit    int
		wantMessage string
		// wantExact, when set, is the whole error.
		wantExact  string
		wantReads  int
		wantWrites int
	}{
		{name: "no flags", arguments: []string{"set"}, wantExit: exitUsage, wantMessage: "nothing to set"},
		{name: "a limit that is not an amount", arguments: []string{"set", "--paid", "--limit", "lots"}, wantExit: exitUsage,
			wantMessage: "expected an amount in euros"},
		{name: "a zero limit", arguments: []string{"set", "--paid", "--limit", "0"}, wantExit: exitUsage,
			wantMessage: "must be more than EUR 0.00"},
		{name: "a limit with fractions of a cent", arguments: []string{"set", "--paid", "--limit", "20.555"}, wantExit: exitUsage,
			wantMessage: "expected an amount in euros"},
		{name: "a limit above the maximum", arguments: []string{"set", "--paid", "--limit", "1000000.01"}, wantExit: exitUsage,
			wantMessage: "at most EUR 1,000,000.00"},
		{name: "paid logs with no limit given or stored", arguments: []string{"set", "--paid", "--yes"}, wantExit: exitUsage,
			wantMessage: "needs a monthly limit: add --limit <euros>", wantReads: 1},
		{name: "a positional argument", arguments: []string{"set", "true"}, wantExit: exitUsage, wantMessage: "unknown command"},
		{name: "402 prints the platform's remedy as given", arguments: []string{"set", "--paid", "--limit", "20", "--yes"},
			writeStatus: http.StatusPaymentRequired, writeBody: `{"detail":"` + hostedLogsBillingTestBlocker + `"}`,
			wantExit: exitError, wantExact: hostedLogsBillingTestBlocker, wantReads: 1, wantWrites: 1},
		{name: "503 says nothing changed", arguments: []string{"set", "--paid", "--limit", "20", "--yes"},
			writeStatus: http.StatusServiceUnavailable,
			writeBody:   `{"detail":"Ankra could not check your billing account just now, so paid hosted logs were not turned on. Try again in a minute."}`,
			wantExit:    exitError, wantExact: "Ankra could not check your billing account just now, so paid hosted logs were not turned on. Try again in a minute.",
			wantReads: 1, wantWrites: 1},
		{name: "a 400 is the platform's sentence", arguments: []string{"set", "--paid", "--limit", "20", "--yes"},
			writeStatus: http.StatusBadRequest, writeBody: `{"detail":"The monthly limit must be a positive number of cents."}`,
			wantExit: exitUsage, wantExact: "The monthly limit must be a positive number of cents.", wantReads: 1, wantWrites: 1},
		{name: "a write without billing.manage", arguments: []string{"set", "--paid=false"}, view: hostedLogsJanuary2027AtTheLimit,
			writeStatus: http.StatusForbidden, writeBody: `{"detail":"permission_denied","permission":"billing.manage"}`,
			wantExit: exitForbidden, wantMessage: "changing paid hosted logs needs the billing.manage permission", wantReads: 1, wantWrites: 1},
		{name: "a read without billing.read", arguments: []string{"show"},
			readStatus: http.StatusForbidden, readBody: `{"detail":"permission_denied","permission":"billing.read"}`,
			wantExit: exitForbidden, wantMessage: "reading paid hosted logs needs the billing.read permission", wantReads: 1},
		{name: "a platform without the route", arguments: []string{"show"},
			readStatus: http.StatusNotFound, readBody: `{"detail":"Not Found"}`,
			wantExit: exitError, wantMessage: "this Ankra platform does not offer paid hosted logs yet", wantReads: 1},
		{name: "set on a platform without the route writes nothing", arguments: []string{"set", "--paid", "--limit", "20", "--yes"},
			readStatus: http.StatusNotFound, readBody: `{"detail":"Not Found"}`,
			wantExit: exitError, wantMessage: "this Ankra platform does not offer paid hosted logs yet", wantReads: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			view := hostedLogsNovember2026()
			if testCase.view != nil {
				view = testCase.view()
			}
			recorder := newHostedLogsBillingServer(t, view)
			recorder.readStatus, recorder.readBody = testCase.readStatus, testCase.readBody
			recorder.writeStatus, recorder.writeBody = testCase.writeStatus, testCase.writeBody

			stdout, _, runError := runOrgHostedLogs(t, "", testCase.arguments...)
			if runError == nil {
				t.Fatalf("expected the refusal, got success:\n%s", stdout)
			}
			if code := exitCodeFor(runError); code != testCase.wantExit {
				t.Errorf("exit code = %d, want %d (%v)", code, testCase.wantExit, runError)
			}
			if testCase.wantExact != "" && runError.Error() != testCase.wantExact {
				t.Errorf("error = %q, want exactly %q", runError.Error(), testCase.wantExact)
			}
			if testCase.wantMessage != "" && !strings.Contains(runError.Error(), testCase.wantMessage) {
				t.Errorf("error = %q, want it to contain %q", runError.Error(), testCase.wantMessage)
			}
			reads, writes := 0, len(recorder.writes())
			for _, request := range recorder.seen() {
				if request.method == http.MethodGet {
					reads++
				}
			}
			if reads != testCase.wantReads || writes != testCase.wantWrites {
				t.Errorf("reads = %d, writes = %d, want %d and %d", reads, writes, testCase.wantReads, testCase.wantWrites)
			}
			if strings.Contains(stdout, "Hosted logs for") || strings.Contains(stdout, "Paid hosted logs are") {
				t.Errorf("a refused command printed a result:\n%s", stdout)
			}
		})
	}
}

func TestParseHostedLogsLimit(t *testing.T) {
	for raw, want := range map[string]int64{
		"20": 2000, "20.5": 2050, "20.50": 2050, "0.01": 1, " 7 ": 700, "1000000": 100_000_000, "1000000.00": 100_000_000,
	} {
		if got, parseError := parseHostedLogsLimit(raw); parseError != nil || got != want {
			t.Errorf("parseHostedLogsLimit(%q) = %d, %v; want %d", raw, got, parseError, want)
		}
	}
	for _, raw := range []string{"", "0", "0.00", "-5", "20.555", "20,50", "EUR 20", "1e3", "1000000.01", "99999999999999999999"} {
		if got, parseError := parseHostedLogsLimit(raw); parseError == nil {
			t.Errorf("parseHostedLogsLimit(%q) = %d, want a refusal", raw, got)
		}
	}
}

func TestOrgHostedLogsIsRegisteredWithItsFlags(t *testing.T) {
	found, _, findError := rootCmd.Find([]string{"org", "hosted-logs", "set"})
	if findError != nil || found.Name() != "set" {
		t.Fatalf("org hosted-logs set is not registered: %v", findError)
	}
	for _, flag := range []string{"paid", "limit", "yes", "output"} {
		if found.Flags().Lookup(flag) == nil {
			t.Errorf("org hosted-logs set lacks the --%s flag", flag)
		}
	}
	if show, _, showError := rootCmd.Find([]string{"org", "hosted-logs", "show"}); showError != nil || show.Name() != "show" ||
		show.Flags().Lookup("output") == nil {
		t.Errorf("org hosted-logs show is not registered with -o: %v", showError)
	}
	var help bytes.Buffer
	command := newOrgHostedLogsCommand()
	command.SetOut(&help)
	command.SetArgs([]string{"--help"})
	if helpError := command.Execute(); helpError != nil {
		t.Fatalf("--help: %v", helpError)
	}
	assertContainsAll(t, help.String(), []string{
		"included until 31 December 2026", "5 GB of log text a month free", "EUR 0.25 per GB", "kept 7 days",
		"Owners\nare told at 80% and 100%", "Built-in metrics are included in every plan", "billing.manage",
	})
}
