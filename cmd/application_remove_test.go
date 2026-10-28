package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const (
	removeTestCluster      = "3b655051-1111-4222-8333-444444444444"
	removeTestOtherCluster = "3b655051-1111-4222-8333-555555555555"
)

type applicationRemoveMock struct {
	baseMock
	installations      json.RawMessage
	installationsReads int
	retainedStacks     []string
	fail               error
	removeCalls        int
	removeRequest      client.RemoveApplicationDeploymentRequest
}

func (mock *applicationRemoveMock) GetApplicationInstallations(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.installationsReads++
	return mock.installations, nil
}

func (mock *applicationRemoveMock) RemoveApplicationDeployment(requestContext context.Context, applicationID string,
	removeRequest client.RemoveApplicationDeploymentRequest) (*client.RemoveApplicationDeploymentResult, error) {
	mock.removeCalls++
	mock.removeRequest = removeRequest
	if mock.fail != nil {
		return nil, mock.fail
	}
	if removeRequest.StackName != "" {
		return &client.RemoveApplicationDeploymentResult{ClusterID: removeRequest.ClusterID,
			StackName: removeRequest.StackName, RetainedStacks: mock.retainedStacks, OperationID: "o-2",
			Status: "removing"}, nil
	}
	return &client.RemoveApplicationDeploymentResult{ClusterID: removeRequest.ClusterID,
		Namespace: removeRequest.Namespace, InstallationID: "i-1", OperationID: "o-1", Status: "removing"}, nil
}

func installationsPayload(rows ...[3]string) json.RawMessage {
	installations := []map[string]string{}
	for _, row := range rows {
		installations = append(installations, map[string]string{"cluster_id": row[0], "namespace": row[1], "status": row[2]})
	}
	encoded, _ := json.Marshal(map[string]any{"installations": installations})
	return encoded
}

func TestApplicationRemoveTakesTheOneDeploymentOnThatCluster(t *testing.T) {
	mockClient := &applicationRemoveMock{installations: installationsPayload(
		[3]string{removeTestCluster, "shop", "healthy"},
		[3]string{removeTestOtherCluster, "shop", "healthy"},
	)}
	output, executeError := runApplicationCommandWithInput(t, mockClient, "y\n",
		"remove", testApplicationID, "--cluster", removeTestCluster)
	if executeError != nil {
		t.Fatalf("remove error = %v", executeError)
	}
	if mockClient.removeCalls != 1 ||
		mockClient.removeRequest != (client.RemoveApplicationDeploymentRequest{ClusterID: removeTestCluster, Namespace: "shop"}) {
		t.Fatalf("remove request = %d calls, %+v", mockClient.removeCalls, mockClient.removeRequest)
	}
	for _, said := range []string{"including data in that deployment's database and volumes",
		"The application and its other deployments stay", "ankra application installations"} {
		if !strings.Contains(output, said) {
			t.Errorf("output does not say %q:\n%s", said, output)
		}
	}
}

func TestApplicationRemoveDeclinedRemovesNothing(t *testing.T) {
	mockClient := &applicationRemoveMock{installations: installationsPayload([3]string{removeTestCluster, "shop", "healthy"})}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "n\n",
		"remove", testApplicationID, "--cluster", removeTestCluster)
	if !errors.Is(executeError, errCancelled) || exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("error = %v (exit %d), want cancelled", executeError, exitCodeFor(executeError))
	}
	if mockClient.removeCalls != 0 {
		t.Fatalf("a declined removal called the platform %d times", mockClient.removeCalls)
	}
}

func TestApplicationRemoveAsksWhichNamespaceWhenThereAreTwo(t *testing.T) {
	mockClient := &applicationRemoveMock{installations: installationsPayload(
		[3]string{removeTestCluster, "shop-a", "healthy"},
		[3]string{removeTestCluster, "shop-b", "healthy"},
	)}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "y\n",
		"remove", testApplicationID, "--cluster", removeTestCluster)
	if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "--namespace") {
		t.Fatalf("error = %v (exit %d), want a usage error asking for --namespace", executeError, exitCodeFor(executeError))
	}
	_, executeError = runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--namespace", "shop-b", "--yes")
	if executeError != nil || mockClient.removeRequest.Namespace != "shop-b" {
		t.Fatalf("with --namespace: error = %v, request = %+v", executeError, mockClient.removeRequest)
	}
}

func TestApplicationRemoveSaysWhereItRunsWhenNotOnThatCluster(t *testing.T) {
	mockClient := &applicationRemoveMock{installations: installationsPayload([3]string{removeTestOtherCluster, "shop", "healthy"})}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--yes")
	if exitCodeFor(executeError) != exitNotFound || !strings.Contains(executeError.Error(), removeTestOtherCluster+" / shop") ||
		!strings.Contains(executeError.Error(), "If it was deployed with the deploy wizard, pass --stack <name>.") {
		t.Fatalf("error = %v (exit %d)", executeError, exitCodeFor(executeError))
	}
	if mockClient.removeCalls != 0 {
		t.Fatalf("called the platform for a deployment that is not there")
	}
}

// An application with no installations at all may still run as a wizard
// stack, so the miss says "no installations" and points at --stack.
func TestApplicationRemoveWithNoInstallationsPointsAtStack(t *testing.T) {
	mockClient := &applicationRemoveMock{installations: installationsPayload()}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--yes")
	if exitCodeFor(executeError) != exitNotFound ||
		!strings.Contains(executeError.Error(), "has no installations on any cluster") ||
		!strings.Contains(executeError.Error(), "pass --stack <name>") {
		t.Fatalf("error = %v (exit %d)", executeError, exitCodeFor(executeError))
	}
	if mockClient.removeCalls != 0 {
		t.Fatalf("called the platform for an application with no installations")
	}
}

