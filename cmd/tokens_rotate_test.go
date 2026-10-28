package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

type rotateMock struct {
	baseMock
	personalCalls, serviceCalls int
	gotID                       string
	gotOverlap                  *int
	response                    *client.RotateTokenResponse
	err                         error
}

func (m *rotateMock) RotateAPIToken(tokenID string, overlapHours *int) (*client.RotateTokenResponse, error) {
	m.personalCalls++
	m.gotID, m.gotOverlap = tokenID, overlapHours
	return m.response, m.err
}

func (m *rotateMock) RotateServiceToken(tokenID string, overlapHours *int) (*client.RotateTokenResponse, error) {
	m.serviceCalls++
	m.gotID, m.gotOverlap = tokenID, overlapHours
	return m.response, m.err
}

func newRotateMock() *rotateMock {
	return &rotateMock{response: &client.RotateTokenResponse{
		ID: "new-id", Token: "ankra_new_secret", Name: "ci-deploy", ExpiresAt: "2027-01-13T10:00:00Z",
		Type: "Bearer", ReplacedTokenID: "old-id", OldTokenValidUntil: "2026-10-16T10:00:00Z",
	}}
}

func runRotate(t *testing.T, isTerminal bool, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	original := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return isTerminal }
	t.Cleanup(func() {
		stdoutIsTerminal = original
		// The command is a package global: put its flags back so the next
		// test starts from the defaults.
		tokensRotateCmd.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	})
	var commandOutput string
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			commandOutput, err = executeCommand(append([]string{"tokens", "rotate"}, args...)...)
		})
	})
	// Human output goes to os.Stdout; -o json goes to the command's writer.
	if err == nil {
		stdout += commandOutput
	}
	return stdout, stderr, err
}

func TestTokensRotateNonTerminalPrintsOnlyTheValueOnStdout(t *testing.T) {
	mock := newRotateMock()
	setMockClient(t, mock)
	stdout, stderr, err := runRotate(t, false, "old-id")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if stdout != "ankra_new_secret\n" {
		t.Fatalf("stdout = %q, want only the token value", stdout)
	}
	if strings.Contains(stderr, "ankra_new_secret") {
		t.Errorf("stderr must not carry the token: %q", stderr)
	}
	for _, want := range []string{"Renewed ci-deploy.", "Expires 2027-01-13 10:00 UTC.",
		"The old token keeps working until 2026-10-16 10:00 UTC. Update where it is used, then it stops by itself."} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q: %q", want, stderr)
		}
	}
	if mock.personalCalls != 1 || mock.serviceCalls != 0 || mock.gotID != "old-id" || mock.gotOverlap != nil {
		t.Errorf("calls = personal %d service %d id %q overlap %v", mock.personalCalls, mock.serviceCalls,
			mock.gotID, mock.gotOverlap)
	}
}

func TestTokensRotateTerminalPrintsTheBlock(t *testing.T) {
	setMockClient(t, newRotateMock())
	stdout, stderr, err := runRotate(t, true, "old-id")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	want := "Renewed ci-deploy. New token (shown once): ankra_new_secret\n" +
		"Expires 2027-01-13 10:00 UTC.\n" +
		"The old token keeps working until 2026-10-16 10:00 UTC. Update where it is used, then it stops by itself.\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if strings.Contains(stderr, "Renewed") || strings.Contains(stderr, "ankra_new_secret") {
		t.Errorf("a terminal run prints the block on stdout only, stderr = %q", stderr)
	}
}

func TestTokensRotateServiceFlagAndOverlapHours(t *testing.T) {
	mock := newRotateMock()
	setMockClient(t, mock)
	if _, _, err := runRotate(t, false, "old-id", "--service", "--overlap-hours", "0"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if mock.serviceCalls != 1 || mock.personalCalls != 0 {
		t.Fatalf("want the service route, got personal %d service %d", mock.personalCalls, mock.serviceCalls)
	}
	if mock.gotOverlap == nil || *mock.gotOverlap != 0 {
		t.Errorf("overlap = %v, want 0 sent", mock.gotOverlap)
	}
}

func TestTokensRotateJSONOutputIsTheResponse(t *testing.T) {
	setMockClient(t, newRotateMock())
	stdout, _, err := runRotate(t, false, "old-id", "-o", "json")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	for _, want := range []string{`"token": "ankra_new_secret"`, `"replaced_token_id": "old-id"`,
		`"old_token_valid_until": "2026-10-16T10:00:00Z"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("json missing %s: %s", want, stdout)
		}
	}
}

func TestTokensRotateRejectsOverlapHoursOutOfRange(t *testing.T) {
	for _, hours := range []string{"-1", "169"} {
		mock := newRotateMock()
		setMockClient(t, mock)
		_, _, err := runRotate(t, false, "old-id", "--overlap-hours", hours)
		if err == nil || !strings.Contains(err.Error(), "between 0 and 168") {
			t.Fatalf("hours %s: err = %v", hours, err)
		}
		if exitCodeFor(err) != exitUsage {
			t.Errorf("hours %s: exit code = %d, want usage", hours, exitCodeFor(err))
		}
		if mock.personalCalls != 0 {
			t.Errorf("hours %s: the API must not be called", hours)
		}
	}
}

func TestTokensRotateErrorsNameTheProblemAndNeverTheToken(t *testing.T) {
	cases := []struct {
		status int
		detail string
		code   int
	}{
		{404, "Token not found", exitNotFound},
		{409, "Token is revoked", exitError},
	}
	for _, testCase := range cases {
		mock := newRotateMock()
		mock.response = nil
		mock.err = client.NewUnexpectedResponseError(testCase.status, testCase.detail)
		setMockClient(t, mock)
		stdout, _, err := runRotate(t, false, "old-id")
		if err == nil || !strings.Contains(err.Error(), testCase.detail) {
			t.Fatalf("status %d: err = %v", testCase.status, err)
		}
		if stdout != "" {
			t.Errorf("status %d: stdout = %q, want empty", testCase.status, stdout)
		}
		if exitCodeFor(err) != testCase.code {
			t.Errorf("status %d: exit code = %d, want %d", testCase.status, exitCodeFor(err), testCase.code)
		}
		if strings.Contains(err.Error(), "ankra_new_secret") {
			t.Errorf("status %d: error carries the token: %v", testCase.status, err)
		}
	}
}

func TestRequireRotatedTokenValueRefusesAnAnswerWithoutTheToken(t *testing.T) {
	if err := requireRotatedTokenValue(&client.RotateTokenResponse{Token: "ankra_new"}); err != nil {
		t.Fatalf("a token value must pass, got %v", err)
	}
	for name, rotated := range map[string]*client.RotateTokenResponse{
		"nil answer":  nil,
		"empty token": {Name: "ci", OldTokenValidUntil: "2026-10-17T10:00:00Z"},
	} {
		if err := requireRotatedTokenValue(rotated); err == nil || !strings.Contains(err.Error(), "without the new token value") {
			t.Errorf("%s: want a refusal naming the missing value, got %v", name, err)
		}
	}
}
