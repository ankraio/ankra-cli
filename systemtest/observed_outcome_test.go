package systemtest

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These tests pin ankra-ubycim. Run 42712361601 (2026-11-09) printed
// "PASS aws/k3s k8s upgrade -> v1.37.2+k3s1 (cp+worker Ready)" one second
// after submitting the upgrade, and PASS for the hetzner/k3s upgrade and the
// hetzner/k3s resize to cpx32. All three operations failed minutes later,
// parked on PodDisruptionBudgets, and the run read 24 passed, 0 failed: the
// steps read the version `cluster list` shows and the type= `node-group list`
// shows, which are the REQUESTED values from the moment the write is
// accepted. They replay the lane steps themselves - the block of
// run_provider, run_aws_provider and run_managed_provider, cut out of the
// script - against a fake ankra that answers what the platform answered in
// that run: the write's output, the operation's JSON (testdata/ubycim, from
// `ankra cluster operations list <id> -o json`), the requested version and
// type in the listings, and nodes that never changed.

const (
	hetznerClusterName = "systest-hetzner-k3s-261109223029"
	hetznerClusterID   = "4c8360f0-d3e4-4aae-b492-929fb424b4de"
	awsClusterName     = "systest-aws-k3s-261109223029"
	awsClusterID       = "86b8e10f-2f9e-4b56-bd36-bb47296d499f"
	managedClusterName = "systest-doks-261109223029"
	managedClusterID   = "6a1f7c52-2d0e-4b8e-9b1f-0c6f3e2a9d41"

	upgradeTarget  = "v1.37.2+k3s1"
	createdVersion = "v1.36.6+k3s1"

	awsUpgradeOperation     = "be3b5487-15a4-4fb0-bbf7-b0356af4cbff"
	hetznerUpgradeOperation = "1efa093c-1587-4166-a34f-f200cc8b41f4"
	hetznerResizeOperation  = "a2caca6b-ab68-4ec1-90df-5b5e6e9b54e8"
	managedUpgradeOperation = "0d7e7c1e-5d55-4c39-8f43-2a9b7c4f1e20"
)

// fakeLaneAnkra answers the reads and writes the upgrade, resize and
// deprovision-wait steps make, from files in $FAKE_DIR, and records every
// call and every reconcile it is asked to start.
const fakeLaneAnkra = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_DIR/calls"
case "$*" in
  "cluster select "*) ;;
  "cluster list"*)
    reads=$(cat "$FAKE_DIR/list-reads" 2>/dev/null || echo 0)
    reads=$((reads + 1))
    echo "$reads" > "$FAKE_DIR/list-reads"
    if [ -f "$FAKE_DIR/present-for" ] && [ "$reads" -gt "$(cat "$FAKE_DIR/present-for")" ]; then
      cat "$FAKE_DIR/cluster-list-gone"
    else
      cat "$FAKE_DIR/cluster-list"
    fi
    ;;
  "cluster operations list -o json"|"cluster operations list --cluster "*) cat "$FAKE_DIR/operations.json" ;;
  "cluster operations list "*" -o json") cat "$FAKE_DIR/operation-$4.json" ;;
  "cluster operations list"*) ;;
  "cluster operations steps "*) cat "$FAKE_DIR/steps.json" 2>/dev/null ;;
  "cluster upgrade "*) cat "$FAKE_DIR/upgrade-answer" ;;
  "cluster managed upgrade "*)
    cat "$FAKE_DIR/managed-upgrade-answer"
    if [ -f "$FAKE_DIR/operations-after-write.json" ]; then
      cp "$FAKE_DIR/operations-after-write.json" "$FAKE_DIR/operations.json"
    fi
    ;;
  "cluster node-group upgrade "*"--wait"*) cat "$FAKE_DIR/resize-answer-wait" ;;
  "cluster node-group upgrade "*) cat "$FAKE_DIR/resize-answer" ;;
  "cluster node-group list "*) cat "$FAKE_DIR/node-group-list" ;;
  "cluster get nodes "*"-o json"*) cat "$FAKE_DIR/nodes.json" ;;
  "cluster get nodes"*) cat "$FAKE_DIR/nodes-table" 2>/dev/null ;;
  "cluster hetzner server-types "*) printf 'cpx22 2 4 80 x86 0.0100 yes\ncpx32 4 8 160 x86 0.0200 yes\n' ;;
  "cluster reconcile"*) echo reconcile >> "$FAKE_DIR/reconciles" ;;
