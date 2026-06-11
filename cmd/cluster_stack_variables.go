package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Stack variables are part of the StackSpec itself (`variables: map[str]str`)
// and travel through the same partial-stack PATCH used by manifests/addons
// upgrade. There is no separate stack-variables endpoint on the backend, so
// the CLI fetches the full stack via /iac, mutates the map, and PATCHes the
// stack back.

var clusterStackVariablesCmd = &cobra.Command{
	Use:   "variables",
	Short: "Manage stack-scoped variables",
	Long: `Manage variables on a specific stack. Stack variables are the most
specific scope and shadow cluster and organisation variables of the same name
when this stack's manifests/addons are rendered.

  ankra cluster stacks variables list <stack>
  ankra cluster stacks variables get <stack> DB_HOST
  ankra cluster stacks variables set <stack> DB_HOST db.prod.example.com
  ankra cluster stacks variables delete <stack> DB_HOST

Stack variables are stored on the stack spec itself; edits use the same
partial-stack PATCH endpoint as manifests/addons upgrade.`,
}

var clusterStackVariablesListCmd = &cobra.Command{
	Use:   "list <stack>",
	Short: "List variables on a stack",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		stack, err := fetchStackForVariables(ctx, clusterFlag, stackName)
		if err != nil {
			return err
		}
		return renderStackVariables(cmd.OutOrStdout(), stackName, stack.Variables, out)
	},
}

var clusterStackVariablesGetCmd = &cobra.Command{
	Use:   "get <stack> <name>",
	Short: "Get a single stack variable",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		name := args[1]
		clusterFlag, _ := cmd.Flags().GetString("cluster")

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		stack, err := fetchStackForVariables(ctx, clusterFlag, stackName)
		if err != nil {
			return err
		}
		value, ok := stack.Variables[name]
		if !ok {
			return fmt.Errorf("stack variable %q not found on stack %q", name, stackName)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), value)
		return nil
	},
}

var clusterStackVariablesSetCmd = &cobra.Command{
	Use:   "set <stack> <name> <value>",
	Short: "Create or update a variable on a stack",
	Long: `Create or update a stack variable. The value can be read from stdin by
passing "-".`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		name := args[1]
		rawValue := args[2]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		value, err := readVariableValue(cmd.InOrStdin(), rawValue)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		clusterID, _, doc, err := fetchClusterIaCDoc(ctx, clusterFlag)
		if err != nil {
			return err
		}
		stack, err := findStackInIaC(doc, stackName)
		if err != nil {
			return err
		}

		patchStack := copyStackMetadata(stack)
		if patchStack.Variables == nil {
			patchStack.Variables = map[string]string{}
		}
		existing, existed := patchStack.Variables[name]
		patchStack.Variables[name] = value

		if dryRun {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Would set stack %q variable %q = %q", stackName, name, value)
			if existed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), " (was %q)\n", existing)
			} else {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), " (new)")
			}
			return nil
		}

		if err := patchStackVariables(ctx, clusterID, patchStack); err != nil {
			return err
		}
		if existed {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stack %q variable %q updated.\n", stackName, name)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stack %q variable %q created.\n", stackName, name)
		}
		return nil
	},
}

var clusterStackVariablesDeleteCmd = &cobra.Command{
	Use:   "delete <stack> <name>",
	Short: "Delete a variable from a stack",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		name := args[1]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		yes, _ := cmd.Flags().GetBool("yes")

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		clusterID, _, doc, err := fetchClusterIaCDoc(ctx, clusterFlag)
		if err != nil {
			return err
		}
		stack, err := findStackInIaC(doc, stackName)
		if err != nil {
			return err
		}
		if _, ok := stack.Variables[name]; !ok {
			return fmt.Errorf("stack variable %q not found on stack %q", name, stackName)
		}

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete variable %q from stack %q? [y/N]: ", name, stackName),
			yes,
		); err != nil {
			return err
		}

		patchStack := copyStackMetadata(stack)
		delete(patchStack.Variables, name)
		// PATCH may serialise empty maps as null; that's the intended clear
		// semantics for stack.Variables (replace with the new map).
		if patchStack.Variables == nil {
			patchStack.Variables = map[string]string{}
		}

		if err := patchStackVariables(ctx, clusterID, patchStack); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stack %q variable %q deleted.\n", stackName, name)
		return nil
	},
}

// fetchStackForVariables resolves the cluster, fetches its IaC, and returns
// the named stack spec (with its Variables map) for read paths.
func fetchStackForVariables(ctx context.Context, clusterFlag, stackName string) (*client.StackSpec, error) {
	_, _, doc, err := fetchClusterIaCDoc(ctx, clusterFlag)
	if err != nil {
		return nil, err
	}
	return findStackInIaC(doc, stackName)
}

func findStackInIaC(doc *ImportClusterDoc, name string) (*client.StackSpec, error) {
	for i := range doc.Spec.Stacks {
		if doc.Spec.Stacks[i].Name == name {
			return &doc.Spec.Stacks[i], nil
		}
	}
	available := []string{}
	for i := range doc.Spec.Stacks {
		available = append(available, doc.Spec.Stacks[i].Name)
	}
	sort.Strings(available)
	return nil, fmt.Errorf("stack %q not found on cluster (available: %s)", name, joinOrNone(available))
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "<none>"
	}
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// patchStackVariables sends a stack-only partial PATCH that carries metadata +
// the updated variables map. Manifests/addons are intentionally omitted so
// the backend preserves them.
func patchStackVariables(ctx context.Context, clusterID string, patchStack client.StackSpec) error {
	req := buildPartialStackPatch(patchStack)
	if _, err := apiClient.PatchClusterStackPartial(ctx, clusterID, patchStack.Name, req); err != nil {
		var perr *client.PatchStackError
		if errors.As(err, &perr) {
			return mapPatchError(perr)
		}
		return err
	}
	return nil
}

func renderStackVariables(out io.Writer, stackName string, vars map[string]string, format outputFormat) error {
	type stackVarsPayload struct {
		StackName string            `json:"stack_name" yaml:"stack_name"`
		Variables map[string]string `json:"variables" yaml:"variables"`
	}
	payload := stackVarsPayload{StackName: stackName, Variables: vars}
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(payload)
	}
	if len(vars) == 0 {
		_, _ = fmt.Fprintf(out, "No variables on stack %q.\n", stackName)
		return nil
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "VALUE"})
	for _, k := range keys {
		t.AppendRow(table.Row{k, truncateForDisplay(vars[k], 60)})
	}
	t.Render()
	return nil
}

func init() {
	for _, c := range []*cobra.Command{clusterStackVariablesListCmd, clusterStackVariablesGetCmd} {
		c.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	}
	registerStructuredOutputFlags(clusterStackVariablesListCmd)
	clusterStackVariablesSetCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterStackVariablesSetCmd.Flags().Bool("dry-run", false, "Print the proposed change without writing")
	clusterStackVariablesDeleteCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterStackVariablesDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	clusterStackVariablesCmd.AddCommand(clusterStackVariablesListCmd)
	clusterStackVariablesCmd.AddCommand(clusterStackVariablesGetCmd)
	clusterStackVariablesCmd.AddCommand(clusterStackVariablesSetCmd)
	clusterStackVariablesCmd.AddCommand(clusterStackVariablesDeleteCmd)
	clusterStacksCmd.AddCommand(clusterStackVariablesCmd)
}
