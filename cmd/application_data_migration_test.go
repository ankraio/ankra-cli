package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const dataMigrationTestRunID = "6b257030-1d8f-4a9d-92a3-88f346f248bf"

type applicationDataMigrationMock struct {
	baseMock
	run           *client.DataMigrationRun
	runs          []client.DataMigrationRun
	rollbackError error
	rollbacks     int
	readRunID     string
}

func (mock *applicationDataMigrationMock) ListApplicationDataMigrationRuns(requestContext context.Context,
	applicationID string) (*client.DataMigrationRunList, error) {
	return &client.DataMigrationRunList{Runs: mock.runs}, nil
}

func (mock *applicationDataMigrationMock) GetApplicationDataMigrationRun(requestContext context.Context,
	applicationID string, runID string) (*client.DataMigrationRun, error) {
	mock.readRunID = runID
	return mock.run, nil
}

func (mock *applicationDataMigrationMock) RollbackApplicationDataMigrationRun(requestContext context.Context,
	applicationID string, runID string) (*client.DataMigrationRun, error) {
	mock.rollbacks++
	if mock.rollbackError != nil {
		return nil, mock.rollbackError
	}
	requested := "2026-10-21T18:00:00Z"
	run := *mock.run
	run.RollbackRequestedAt = &requested
	return &run, nil
}

func cutOverRun() *client.DataMigrationRun {
	closes := "2026-10-28T12:00:00Z"
	return &client.DataMigrationRun{
		ID: dataMigrationTestRunID, Status: "cut-over", RollbackClosesAt: &closes,
		EnvironmentNames: []string{"DATABASE_URL"},
		Run: client.DataMigrationRunState{Status: "cut-over", Steps: []client.DataMigrationStep{
			{Key: "postgres:cutover:freeze-source:postgres-replication", Status: "succeeded",
				Detail: "shop is read-only; 2 session(s) opened before the freeze were ended"},
			{Key: "application:flip-environment", Status: "succeeded", Detail: "rewired 1 variable(s)"},
		}, Rollback: []client.DataMigrationStep{{Key: "application:rollback:restore-environment", Status: "pending"}}},
	}
}

func TestDataMigrationRollbackAsksFirstAndSaysHowToFollowIt(t *testing.T) {
	mockClient := &applicationDataMigrationMock{run: cutOverRun()}
	output, executeError := runApplicationCommandWithInput(t, mockClient, "y\n",
		"data-migration", "rollback", testApplicationID, dataMigrationTestRunID)
	if executeError != nil || mockClient.rollbacks != 1 {
		t.Fatalf("rollback error = %v, calls = %d", executeError, mockClient.rollbacks)
	}
	for _, said := range []string{"writable again", "Rollback of data migration " + dataMigrationTestRunID + " requested",
		"ankra application data-migration get", "rolled-back"} {
		if !strings.Contains(output, said) {
			t.Errorf("output does not say %q:\n%s", said, output)
		}
	}
}

func TestDataMigrationRollbackDeclinedRollsNothingBack(t *testing.T) {
	mockClient := &applicationDataMigrationMock{run: cutOverRun()}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "n\n",
		"data-migration", "rollback", testApplicationID, dataMigrationTestRunID)
	if !errors.Is(executeError, errCancelled) || exitCodeFor(executeError) != exitCancelled || mockClient.rollbacks != 0 {
		t.Fatalf("declined: error = %v, exit = %d, calls = %d", executeError, exitCodeFor(executeError),
			mockClient.rollbacks)
	}
}

func TestDataMigrationRollbackRefusalCarriesThePlatformsSentence(t *testing.T) {
	mockClient := &applicationDataMigrationMock{run: cutOverRun(), rollbackError: errors.New(
		"The cutover has not started, so there is nothing to roll back.")}
	_, executeError := runApplicationCommand(t, mockClient,
		"data-migration", "rollback", testApplicationID, dataMigrationTestRunID, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "nothing to roll back") ||
		exitCodeFor(executeError) != exitError {
		t.Fatalf("refusal: %v (exit %d)", executeError, exitCodeFor(executeError))
	}
}

func TestDataMigrationRollbackJSONKeepsStdoutParseable(t *testing.T) {
	mockClient := &applicationDataMigrationMock{run: cutOverRun()}
	output, executeError := runApplicationCommand(t, mockClient,
		"data-migration", "rollback", testApplicationID, dataMigrationTestRunID, "--yes", "-o", "json")
	var decoded client.DataMigrationRun
	if executeError != nil || json.Unmarshal([]byte(output), &decoded) != nil || decoded.RollbackRequestedAt == nil {
		t.Fatalf("json output: %v\n%s", executeError, output)
	}
}

func TestDataMigrationGetShowsStepsAndTheWindow(t *testing.T) {
	mockClient := &applicationDataMigrationMock{run: cutOverRun()}
	output, executeError := runApplicationCommand(t, mockClient,
		"data-migration", "get", testApplicationID, dataMigrationTestRunID)
	if executeError != nil || mockClient.readRunID != dataMigrationTestRunID {
		t.Fatalf("get error = %v, run = %q", executeError, mockClient.readRunID)
	}
	for _, said := range []string{"cut-over", "2026-10-28T12:00:00Z", "DATABASE_URL", "flip-environment",
		"shop is read-only"} {
		if !strings.Contains(output, said) {
			t.Errorf("output does not say %q:\n%s", said, output)
		}
	}
	if strings.Contains(output, "Rollback:") {
		t.Errorf("a rollback that never started is listed:\n%s", output)
	}
}

func TestDataMigrationListSaysWhenNothingStarted(t *testing.T) {
	output, executeError := runApplicationCommand(t, &applicationDataMigrationMock{},
		"data-migration", "list", testApplicationID)
	if executeError != nil || !strings.Contains(output, "No data migration has been started") {
		t.Fatalf("list: %v\n%s", executeError, output)
	}
	requested := "2026-10-21T18:00:00Z"
	run := *cutOverRun()
	run.RollbackRequestedAt = &requested
	output, executeError = runApplicationCommand(t, &applicationDataMigrationMock{runs: []client.DataMigrationRun{run}},
		"data-migration", "list", testApplicationID)
	if executeError != nil || !strings.Contains(output, "cut-over (rollback requested)") {
		t.Fatalf("list: %v\n%s", executeError, output)
	}
}