esac
exit 0
`

// The answers the CLI printed. The upgrade's is cmd/cluster_upgrade.go's
// output with the run's operation; the resize's are cmd/node_group_async.go's
// (submitted without --wait: no operation) and cmd/node_group_write_output.go's
// (with --wait: the operation that resizes the nodes).
func upgradeAnswer(provider, clusterID, operation string) string {
	return "Kubernetes version upgrade initiated.\n" +
		"  Provider:         " + provider + "\n" +
		"  Previous version: " + createdVersion + "\n" +
		"  New version:      " + upgradeTarget + "\n" +
		"  Nodes affected:   2\n" +
		"  Operation:        " + operation + "\n" +
		"Follow progress with 'ankra cluster operations list " + operation + " --cluster " + clusterID + "'.\n"
}

const resizeAnswerSubmitted = "Node group instance-type update submitted.\n" +
	"The platform records the change, then creates, removes or changes the nodes in the background.\n" +
	"The record appears once the refusal is decided, so an empty listing right away does not mean the change was applied.\n"

func resizeAnswerWait(operation string) string {
	return "Node group 'default' instance type change recorded for 1 node(s); the resize rolls out in operation " + operation + ".\n" +
		"Follow it with:\n  ankra cluster operations list " + operation + "\n"
}

// clusterList is `ankra cluster list` as the platform answered it after the
// upgrade was accepted: the requested version, at once.
func clusterList(name, version string) string {
	return "╭──────┬───────┬─────────────╮\n" +
		"│ NAME │ STATE │ KUBEVERSION │\n" +
		"├──────┼───────┼─────────────┤\n" +
		"│ " + name + " │ online │ " + version + " │\n" +
		"╰──────┴───────┴─────────────╯\n"
}

type fakeNode struct {
	name, kubelet, instanceType, group string
	controlPlane, notReady             bool
}

// nodesJSON is `ankra cluster get nodes -o json`: the Kubernetes Node objects
// under resource_responses[0].items, as the Hetzner sample read showed them.
func nodesJSON(t *testing.T, nodes ...fakeNode) string {
	t.Helper()
	items := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		labels := map[string]string{}
		if node.instanceType != "" {
			labels["node.kubernetes.io/instance-type"] = node.instanceType
			labels["beta.kubernetes.io/instance-type"] = node.instanceType
		}
		if node.group != "" {
			labels["ankra.cloud/node-group"] = node.group
		}
		if node.controlPlane {
			labels["node-role.kubernetes.io/control-plane"] = "true"
		}
		ready := "True"
		if node.notReady {
			ready = "False"
		}
		items = append(items, map[string]any{
			"metadata": map[string]any{"name": node.name, "labels": labels},
			"status": map[string]any{
				"conditions": []map[string]string{{"type": "Ready", "status": ready}},
				"nodeInfo":   map[string]string{"kubeletVersion": node.kubelet},
			},
		})
	}
	document, err := json.Marshal(map[string]any{"resource_responses": []map[string]any{{"items": items, "total_count": len(items)}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

func hetznerNodes(t *testing.T, kubelet, workerType string) string {
	return nodesJSON(t,
		fakeNode{name: hetznerClusterName + "-4c8360f0-cp-0", kubelet: kubelet, instanceType: "cpx32", controlPlane: true},
		fakeNode{name: hetznerClusterName + "-4c8360f0-default-0", kubelet: kubelet, instanceType: workerType, group: "default"},
	)
}

// operationJSON is `ankra cluster operations list <id> -o json` for an
// operation that ended with the given status.
func operationJSON(id, status, excerpt string) string {
	errorExcerpt := "null"
	if excerpt != "" {
		errorExcerpt = jsonString(excerpt)
	}
	return `{"execution":{"id":"` + id + `","name":"reconcile","display_name":"Reconcile 2 resources","status":"` + status +
		`","error_excerpt":` + errorExcerpt + `},"steps":[]}`
}

func testdataFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "ubycim", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// sourceableScript copies lifecycle_systemtest.sh without its closing
// `main "$@"` line, next to reconcile_failure.sh, so a test can source every
// helper exactly as the script defines it and run one step.
func sourceableScript(t *testing.T, dir string) string {
	t.Helper()
	script, err := os.ReadFile("lifecycle_systemtest.sh")
	if err != nil {
		t.Fatal(err)
	}
	mainCall := regexp.MustCompile(`(?m)^main "\$@"\s*$`)
	if !mainCall.Match(script) {
		t.Fatal(`lifecycle_systemtest.sh no longer ends with main "$@"`)
	}
	reconcile, err := os.ReadFile("reconcile_failure.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "lifecycle_systemtest.sh")
	if err := os.WriteFile(path, mainCall.ReplaceAll(script, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reconcile_failure.sh"), reconcile, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// laneStep cuts one step out of a lane function: from the line starting with
// the start comment up to the line starting with the end comment.
func laneStep(t *testing.T, start, end string) string {
	t.Helper()
	script, err := os.ReadFile("lifecycle_systemtest.sh")
	if err != nil {
		t.Fatal(err)
	}
	step := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(start) + `.*?^` + regexp.QuoteMeta(end)).Find(script)
	if step == nil {
		t.Fatalf("no lane step from %q to %q in lifecycle_systemtest.sh", start, end)
	}
	return strings.TrimSuffix(string(step), end)
}

type laneRun struct {
	output     string
	results    []string
	reconciles int
	calls      string
}

func requireBashAndJq(t *testing.T) string {
	t.Helper()
	bash := bashOrSkip(t)
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	return bash
}

// runLane sources the script, then runs body with the fake ankra answering
// from files, fast timeouts, and RESULT_FILE collecting the PASS/FAIL lines.
func runLane(t *testing.T, files map[string]string, env map[string]string, body string) laneRun {
	t.Helper()
	bash := requireBashAndJq(t)
	dir := t.TempDir()
	fakeDir := filepath.Join(dir, "fake")
	if err := os.Mkdir(fakeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(fakeDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "ankra")
	if err := os.WriteFile(bin, []byte(fakeLaneAnkra), 0o700); err != nil {
		t.Fatal(err)
	}
	script := sourceableScript(t, dir)
	resultFile := filepath.Join(dir, "results")
	command := exec.Command(bash, "-c", `. "$1"
trap - EXIT INT TERM
`+body, "lane", script)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"ANKRA_BIN=" + bin,
		"FAKE_DIR=" + fakeDir,
		"RESULT_FILE=" + resultFile,
		"DAYTWO_TIMEOUT=2",
		"IDLE_TIMEOUT=2",
		"POLL_INTERVAL=0.1",
		"K8S_UPGRADE_TARGET=" + upgradeTarget,
		"HETZNER_LOCATION=fsn1",
	}
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	out, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("running the lane step: %v", err)
	}
	run := laneRun{output: string(out)}
	if results, err := os.ReadFile(resultFile); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(results)), "\n") {
			if line != "" {
				run.results = append(run.results, line)
			}
		}
	}
	reconciles, _ := os.ReadFile(filepath.Join(fakeDir, "reconciles"))
	run.reconciles = strings.Count(string(reconciles), "reconcile")
	calls, _ := os.ReadFile(filepath.Join(fakeDir, "calls"))
	run.calls = string(calls)
	return run
}

