package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var stackProfileDraftsCmd = &cobra.Command{
	Use:   "drafts",
	Short: "Open, edit, and publish stack profile builder drafts",
	Long: `Work with stack profile builder drafts from the terminal.

A draft is the editable working copy behind a profile version: open one from
an existing profile (or from a deployed stack), annotate its parameters with
the titles and descriptions the launch form shows as guidance, and publish it
as the next version.`,
}

var stackProfileDraftsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's open builder drafts",
	RunE: func(cmd *cobra.Command, args []string) error {
		drafts, err := apiClient.ListStackProfileDrafts()
		if err != nil {
			return fmt.Errorf("listing stack profile drafts: %w", err)
		}
		if handled, err := renderStructured(cmd, drafts); err != nil {
			return err
		} else if handled {
			return nil
		}
		if len(drafts) == 0 {
			fmt.Println("No open stack profile drafts.")
			return nil
		}
		for _, draft := range drafts {
			target := "new profile"
			if draft.ProfileID != nil {
				target = "edits profile " + *draft.ProfileID
			}
			fmt.Printf("%s  %s\n", text.Bold.Sprint(draft.Name), draft.ID)
			fmt.Printf("  %s   updated %s\n", target, draft.UpdatedAt)
		}
		return nil
	},
}

var stackProfileDraftsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Open a builder draft",
	Long: `Open a builder draft: --profile edits an existing profile (publishing
creates its next version), --name starts a brand-new profile, and
--source-cluster with --source-stack seeds the draft from a deployed stack.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		profileReference, _ := cmd.Flags().GetString("profile")
		sourceCluster, _ := cmd.Flags().GetString("source-cluster")
		sourceStack, _ := cmd.Flags().GetString("source-stack")
		if name == "" && profileReference == "" && sourceCluster == "" {
			return withExitCode(exitUsage, errors.New("one of --name, --profile or --source-cluster is required"))
		}

		request := client.CreateStackProfileDraftRequest{Name: name}
		if profileReference != "" {
			profileID, err := resolveStackProfileID(apiClient, profileReference)
			if err != nil {
				return err
			}
			request.ProfileID = profileID
		}
		if sourceCluster != "" {
			if sourceStack == "" {
				return withExitCode(exitUsage, errors.New("--source-stack is required with --source-cluster"))
			}
			clusterID, err := resolveClusterID(sourceCluster)
			if err != nil {
				return err
			}
			request.SourceClusterID = clusterID
			request.SourceStackName = sourceStack
		}

		draft, err := apiClient.CreateStackProfileDraft(request)
		if err != nil {
			return fmt.Errorf("creating stack profile draft: %w", err)
		}
		if handled, err := renderStructured(cmd, draft); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Draft '%s' opened.\n", draft.Name)
		fmt.Printf("  Draft ID: %s\n", draft.ID)
		fmt.Printf("  Parameters detected: %d\n", len(draft.Parameters))
		fmt.Printf("\nAnnotate the launch form's guidance with:\n  ankra stack-profiles drafts annotate %s --parameter <name> --description \"...\"\n", draft.ID)
		return nil
	},
}

var stackProfileDraftsGetCmd = &cobra.Command{
	Use:   "get <draft-id>",
	Short: "Show a draft's parameters and their annotations",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		draft, err := apiClient.GetStackProfileDraft(args[0])
		if err != nil {
			return fmt.Errorf("reading stack profile draft: %w", err)
		}
		if handled, err := renderStructured(cmd, draft); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("%s  (version %d)\n", text.Bold.Sprint(draft.Name), draft.Version)
		for _, parameter := range draft.Parameters {
			name, _ := parameter["name"].(string)
			parameterType, _ := parameter["type"].(string)
			description, _ := parameter["description"].(string)
			if description == "" {
				description = text.Faint.Sprint("(no description yet)")
			}
			fmt.Printf("  %s  [%s]\n    %s\n", text.Bold.Sprint(name), parameterType, description)
		}
		return nil
	},
}

var stackProfileDraftsAnnotateCmd = &cobra.Command{
	Use:   "annotate <draft-id>",
	Short: "Set the guidance a parameter shows in the launch form",
	Long: `Set a parameter's title and description on a draft. The description is
the guidance the launch form shows under the field, so this is how a profile
author instructs the person filling it in. Publishing the draft makes the
annotations live.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		parameterName, _ := cmd.Flags().GetString("parameter")
		description, _ := cmd.Flags().GetString("description")
		title, _ := cmd.Flags().GetString("title")
		if description == "" && title == "" {
			return withExitCode(exitUsage, errors.New("provide --description and/or --title"))
		}

		draft, err := apiClient.GetStackProfileDraft(args[0])
		if err != nil {
			return fmt.Errorf("reading stack profile draft: %w", err)
		}
		found := false
		for _, parameter := range draft.Parameters {
			if name, _ := parameter["name"].(string); name == parameterName {
				if description != "" {
					parameter["description"] = description
				}
				if title != "" {
					parameter["title"] = title
				}
				found = true
				break
			}
		}
		if !found {
			names := make([]string, 0, len(draft.Parameters))
			for _, parameter := range draft.Parameters {
				if name, _ := parameter["name"].(string); name != "" {
					names = append(names, name)
				}
			}
			return fmt.Errorf("parameter %q not found on the draft; it has: %v", parameterName, names)
		}

		updated, err := apiClient.UpdateStackProfileDraft(draft.ID, client.UpdateStackProfileDraftRequest{
			Spec:       draft.Spec,
			Parameters: draft.Parameters,
			Version:    draft.Version,
		})
		if err != nil {
			return fmt.Errorf("annotating stack profile draft: %w", err)
		}
		if handled, err := renderStructured(cmd, updated); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Annotated %s on draft '%s'.\n", parameterName, updated.Name)
		fmt.Println("Publish to make it live:")
		fmt.Printf("  ankra stack-profiles drafts publish %s --changelog \"...\"\n", updated.ID)
		return nil
	},
}

