package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// deprovisionRoleReadTimeout bounds both settings reads together: the warning
// must never be what holds a deprovision up.
const deprovisionRoleReadTimeout = 15 * time.Second

// warnDeprovisionOrganisationRoles says, before a cluster is torn down, that it
// is the organisation's pipeline or AI staging cluster, and how to choose
// another one (ankra-oznf7k.6). The settings keep pointing at the deleted id
// afterwards, and nothing else says so until a pipeline has nowhere to run.
//
// It only warns. A read that fails says nothing, because a deprovision is not
// held up on a setting the caller may not be allowed to read. Notes go to
// stderr so --output json|yaml stays parseable.
func warnDeprovisionOrganisationRoles(cmd *cobra.Command, clusterID string, clusterLabel string) {
	errorOutput := cmd.ErrOrStderr()
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	readContext, cancelRead := context.WithTimeout(parent, deprovisionRoleReadTimeout)
	defer cancelRead()
	ciSettings, ciError := apiClient.GetOrganisationCISettings(readContext)
	if ciError == nil && ciSettings != nil && ciSettings.ClusterID != nil && *ciSettings.ClusterID == clusterID {
		_, _ = fmt.Fprintf(errorOutput, "Warning: cluster %s is this organisation's pipeline cluster. Pipelines stop "+
			"running on it when it goes. Choose another with: ankra org ci-settings set --cluster <name>\n", clusterLabel)
	}
	previewSettings, previewError := apiClient.GetOrganisationPreviewSettings(readContext)
	if previewError == nil && previewSettings != nil && previewSettings.StagingClusterID != nil &&
		*previewSettings.StagingClusterID == clusterID {
		_, _ = fmt.Fprintf(errorOutput, "Warning: cluster %s is this organisation's AI staging cluster. AI workspaces "+
			"and pull request previews stop running on it when it goes. Choose another with: "+
			"ankra org ai-environment set --staging-cluster <name>\n", clusterLabel)
	}
}