// result is the one recorded line for a step, or "" when there is none.
func (run laneRun) result(t *testing.T, step string) string {
	t.Helper()
	var found []string
	for _, line := range run.results {
		if strings.Contains(line, step) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one result line for %q, got %d: %q\n--- output ---\n%s", step, len(found), found, run.output)
	}
	return found[0]
}

func (run laneRun) wantResult(t *testing.T, step, outcome string, mustContain ...string) {
	t.Helper()
	line := run.result(t, step)
	if !strings.HasPrefix(line, outcome+"  ") {
		t.Errorf("%s: got %q, want %s\n--- output ---\n%s", step, line, outcome, run.output)
		return
	}
	for _, want := range mustContain {
		if !strings.Contains(line, want) {
			t.Errorf("%s: %q lacks %q\n--- output ---\n%s", step, line, want, run.output)
		}
	}
}

// The Hetzner lane's steps 6 (k8s upgrade) and 7 (instance resize).
func hetznerUpgradeAndResize(t *testing.T) string {
	step := laneStep(t, "  # 6. K8s upgrade", "  # 7b. Node-group autoscaling")
	return `replay() {
  local provider=hetzner distribution=k3s name=` + hetznerClusterName + ` label=hetzner/k3s id=` + hetznerClusterID + ` target="" plan="" out=""
` + step + `
}
replay
`
}

