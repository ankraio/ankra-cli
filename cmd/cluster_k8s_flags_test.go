package cmd

import "testing"

// The kubectl-shaped shorthand contract of the `cluster get` family: -A
// means --all-namespaces on every namespaced listing, the generic resources
// command included (locked in after the ankra-sjf1u dogfood report).
func TestClusterGetAllNamespacesShorthandIsConsistent(t *testing.T) {
	podsFlag := clusterPodsCmd.Flags().Lookup("all-namespaces")
	if podsFlag == nil || podsFlag.Shorthand != "A" {
		t.Fatalf("cluster get pods must offer -A for --all-namespaces, got %+v", podsFlag)
	}
	resourcesFlag := clusterGenericResourcesCmd.Flags().Lookup("all-namespaces")
	if resourcesFlag == nil || resourcesFlag.Shorthand != "A" {
		t.Fatalf("cluster get resources must offer -A for --all-namespaces, got %+v", resourcesFlag)
	}
	for _, configuration := range kindConfigs {
		if configuration.clusterScoped {
			continue
		}
		kindCommand := registerKindCommand(configuration)
		kindFlag := kindCommand.Flags().Lookup("all-namespaces")
		if kindFlag == nil || kindFlag.Shorthand != "A" {
			t.Errorf("cluster get %s must offer -A for --all-namespaces, got %+v",
				configuration.commandName, kindFlag)
		}
	}
}
