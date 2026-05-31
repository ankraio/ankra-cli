package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var clusterVariablesCmd = &cobra.Command{
	Use:   "variables",
	Short: "Manage cluster-scoped variables",
	Long: `Manage cluster-scoped variables that are available to every stack on the
cluster as template substitutions in manifests and addon values. Cluster
variables shadow organisation variables of the same name on this cluster.

  ankra cluster variables list
  ankra cluster variables get DB_HOST --cluster prod
  ankra cluster variables set DB_HOST db.prod.example.com
  ankra cluster variables delete DB_HOST

When --cluster is omitted, the active selection is used.`,
}

var clusterVariablesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List variables for a cluster",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		outRaw, _ := cmd.Flags().GetString("output")
		out, err := parseOutputFormat(outRaw)
		if err != nil {
			return err
		}

		clusterID, _, err := resolveClusterForCmd(clusterFlag)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListClusterVariables(ctx, clusterID)
		if err != nil {
			return fmt.Errorf("list cluster variables: %w", err)
		}
		return renderClusterVariables(cmd.OutOrStdout(), list.Variables, out)
	},
}

var clusterVariablesGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get a single cluster variable",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		outRaw, _ := cmd.Flags().GetString("output")
		out, err := parseOutputFormat(outRaw)
		if err != nil {
			return err
		}

		clusterID, _, err := resolveClusterForCmd(clusterFlag)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListClusterVariables(ctx, clusterID)
		if err != nil {
			return fmt.Errorf("list cluster variables: %w", err)
		}
		for _, v := range list.Variables {
			if v.Name == name {
				return renderSingleClusterVariable(cmd.OutOrStdout(), v, out)
			}
		}
		return fmt.Errorf("cluster variable %q not found", name)
	},
}

var clusterVariablesSetCmd = &cobra.Command{
	Use:   "set <name> <value>",
	Short: "Create or update a cluster variable",
	Long: `Create or update a cluster variable (upsert). The value can be read from
stdin by passing "-".`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		rawValue := args[1]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		descriptionFlag := cmd.Flags().Lookup("description")
		description := descriptionFlag.Value.String()
		descriptionExplicit := descriptionFlag.Changed

		value, err := readVariableValue(cmd.InOrStdin(), rawValue)
		if err != nil {
			return err
		}

		clusterID, _, err := resolveClusterForCmd(clusterFlag)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		_, err = apiClient.CreateClusterVariable(ctx, clusterID, name, value, description)
		if err == nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Cluster variable %q created.\n", name)
			return nil
		}
		if !errors.Is(err, client.ErrVariableDuplicate) {
			return fmt.Errorf("create cluster variable: %w", err)
		}
		// Upsert fallback: preserve the existing description when
		// --description was not explicitly provided.
		if !descriptionExplicit {
			list, listErr := apiClient.ListClusterVariables(ctx, clusterID)
			if listErr == nil {
				for _, v := range list.Variables {
					if v.Name == name {
						description = v.Description
						break
					}
				}
			}
		}
		if _, err := apiClient.UpdateClusterVariable(ctx, clusterID, name, value, description); err != nil {
			return fmt.Errorf("update cluster variable: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Cluster variable %q updated.\n", name)
		return nil
	},
}

var clusterVariablesDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a cluster variable",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		yes, _ := cmd.Flags().GetBool("yes")

		clusterID, _, err := resolveClusterForCmd(clusterFlag)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete cluster variable %q? [y/N]: ", name),
			yes,
		); err != nil {
			return err
		}

		if err := apiClient.DeleteClusterVariable(ctx, clusterID, name); err != nil {
			if errors.Is(err, client.ErrVariableNotFound) {
				return fmt.Errorf("cluster variable %q not found", name)
			}
			return fmt.Errorf("delete cluster variable: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Cluster variable %q deleted.\n", name)
		return nil
	},
}

func renderClusterVariables(out io.Writer, vars []client.ClusterVariable, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.ClusterVariablesListResult{Variables: vars})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer enc.Close()
		return enc.Encode(client.ClusterVariablesListResult{Variables: vars})
	}
	if len(vars) == 0 {
		fmt.Fprintln(out, "No cluster variables.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "VALUE", "DESCRIPTION", "UPDATED"})
	for _, v := range vars {
		t.AppendRow(table.Row{v.Name, truncateForDisplay(v.Value, 40), v.Description, v.UpdatedAt.Format(time.RFC3339)})
	}
	t.Render()
	return nil
}

func renderSingleClusterVariable(out io.Writer, v client.ClusterVariable, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.ClusterVariableResult{Variable: v})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer enc.Close()
		return enc.Encode(client.ClusterVariableResult{Variable: v})
	}
	fmt.Fprintf(out, "Name:        %s\nValue:       %s\nDescription: %s\nUpdated:     %s\n",
		v.Name, v.Value, v.Description, v.UpdatedAt.Format(time.RFC3339))
	return nil
}

func init() {
	for _, c := range []*cobra.Command{clusterVariablesListCmd, clusterVariablesGetCmd} {
		c.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
		c.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")
	}
	clusterVariablesSetCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterVariablesSetCmd.Flags().String("description", "", "Optional human-readable description")
	clusterVariablesDeleteCmd.Flags().String("cluster", "", "Target cluster (name or ID); defaults to the active selection")
	clusterVariablesDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	clusterVariablesCmd.AddCommand(clusterVariablesListCmd)
	clusterVariablesCmd.AddCommand(clusterVariablesGetCmd)
	clusterVariablesCmd.AddCommand(clusterVariablesSetCmd)
	clusterVariablesCmd.AddCommand(clusterVariablesDeleteCmd)
	clusterCmd.AddCommand(clusterVariablesCmd)
}