func hetznerFiles(t *testing.T, upgradeOperation, resizeOperation, nodes string) map[string]string {
	return map[string]string{
		"cluster-list":       clusterList(hetznerClusterName, upgradeTarget),
		"node-group-list":    "default  type=cpx32  count=1\n",
		"upgrade-answer":     upgradeAnswer("hetzner", hetznerClusterID, hetznerUpgradeOperation),
		"resize-answer":      resizeAnswerSubmitted,
		"resize-answer-wait": resizeAnswerWait(hetznerResizeOperation),
		"operation-" + hetznerUpgradeOperation + ".json": upgradeOperation,
		"operation-" + hetznerResizeOperation + ".json":  resizeOperation,
		"nodes.json": nodes,
	}
}

// TestParkedUpgradeAndResizeFailTheirSteps replays the 2026-11-09 Hetzner
// lane: the upgrade (1efa093c) and the resize (a2caca6b) both ended failed,
// parked on PodDisruptionBudgets, while the listings already showed the
// requested v1.37.2+k3s1 and cpx32 and the nodes never changed. Both steps
// must FAIL, in the platform's words; on master both PASSed.
func TestParkedUpgradeAndResizeFailTheirSteps(t *testing.T) {
	files := hetznerFiles(t,
		testdataFile(t, "operation-1efa093c-hetzner-upgrade.json"),
		testdataFile(t, "operation-a2caca6b-hetzner-resize.json"),
		hetznerNodes(t, createdVersion, "cpx22"))
	run := runLane(t, files, nil, hetznerUpgradeAndResize(t))
	run.wantResult(t, "hetzner/k3s k8s upgrade", "FAIL",
		hetznerUpgradeOperation, "ended failed", "blocked by a PodDisruptionBudget")
	run.wantResult(t, "hetzner/k3s instance resize", "FAIL",
		hetznerResizeOperation, "ended failed", "from cpx22 to cpx32 is parked")
	if run.reconciles != 0 {
		t.Fatalf("the steps started %d reconciles, want none", run.reconciles)
	}
}

// TestParkedAwsUpgradeFailsItsStep replays the AWS lane's step 6b: the
// upgrade be3b5487 ended failed after 7 minutes while `cluster list` showed
// the target at once. It must FAIL with the operation's error; on master it
// PASSed "(cp+worker Ready)" one second after the submit.
func TestParkedAwsUpgradeFailsItsStep(t *testing.T) {
	step := laneStep(t, "  # 6b. Kubernetes upgrade", "  # 6c. Node-group autoscaling")
	files := map[string]string{
		"cluster-list":   clusterList(awsClusterName, upgradeTarget),
		"upgrade-answer": upgradeAnswer("aws", awsClusterID, awsUpgradeOperation),
		"operation-" + awsUpgradeOperation + ".json": testdataFile(t, "operation-be3b5487-aws-upgrade.json"),
		"nodes.json": nodesJSON(t,
			fakeNode{name: "ip-10-0-13-201.eu-north-1.compute.internal", kubelet: createdVersion, instanceType: "t3.medium", controlPlane: true},
			fakeNode{name: "ip-10-0-9-52.eu-north-1.compute.internal", kubelet: createdVersion, instanceType: "t3.medium", group: "default"}),
	}
	// The table the old step counted "Ready" rows in: both nodes were Ready,
	// on the version they were created with.
	files["nodes-table"] = "│ NAME │ STATUS │ ROLES │ VERSION │\n" +
		"│ ip-10-0-13-201.eu-north-1.compute.internal │ Ready │ control-plane,master │ " + createdVersion + " │\n" +
		"│ ip-10-0-9-52.eu-north-1.compute.internal │ Ready │ <none> │ " + createdVersion + " │\n"
	run := runLane(t, files, nil, `replay() {
  local distribution=k3s name=`+awsClusterName+` label=aws/k3s id=`+awsClusterID+`
`+step+`
}
replay
`)
	run.wantResult(t, "aws/k3s k8s upgrade", "FAIL",
		awsUpgradeOperation, "ended failed", "ip-10-0-9-52.eu-north-1.compute.internal is blocked by a PodDisruptionBudget")
}

