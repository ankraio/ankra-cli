package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

type aiMemoriesMock struct {
	baseMock
	memories   []client.AIMemory
	deleted    int
	routeError error
	limits     []int
	clears     int
}

func (mock *aiMemoriesMock) ListAIMemories(limit int) ([]client.AIMemory, error) {
	mock.limits = append(mock.limits, limit)
	if mock.routeError != nil {
		return nil, mock.routeError
	}
	return mock.memories, nil
}

func (mock *aiMemoriesMock) ClearAIMemories() (int, error) {
	mock.clears++
	if mock.routeError != nil {
		return 0, mock.routeError
	}
	return mock.deleted, nil
}

func runAIMemoriesCommand(t *testing.T, mock APIClient, stdin string, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stdout)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	resetTreeFlags(t, aiMemoriesListCmd, aiMemoriesClearCmd)
	t.Cleanup(func() { resetTreeFlags(t, aiMemoriesListCmd, aiMemoriesClearCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func TestAIMemoriesListShowsTheNewestAndSaysWhenItIsCapped(t *testing.T) {
	mock := &aiMemoriesMock{memories: []client.AIMemory{
		{ID: "m1", Content: "Resolution: delete the stale Secret, then redeploy", CreatedAt: time.Date(2026, 10, 16, 1, 7, 30, 0, time.UTC)},
	}}
	output, runError := runAIMemoriesCommand(t, mock, "", "ai", "memories", "list", "--limit", "1")
	if runError != nil {
		t.Fatalf("ai memories list: %v", runError)
	}
	if len(mock.limits) != 1 || mock.limits[0] != 1 {
		t.Fatalf("limits asked = %v, want [1]", mock.limits)
	}
	for _, want := range []string{"2026-10-16 01:07", "delete the stale Secret", "there may be more"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestAIMemoriesListSaysSoWhenThereAreNone(t *testing.T) {
	output, runError := runAIMemoriesCommand(t, &aiMemoriesMock{memories: []client.AIMemory{}}, "", "ai", "memories", "list")
	if runError != nil || !strings.Contains(output, "no memories stored for you") {
		t.Fatalf("output = %q, error = %v", output, runError)
	}
}

func TestAIMemoriesClearDeletesAfterConfirmation(t *testing.T) {
	mock := &aiMemoriesMock{deleted: 37}
	output, runError := runAIMemoriesCommand(t, mock, "", "ai", "memories", "clear", "--yes")
	if runError != nil || mock.clears != 1 || !strings.Contains(output, "Deleted 37 memories.") {
		t.Fatalf("output = %q, clears = %d, error = %v", output, mock.clears, runError)
	}
}

func TestAIMemoriesClearDeclinedDeletesNothing(t *testing.T) {
	mock := &aiMemoriesMock{deleted: 5}
	_, runError := runAIMemoriesCommand(t, mock, "n\n", "ai", "memories", "clear")
	if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("declined clear error = %v (exit %d), want errCancelled exit %d", runError, exitCodeFor(runError), exitCancelled)
	}
	if mock.clears != 0 {
		t.Fatalf("a declined clear called the platform %d time(s)", mock.clears)
	}
}

// A 404 means the platform has no token route yet; exiting 3 would read as "you have no memories".
func TestAIMemoriesNamesAPlatformWithoutTheTokenRoute(t *testing.T) {
	mock := &aiMemoriesMock{routeError: &client.UnexpectedResponseError{StatusCode: 404}}
	_, runError := runAIMemoriesCommand(t, mock, "", "ai", "memories", "list")
	if runError == nil || !strings.Contains(runError.Error(), "predates the token route") || exitCodeFor(runError) != exitError {
		t.Fatalf("error = %v (exit %d), want the predates-the-route message with exit %d", runError, exitCodeFor(runError), exitError)
	}
}