// --stack takes a deploy-wizard deployment off the cluster: no installation is
// looked up, the body names only the cluster and the stack, the prompt names
// the stack, and the follow-up lists the stacks that deploy left standing.
func TestApplicationRemoveStackTakesTheWizardDeployment(t *testing.T) {
	mockClient := &applicationRemoveMock{retainedStacks: []string{"shop-db", "shop-cache"}}
	output, executeError := runApplicationCommandWithInput(t, mockClient, "y\n",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--stack", "shop-web")
	if executeError != nil {
		t.Fatalf("remove error = %v", executeError)
	}
	if mockClient.installationsReads != 0 {
		t.Fatalf("--stack read the installations %d times", mockClient.installationsReads)
	}
	if mockClient.removeCalls != 1 ||
		mockClient.removeRequest != (client.RemoveApplicationDeploymentRequest{ClusterID: removeTestCluster, StackName: "shop-web"}) {
		t.Fatalf("remove request = %d calls, %+v", mockClient.removeCalls, mockClient.removeRequest)
	}
	encoded, encodeError := json.Marshal(mockClient.removeRequest)
	if encodeError != nil || string(encoded) != `{"cluster_id":"`+removeTestCluster+`","stack_name":"shop-web"}` {
		t.Fatalf("request body = %s (%v)", encoded, encodeError)
	}
	for _, said := range []string{
		"from cluster " + removeTestCluster + " (stack shop-web)?",
		"The application, its other deployments and the other stacks that deploy created stay. [y/N]: ",
		"Removing " + testApplicationID + " from cluster " + removeTestCluster + " (stack shop-web).",
		"Left standing: the other stacks that deploy created (shop-db, shop-cache).",
		"Follow it with 'ankra application jobs " + testApplicationID + "': its undeploy_application job",
	} {
		if !strings.Contains(output, said) {
			t.Errorf("output does not say %q:\n%s", said, output)
		}
	}
}

func TestApplicationRemoveStackRendersJSON(t *testing.T) {
	mockClient := &applicationRemoveMock{retainedStacks: []string{"shop-db"}}
	output, executeError := runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--stack", "shop-web", "--yes", "-o", "json")
	if executeError != nil {
		t.Fatalf("remove error = %v", executeError)
	}
	jsonStart := strings.Index(output, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in output: %s", output)
	}
	var rendered map[string]any
	if decodeError := json.Unmarshal([]byte(output[jsonStart:]), &rendered); decodeError != nil {
		t.Fatalf("decode output: %v\n%s", decodeError, output)
	}
	if rendered["stack_name"] != "shop-web" || rendered["installation_id"] != nil {
		t.Fatalf("rendered = %v", rendered)
	}
	if retained, _ := rendered["retained_stacks"].([]any); len(retained) != 1 || retained[0] != "shop-db" {
		t.Fatalf("retained_stacks = %v", rendered["retained_stacks"])
	}
}

func TestApplicationRemoveStackAndNamespaceIsAUsageError(t *testing.T) {
	mockClient := &applicationRemoveMock{}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--stack", "shop-web", "--namespace", "shop", "--yes")
	if exitCodeFor(executeError) != exitUsage ||
		!strings.Contains(executeError.Error(), "--stack and --namespace cannot be used together") {
		t.Fatalf("error = %v (exit %d), want a usage error", executeError, exitCodeFor(executeError))
	}
	if mockClient.removeCalls != 0 || mockClient.installationsReads != 0 {
		t.Fatalf("a usage error reached the platform: %d removes, %d reads", mockClient.removeCalls, mockClient.installationsReads)
	}
}

func TestApplicationRemoveRefusals(t *testing.T) {
	mockClient := &applicationRemoveMock{installations: installationsPayload([3]string{removeTestCluster, "shop", "removing"})}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "already being removed") || mockClient.removeCalls != 0 {
		t.Fatalf("a removal already running: error = %v, calls = %d", executeError, mockClient.removeCalls)
	}

	mockClient = &applicationRemoveMock{
		installations: installationsPayload([3]string{removeTestCluster, "shop", "healthy"}),
		fail: client.NewUnexpectedResponseError(422,
			"This application is not deployed to that cluster and namespace, so there is nothing to remove."),
	}
	_, executeError = runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--yes")
	if exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("a 422: error = %v (exit %d), want not found", executeError, exitCodeFor(executeError))
	}

	_, executeError = runApplicationCommandWithInput(t, mockClient, "", "remove", testApplicationID, "--yes")
	if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "--cluster is required") {
		t.Fatalf("no --cluster: error = %v (exit %d)", executeError, exitCodeFor(executeError))
	}
}

func TestApplicationRemoveRendersJSON(t *testing.T) {
	mockClient := &applicationRemoveMock{installations: installationsPayload([3]string{removeTestCluster, "shop", "healthy"})}
	output, executeError := runApplicationCommandWithInput(t, mockClient, "",
		"remove", testApplicationID, "--cluster", removeTestCluster, "--yes", "-o", "json")
	if executeError != nil {
		t.Fatalf("remove error = %v", executeError)
	}
	jsonStart := strings.Index(output, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in output: %s", output)
	}
	var rendered map[string]any
	if decodeError := json.Unmarshal([]byte(output[jsonStart:]), &rendered); decodeError != nil {
		t.Fatalf("decode output: %v\n%s", decodeError, output)
	}
	if rendered["status"] != "removing" || rendered["operation_id"] != "o-1" {
		t.Fatalf("rendered = %v", rendered)
	}
}
