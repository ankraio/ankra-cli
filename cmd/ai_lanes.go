package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var aiLanesCmd = &cobra.Command{
	Use:   "lanes",
	Short: "Choose the model each AI function runs on",
	Long: `Choose the model each AI function runs on.

A lane is one AI function that runs outside chat: deploy analysis,
troubleshooting, CI/CD generation, stack README generation and AI code
review. A lane with no selection follows its default tier in the model
catalog ('ankra ai models list'), so it moves when the catalog does.`,
}

var aiLanesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List each AI lane with the model it runs on",
	RunE: func(cmd *cobra.Command, args []string) error {
		lanes, listError := apiClient.ListAILaneModels()
		if listError != nil {
			return fmt.Errorf("listing AI lanes: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, lanes); rendered || renderError != nil {
			return renderError
		}
		renderAILanes(lanes)
		return nil
	},
}

var aiLanesSetCmd = &cobra.Command{
	Use:   "set <lane> <model>",
	Short: "Run a lane on a catalog model or an exact OpenRouter model",
	Long: `Run a lane on a catalog model or an exact OpenRouter model.

<model> is a catalog key from 'ankra ai models list' (for example think or
expert) or an exact OpenRouter model id (for example z-ai/glm-5.2). Changing a
lane requires organisation admin.`,
	Example: `  ankra ai lanes set pr_review think
  ankra ai lanes set stack_description z-ai/glm-5.2`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return applyAILaneModel(cmd, args[0], args[1])
	},
}

var aiLanesClearCmd = &cobra.Command{
	Use:   "clear <lane>",
	Short: "Return a lane to its default tier",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return applyAILaneModel(cmd, args[0], "")
	},
}

// applyAILaneModel stores one lane's model - an empty modelKey clears it -
// and reports what the lane runs on afterwards. An answer that does not list
// the lane is an error rather than a success: nothing confirmed the change.
func applyAILaneModel(cmd *cobra.Command, lane string, modelKey string) error {
	lanes, setError := apiClient.SetAILaneModel(lane, modelKey)
	if setError != nil {
		return fmt.Errorf("setting the %s lane model: %w", lane, setError)
	}
	if rendered, renderError := renderStructured(cmd, lanes); rendered || renderError != nil {
		return renderError
	}
	for _, laneModel := range lanes {
		if laneModel.Lane != lane {
			continue
		}
		if modelKey == "" {
			fmt.Printf("Lane %s follows its %s tier again and runs on %s.\n",
				text.FgGreen.Sprint(lane), laneModel.DefaultTier, laneModel.EffectiveModelID)
			return nil
		}
		fmt.Printf("Lane %s now runs on %s.\n", text.FgGreen.Sprint(lane), laneModel.EffectiveModelID)
		return nil
	}
	return fmt.Errorf("the platform accepted the change but its answer did not include lane %s, "+
		"so what it runs on now is unconfirmed; run 'ankra ai lanes list' to check", lane)
}

// renderAILanes prints the lanes table. A stale selection is labelled so the
// default the lane fell back to is not read as the organisation's choice.
func renderAILanes(lanes []client.AILaneModel) {
	if len(lanes) == 0 {
		fmt.Println("No AI lanes are registered.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(os.Stdout)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Lane", "Function", "Selected", "Runs On", "Default Tier"})
	for _, laneModel := range lanes {
		selected := laneModel.SelectedModelKey
		switch {
		case selected == "":
			selected = "(default tier)"
		case laneModel.IsSelectionStale:
			selected += " (stale, running on the default)"
		}
		writer.AppendRow(table.Row{laneModel.Lane, laneModel.DisplayName, selected,
			laneModel.EffectiveModelID, laneModel.DefaultTier})
	}
	writer.Render()
}

func init() {
	registerStructuredOutputFlags(aiLanesListCmd, aiLanesSetCmd, aiLanesClearCmd)
	aiLanesCmd.AddCommand(aiLanesListCmd, aiLanesSetCmd, aiLanesClearCmd)
	aiCmd.AddCommand(aiLanesCmd)
}
