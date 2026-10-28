package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type securityClusterMock struct {
	baseMock
	stacks           *client.SecurityClusterStackList
	stacksCluster    string
	posture          *client.SecurityStackPosture
	postureStack     string
	workloads        *client.SecurityStackWorkloadList
	pod              *client.SecurityPodPosture
	podNamespace     string
	podName          string
	podClusterCalled string
}

func (m *securityClusterMock) ListClusterSecurityStacks(clusterID string) (*client.SecurityClusterStackList, error) {
	m.stacksCluster = clusterID
	return m.stacks, nil
}

func (m *securityClusterMock) GetStackSecurity(clusterID string, stackName string) (*client.SecurityStackPosture, error) {
	m.postureStack = clusterID + "/" + stackName
	return m.posture, nil
}

func (m *securityClusterMock) ListStackSecurityWorkloads(string, string) (*client.SecurityStackWorkloadList, error) {
	return m.workloads, nil
}

func (m *securityClusterMock) GetPodSecurity(clusterID string, namespace string, podName string) (*client.SecurityPodPosture, error) {
	m.podClusterCalled, m.podNamespace, m.podName = clusterID, namespace, podName
	return m.pod, nil
}

const securityClusterID = "2c4e9f2a-6f10-4a45-9d3e-2c6f2a3b4c5d"

func securityStackFindings() client.SecurityStackFindings {
	lastScan := "2026-09-04T09:00:00Z"
	return client.SecurityStackFindings{
		Observed:          client.SecuritySeverityCounts{Critical: 4},
		Actionable:        client.SecuritySeverityCounts{Critical: 3},
		Acknowledged:      client.SecuritySeverityCounts{Critical: 1},
		Findings:          1,
		FixableSevere:     1,
		KnownExploited:    2,
		AffectedImages:    2,
		AffectedWorkloads: 2,
		LastScan:          &lastScan,
	}
}

func securityIntelligenceSynced() client.SecurityIntelligenceStatus {
	synced := "2026-09-04T10:00:00Z"
	return client.SecurityIntelligenceStatus{KevSyncedAt: &synced, EPSSSyncedAt: &synced, KevListed: 1300}
}