// TestUpgradeAndResizePassOnlyOnWhatTheNodesShow: an operation that ended in
// success is not enough on its own - the kubelets must run the target and
// the group's nodes must carry the new type. The shapes after the operation
// succeeded: everything landed (PASS), the kubelets stayed behind or the
// worker kept its old type (FAIL), the nodes carry no instance-type label
// (UNASSERTED for the resize: its type cannot be read). On master every one
// of them PASSed on the listings.
func TestUpgradeAndResizePassOnlyOnWhatTheNodesShow(t *testing.T) {
	succeeded := func(id string) string { return operationJSON(id, "success", "") }
	cases := []struct {
		name           string
		nodes          string
		upgradeOutcome string
		upgradeWant    []string
		resizeOutcome  string
		resizeWant     []string
	}{
		{
			name:           "both landed",
			nodes:          hetznerNodes(t, upgradeTarget, "cpx32"),
			upgradeOutcome: "PASS", upgradeWant: []string{hetznerUpgradeOperation + " succeeded", "2 kubelets at 1.37.2"},
			resizeOutcome: "PASS", resizeWant: []string{hetznerResizeOperation + " succeeded", "every default node is cpx32"},
		},
		{
			name:           "operations succeeded, kubelets and type still old",
			nodes:          hetznerNodes(t, createdVersion, "cpx22"),
			upgradeOutcome: "FAIL", upgradeWant: []string{"succeeded, but the kubelets did not all reach it", "=" + createdVersion},
			resizeOutcome: "FAIL", resizeWant: []string{"succeeded, but the default nodes are not all cpx32", "default-0=cpx22"},
		},
		{
			name: "a node NotReady after the upgrade",
			nodes: nodesJSON(t,
				fakeNode{name: "cp-0", kubelet: upgradeTarget, instanceType: "cpx32", controlPlane: true},
				fakeNode{name: "default-0", kubelet: upgradeTarget, instanceType: "cpx32", group: "default", notReady: true}),
			upgradeOutcome: "FAIL", upgradeWant: []string{"default-0=" + upgradeTarget + " NotReady"},
			resizeOutcome: "FAIL", resizeWant: []string{"default-0=cpx32 NotReady"},
		},
		{
			name: "no instance-type label to read",
			nodes: nodesJSON(t,
				fakeNode{name: "cp-0", kubelet: upgradeTarget, controlPlane: true},
				fakeNode{name: "default-0", kubelet: upgradeTarget, group: "default"}),
			upgradeOutcome: "PASS", upgradeWant: []string{"2 kubelets at 1.37.2"},
			resizeOutcome: "UNASSERTED", resizeWant: []string{"no default node carries a node.kubernetes.io/instance-type label"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			files := hetznerFiles(t, succeeded(hetznerUpgradeOperation), succeeded(hetznerResizeOperation), testCase.nodes)
			run := runLane(t, files, nil, hetznerUpgradeAndResize(t))
			run.wantResult(t, "hetzner/k3s k8s upgrade", testCase.upgradeOutcome, testCase.upgradeWant...)
			run.wantResult(t, "hetzner/k3s instance resize", testCase.resizeOutcome, testCase.resizeWant...)
			if !strings.Contains(run.calls, "node-group upgrade "+hetznerClusterID+" default cpx32 --wait") {
				t.Fatalf("the resize must be submitted with --wait so its answer names the operation; calls:\n%s", run.calls)
			}
		})
	}
}

// TestManagedUpgradePassesOnlyOnItsOperation: the managed upgrade's answer
// names no operation, and `cluster list` shows the requested version at
// once. The step finds the managed_k8s_upgrade operation that appeared after
// the write and passes only when it ended in success; on master a failed one
// PASSed on the listing.
func TestManagedUpgradePassesOnlyOnItsOperation(t *testing.T) {
	step := laneStep(t, "  # 5. K8s upgrade (explicit target only", "  # 6. Delete")
	const target = "1.33.1-do.0"
	refusal := "doks API 422: kubernetes version 1.33.1-do.0 is not an available upgrade for this cluster"
	cases := []struct {
		name      string
		operation string
		outcome   string
		want      []string
	}{
		{"operation failed", operationJSON(managedUpgradeOperation, "failed", refusal), "FAIL",
			[]string{managedUpgradeOperation, "ended failed", "is not an available upgrade"}},
		{"operation succeeded", operationJSON(managedUpgradeOperation, "success", ""), "PASS",
			[]string{"operation " + managedUpgradeOperation + " succeeded"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			files := map[string]string{
				"cluster-list":           clusterList(managedClusterName, target),
				"operations.json":        `[{"id":"11111111-2222-4333-8444-555555555555","name":"reconcile","display_name":"Reconcile 3 resources","status":"success"}]`,
				"managed-upgrade-answer": "Managed cluster " + managedClusterID + " upgraded to Kubernetes " + target + "\n",
				"operations-after-write.json": `[{"id":"` + managedUpgradeOperation + `","name":"managed_k8s_upgrade","display_name":"Upgrade managed Kubernetes","status":"running"},` +
					`{"id":"11111111-2222-4333-8444-555555555555","name":"reconcile","display_name":"Reconcile 3 resources","status":"success"}]`,
				"operation-" + managedUpgradeOperation + ".json": testCase.operation,
				"nodes.json": nodesJSON(t, fakeNode{name: "workers-abc", kubelet: "v1.32.5"}),
			}
			run := runLane(t, files, map[string]string{"MANAGED_UPGRADE_K8S_VERSION_DOKS": target}, `replay() {
  local provider=doks name=`+managedClusterName+` label=managed/doks id=`+managedClusterID+` target=""
`+step+`
}
replay
`)
			run.wantResult(t, "managed/doks k8s upgrade", testCase.outcome, testCase.want...)
		})
	}
}

