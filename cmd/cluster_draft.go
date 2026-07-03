package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"
	"github.com/spf13/cobra"
)

type stackDraftOutput struct {
	StackName string `json:"stack_name" yaml:"stack_name"`
	DraftID   string `json:"draft_id,omitempty" yaml:"draft_id,omitempty"`
	NoChange  bool   `json:"no_change" yaml:"no_change"`
	Error     string `json:"error,omitempty" yaml:"error,omitempty"`
}

var clusterDraftCmd = &cobra.Command{
	Use:   "draft",
	Short: "Stage an ImportCluster YAML as reviewable drafts instead of applying it",
	Long: `Stage all changes in an ImportCluster YAML as drafts on the cluster
without deploying anything. The local checks run first (the same ones as
'ankra cluster apply --dry-run'), then each stack in the file is saved as a
resource draft you can review, edit, and deploy from the Ankra stack builder.

If the cluster does not exist yet it is imported first without its stacks,
since drafts can only be attached to an existing cluster; every stack in the
file is then staged as a draft. Stacks that already match the cluster's
desired state are reported as "no changes" rather than creating an empty
draft.`,
	Args: cobra.NoArgs,
	RunE: runClusterDraft,
}

func init() {
	clusterDraftCmd.Flags().StringP("file", "f", "", "Path to the ImportCluster YAML file to stage as drafts")
	registerStructuredOutputFlags(clusterDraftCmd)
	_ = clusterDraftCmd.MarkFlagRequired("file")
	clusterCmd.AddCommand(clusterDraftCmd)
}

func runClusterDraft(cmd *cobra.Command, _ []string) error {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return fmt.Errorf("reading --file: %w", err)
	}
	format, err := structuredFormatFromFlags(cmd)
	if err != nil {
		return err
	}
	structured := format != outputDefault

	importRequest, err := buildImportRequest(filePath)
	if err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}
	if err := validateResourceGraph(importRequest); err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clusterID, err := resolveClusterForDraft(ctx, importRequest)
	if err != nil {
		return err
	}

	var created, unchanged int
	hasErrors := false
	stackOutputs := make([]stackDraftOutput, 0, len(importRequest.Spec.Stacks))
	for _, stack := range importRequest.Spec.Stacks {
		result, draftErr := apiClient.CreateStackDraft(ctx, clusterID, stack)
		if draftErr != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "- stack %q: %s\n", stack.Name, draftErr)
			hasErrors = true
			stackOutputs = append(stackOutputs, stackDraftOutput{StackName: stack.Name, Error: draftErr.Error()})
			continue
		}
		switch {
		case len(result.Errors) > 0:
			hasErrors = true
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "- stack %q:\n", stack.Name)
			errorParts := make([]string, 0, len(result.Errors))
			for _, resourceError := range result.Errors {
				for _, detail := range resourceError.Errors {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "    • %s %q: %s - %s\n", resourceError.Kind, resourceError.Name, detail.Key, detail.Message)
					errorParts = append(errorParts, fmt.Sprintf("%s %q: %s - %s", resourceError.Kind, resourceError.Name, detail.Key, detail.Message))
				}
			}
			stackOutputs = append(stackOutputs, stackDraftOutput{StackName: stack.Name, Error: strings.Join(errorParts, "; ")})
		case result.NoChange:
			unchanged++
			stackOutputs = append(stackOutputs, stackDraftOutput{StackName: stack.Name, NoChange: true})
			if !structured {
				fmt.Printf("- stack %q: no changes\n", stack.Name)
			}
		default:
			created++
			stackOutputs = append(stackOutputs, stackDraftOutput{StackName: stack.Name, DraftID: result.DraftID})
			if !structured {
				fmt.Printf("- stack %q: draft created (%s)\n", stack.Name, result.DraftID)
			}
		}
	}

	if structured {
		if err := encodeStructured(cmd.OutOrStdout(), format, stackOutputs); err != nil {
			return err
		}
		if hasErrors {
			return errors.New("some stacks could not be staged as drafts")
		}
		return nil
	}

	if hasErrors {
		return errors.New("some stacks could not be staged as drafts")
	}

	fmt.Printf("\nStaged %d draft(s), %d stack(s) already up to date. Nothing was deployed.\n", created, unchanged)
	if created > 0 {
		fmt.Printf("Review and deploy the drafts in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), clusterID)
	}
	return nil
}

// resolveClusterForDraft returns the cluster ID to attach drafts to. If the
// cluster does not exist yet it is imported first without any stacks, because
// drafts can only be attached to an existing cluster and importing the stacks
// would deploy them.
func resolveClusterForDraft(ctx context.Context, importRequest client.CreateImportClusterRequest) (string, error) {
	existing, err := apiClient.GetCluster(importRequest.Name)
	if err == nil && existing.ID != "" {
		return existing.ID, nil
	}
	if err != nil && !strings.Contains(err.Error(), "no cluster found") {
		return "", fmt.Errorf("looking up cluster %q: %w", importRequest.Name, err)
	}

	fmt.Fprintf(os.Stderr, "Cluster %q does not exist yet; importing it first (without stacks, so they can be staged as drafts)...\n", importRequest.Name)
	// Bootstrap the cluster without stacks: importing the full request would
	// deploy every stack as live desired state, leaving nothing to draft.
	bootstrapRequest := importRequest
	bootstrapRequest.Spec.Stacks = []client.Stack{}
	importResponse, _, importErr := apiClient.ApplyCluster(ctx, bootstrapRequest, true)
	if importErr != nil {
		return "", fmt.Errorf("importing cluster %q: %w", importRequest.Name, importErr)
	}
	if len(importResponse.Errors) > 0 {
		return "", fmt.Errorf("import of cluster %q failed: %v", importRequest.Name, importResponse.Errors)
	}
	fmt.Fprintf(os.Stderr, "Cluster %q imported.\n", importResponse.Name)
	return importResponse.ClusterId, nil
}