func TestSecurityStacksRendersEveryStackAndTheOutsideRow(t *testing.T) {
	mock := &securityClusterMock{stacks: &client.SecurityClusterStackList{
		ClusterID:    securityClusterID,
		Scanner:      client.SecurityScanner{Status: "fresh"},
		Intelligence: securityIntelligenceSynced(),
		Coverage:     sbomCoverage(),
		Stacks: []client.SecurityClusterStackSummary{
			{
				StackName: "observability", Status: "connected",
				Scope:    client.SecurityStackScope{Addons: 1, Manifests: 1, MatchedWorkloads: 3},
				Findings: securityStackFindings(), Containers: 3, ContainersWithSBOM: 2, Pods: 2,
			},
			{
				StackName: "ingress", Status: "unmatched",
				Scope: client.SecurityStackScope{Addons: 1, UnmatchedMembers: 1},
			},
		},
		Outside: client.SecurityClusterStackOutside{Workloads: 4, Actionable: client.SecuritySeverityCounts{High: 5}, KnownExploited: 1, Containers: 6, ContainersWithSBOM: 1, Pods: 4},
	}}
	output, executeError := runSecurityCommand(t, mock, "security", "stacks", "--cluster", securityClusterID)
	if executeError != nil {
		t.Fatalf("security stacks failed: %v", executeError)
	}
	if mock.stacksCluster != securityClusterID {
		t.Fatalf("cluster id = %q", mock.stacksCluster)
	}
	for _, expected := range []string{"observability", "scanned", "1 add-ons, 1 manifests, 3 workloads", "3 critical, 0 high (1 fixable)", "2 / 3",
		"ingress", "unmatched", "1 unmatched", "(outside any stack)", "4 workloads", "0 critical, 5 high", "1 / 6", "1 of 3 scanned clusters publish one"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestSecurityStacksRefusesAMissingCluster(t *testing.T) {
	mock := &securityClusterMock{stacks: &client.SecurityClusterStackList{}}
	if _, executeError := runSecurityCommand(t, mock, "security", "stacks"); executeError == nil {
		t.Fatal("expected a missing --cluster to be refused")
	}
	if mock.stacksCluster != "" {
		t.Fatalf("read happened without a cluster: %q", mock.stacksCluster)
	}
}

func TestSecurityStackRendersMembersAndTheirWorkloads(t *testing.T) {
	namespace := "monitoring"
	mock := &securityClusterMock{
		posture: &client.SecurityStackPosture{
			Status: "connected", ClusterID: securityClusterID, StackName: "observability",
			Scanner: client.SecurityScanner{Status: "fresh"}, Intelligence: securityIntelligenceSynced(),
			Scope:    client.SecurityStackScope{Addons: 1, Manifests: 1, DeclaredObjects: 1, MatchedWorkloads: 3},
			Findings: securityStackFindings(),
			KnownExploited: []client.SecurityRemediationCandidate{{
				FindingID: "f1", CVEID: "CVE-2025-24813", Severity: "CRITICAL", PackageName: "tomcat-embed-core", ActionableCount: 2, AffectedWorkloads: 1,
			}},
			SBOM: client.SecurityStackSBOM{Containers: 3, ContainersWithSBOM: 2, Pods: 2, Images: 2, Components: 120, Coverage: sbomCoverage()},
			Members: []client.SecurityStackMember{
				{Name: "grafana", Kind: "addon", Namespace: &namespace, Workloads: 2, Actionable: client.SecuritySeverityCounts{Critical: 3}, KnownExploited: 2, Containers: 3, ContainersWithSBOM: 2},
				{Name: "redis-manifest", Kind: "manifest", Workloads: 0},
			},
		},
		workloads: &client.SecurityStackWorkloadList{Result: []client.SecurityStackWorkload{
			{MemberName: "grafana", Kind: "Deployment", Namespace: "monitoring", Name: "grafana", Pods: 2, Scanned: true, Actionable: client.SecuritySeverityCounts{Critical: 2}, KnownExploited: 2, Containers: 3, ContainersWithSBOM: 2},
			{MemberName: "grafana", Kind: "StatefulSet", Namespace: "monitoring", Name: "grafana-db", Pods: 1, Scanned: false, Containers: 1},
		}},
	}
	output, executeError := runSecurityCommand(t, mock, "security", "stack", "observability", "--cluster", securityClusterID)
	if executeError != nil {
		t.Fatalf("security stack failed: %v", executeError)
	}
	if mock.postureStack != securityClusterID+"/observability" {
		t.Fatalf("posture read = %q", mock.postureStack)
	}
	for _, expected := range []string{"Stack observability", "CVE-2025-24813", "grafana", "redis-manifest", "unmatched",
		"Deployment grafana", "StatefulSet grafana-db", "not scanned", "2 critical, 0 high", "ankra security pods --cluster"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestSecurityPodRendersEveryContainerNeverAsCleanWhenUnscanned(t *testing.T) {
	workloadKind, workloadName, node, phase := "deployment", "grafana", "worker-3", "Running"
	components := 12
	mock := &securityClusterMock{pod: &client.SecurityPodPosture{
		Status: "connected", ClusterID: securityClusterID, Namespace: "security", PodName: "grafana-abc",
		Node: &node, Phase: &phase, WorkloadKind: &workloadKind, WorkloadName: &workloadName,
		Scanner: client.SecurityScanner{Status: "fresh"}, Intelligence: client.SecurityIntelligenceStatus{},
		Findings: client.SecurityPodSecurityFindings{Observed: 2, Actionable: client.SecuritySeverityCounts{Critical: 2}, KnownExploited: 1, Containers: 2, ScannedContainers: 1},
		SBOM:     client.SecurityPodSecuritySBOM{Containers: 2, WithSBOM: 1, WithoutSBOM: 1, Images: 1, Components: 12},
		Containers: []client.SecurityPodSecurityContainer{
			{Name: "app", Kind: "app", Image: "registry.test/grafana:3.0.0", Ready: true, Scanned: true, Observed: 2, Actionable: client.SecuritySeverityCounts{Critical: 2}, KnownExploited: 1, SBOMStatus: "present", ComponentCount: &components},
			{Name: "sidecar", Kind: "app", Image: "registry.test/sidecar:1.0.0", Ready: false, Scanned: false, SBOMStatus: "absent"},
		},
	}}
	output, executeError := runSecurityCommand(t, mock, "security", "pod", "security", "grafana-abc", "--cluster", securityClusterID)
	if executeError != nil {
		t.Fatalf("security pod failed: %v", executeError)
	}
	if mock.podClusterCalled != securityClusterID || mock.podNamespace != "security" || mock.podName != "grafana-abc" {
		t.Fatalf("pod read = %s %s %s", mock.podClusterCalled, mock.podNamespace, mock.podName)
	}
	for _, expected := range []string{"Pod security/grafana-abc", "deployment grafana", "1 of 2 containers scanned", "app", "sidecar", "not scanned", "absent", "unknown", "ankra security sbom image"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}