// TestDeprovisionWaitNeverNudgesAReconcile replays the AWS lane's
// deprovision wait: the upgrade be3b5487 had failed, and the deprovision's
// own reconcile (faa71572) was running. The old wait read the failed upgrade
// as a "transient" teardown failure and started a reconcile on every poll -
// 7 on the AWS cluster being torn down at 22:46-22:48Z, 5 on the Hetzner one.
// No poll of a teardown may start one; the operations list after the run
// (the failure resolved by the deprovision reconcile) neither.
func TestDeprovisionWaitNeverNudgesAReconcile(t *testing.T) {
	cases := []struct {
		name       string
		operations string
	}{
		// What the wait read at 22:46:10: the deprovision reconcile running,
		// the failed upgrade behind it (its attention not yet resolved).
		{"during the deprovision", testdataFile(t, "operations-aws-during-deprovision.json")},
		// The listing as it stands after the run: be3b5487 failed and
		// resolved by faa71572, every later reconcile a success.
		{"after the run", testdataFile(t, "operations-aws-after-run.json")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			files := map[string]string{
				"cluster-list":      clusterList(awsClusterName, upgradeTarget),
				"cluster-list-gone": clusterList("some-other-cluster", upgradeTarget),
				"present-for":       "8",
				"operations.json":   testCase.operations,
				"steps.json":        testdataFile(t, "operation-be3b5487-aws-upgrade.json"),
			}
			run := runLane(t, files, nil, `wait_for_removed `+awsClusterName+` 30; echo "rc=$?"`)
			if !strings.Contains(run.output, "rc=0") {
				t.Fatalf("the wait did not end with the cluster gone:\n%s", run.output)
			}
			if run.reconciles != 0 {
				t.Fatalf("the deprovision wait started %d reconciles, want none:\n%s", run.reconciles, run.output)
			}
		})
	}
}

// TestOperationIDIsReadFromTheWritesAnswer: the answers the step follows,
// as the CLI prints them. The "Follow progress" hint alone names the
// operation after "operations list", which is not an operation line.
func TestOperationIDIsReadFromTheWritesAnswer(t *testing.T) {
	cases := []struct {
		name, answer, want string
	}{
		{"cluster upgrade", upgradeAnswer("aws", awsClusterID, awsUpgradeOperation), awsUpgradeOperation},
		{"node-group upgrade --wait", resizeAnswerWait(hetznerResizeOperation), hetznerResizeOperation},
		{"stop", "AWS cluster stop initiated.\n  Cluster ID: " + awsClusterID + "\n  Operation ID: " + strings.ToUpper(awsUpgradeOperation) + "\n", awsUpgradeOperation},
		{"node-group upgrade without --wait", resizeAnswerSubmitted, ""},
		{"managed upgrade", "Managed cluster " + managedClusterID + " upgraded to Kubernetes 1.33.1-do.0\n", ""},
		{"a hint is not an operation line", "Follow it with:\n  ankra cluster operations list " + awsUpgradeOperation + "\n", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			bash := requireBashAndJq(t)
			dir := t.TempDir()
			script := sourceableScript(t, dir)
			command := exec.Command(bash, "-c", `. "$1"; trap - EXIT INT TERM; printf 'id=%s\n' "$(operation_id_in "$2")"`, "lane", script, testCase.answer)
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "ANKRA_BIN=/nonexistent"}
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("operation_id_in: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "id="+testCase.want {
				t.Fatalf("operation_id_in(%q) = %q, want id=%s", testCase.answer, got, testCase.want)
			}
		})
	}
}