var stackProfileDraftsPublishCmd = &cobra.Command{
	Use:   "publish <draft-id>",
	Short: "Publish a draft as a profile version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		channel, _ := cmd.Flags().GetString("channel")
		changelog, _ := cmd.Flags().GetString("changelog")
		visibility, _ := cmd.Flags().GetString("visibility")

		result, err := apiClient.PublishStackProfileDraft(args[0], client.PublishStackProfileDraftRequest{
			Channel: channel, Changelog: changelog, Visibility: visibility,
		})
		if err != nil {
			return fmt.Errorf("publishing stack profile draft: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		profileName, _ := result.Profile["name"].(string)
		versionNumber := result.Version["version"]
		fmt.Printf("Published '%s' version %v.\n", profileName, versionNumber)
		return nil
	},
}

var stackProfileDraftsDeleteCmd = &cobra.Command{
	Use:   "delete <draft-id>",
	Short: "Discard a builder draft",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.DeleteStackProfileDraft(args[0])
		if err != nil {
			return fmt.Errorf("deleting stack profile draft: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Println("Draft deleted.")
		return nil
	},
}

var stackProfileDraftsValidateCmd = &cobra.Command{
	Use:   "validate <draft-id>",
	Short: "Run the publish validations on a draft without publishing",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := apiClient.ValidateStackProfileDraft(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("validating stack profile draft: %w", err)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfileDraftsRebaseCmd = &cobra.Command{
	Use:   "rebase <draft-id>",
	Short: "Move a stale draft's base to the profile's latest version",
	Long: `Move a draft's base to the profile's latest published version. A draft
opened before someone else published cannot be published until it is
rebased; the draft's contents are kept and the upstream changes reported.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := apiClient.RebaseStackProfileDraft(cmd.Context(), args[0],
			client.RebaseStackProfileDraftRequest{Strategy: "acknowledge"})
		if err != nil {
			return fmt.Errorf("rebasing stack profile draft: %w", err)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfileDraftsSubmitSuggestionCmd = &cobra.Command{
	Use:   "submit-suggestion <draft-id>",
	Short: "Submit a draft as a suggestion to another organisation's profile",
	Long: `Submit a draft that edits another organisation's public profile as a
suggestion. The owning organisation reviews it with
'ankra stack-profiles suggestions'; submitting retires the draft.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		if strings.TrimSpace(title) == "" {
			return withExitCode(exitUsage, errors.New("--title is required: one line describing the change"))
		}
		payload, err := apiClient.SubmitStackProfileSuggestion(cmd.Context(), args[0], strings.TrimSpace(title))
		if err != nil {
			return fmt.Errorf("submitting stack profile suggestion: %w", err)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

func init() {
	stackProfileDraftsCreateCmd.Flags().String("name", "", "Name for a brand-new profile draft")
	stackProfileDraftsCreateCmd.Flags().String("profile", "", "Existing profile (name or ID) to open a draft on")
	stackProfileDraftsCreateCmd.Flags().String("source-cluster", "", "Cluster (name or ID) to seed the draft from a deployed stack")
	stackProfileDraftsCreateCmd.Flags().String("source-stack", "", "Deployed stack name to seed the draft from (with --source-cluster)")

	stackProfileDraftsAnnotateCmd.Flags().String("parameter", "", "Parameter name to annotate")
	stackProfileDraftsAnnotateCmd.Flags().String("description", "", "Guidance shown under the field in the launch form")
	stackProfileDraftsAnnotateCmd.Flags().String("title", "", "Display title for the field (optional)")
	_ = stackProfileDraftsAnnotateCmd.MarkFlagRequired("parameter")

	stackProfileDraftsPublishCmd.Flags().String("channel", "stable", "Release channel for the version")
	stackProfileDraftsPublishCmd.Flags().String("changelog", "", "Changelog entry for the version")
	stackProfileDraftsPublishCmd.Flags().String("visibility", "", "Profile visibility applied at publish (organisation or public)")

	stackProfileDraftsSubmitSuggestionCmd.Flags().String("title", "", "One line describing the change (required)")

	stackProfileDraftsCmd.AddCommand(stackProfileDraftsListCmd, stackProfileDraftsCreateCmd,
		stackProfileDraftsGetCmd, stackProfileDraftsAnnotateCmd,
		stackProfileDraftsPublishCmd, stackProfileDraftsDeleteCmd,
		stackProfileDraftsValidateCmd, stackProfileDraftsRebaseCmd,
		stackProfileDraftsSubmitSuggestionCmd)
	stackProfilesCmd.AddCommand(stackProfileDraftsCmd)

	registerStructuredOutputFlags(stackProfileDraftsListCmd, stackProfileDraftsCreateCmd,
		stackProfileDraftsGetCmd, stackProfileDraftsAnnotateCmd,
		stackProfileDraftsPublishCmd, stackProfileDraftsDeleteCmd,
		stackProfileDraftsValidateCmd, stackProfileDraftsRebaseCmd,
		stackProfileDraftsSubmitSuggestionCmd)
}
