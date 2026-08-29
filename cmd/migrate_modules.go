package cmd

import (
	"fmt"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/migrate"
)

var migrateModulesCmd = &cobra.Command{
	Use:   "modules",
	Short: "List the conversion modules available, built-in and external",
	Long: `List every conversion module 'ankra migrate' can use.

Built-in modules ship with the CLI. External modules are executables named
'ankra-module-<name>' found in ~/.ankra/modules or on PATH, in that order; a
name that collides with a built-in is ignored.

An external module answers three verbs, each with a JSON request on stdin
and a JSON reply on stdout:

  ankra-module-<name> describe
      -> {"name":"<name>","version":"1.2","protocol":1,"summary":"...",
          "file_patterns":["Procfile"]}
  ankra-module-<name> detect
      <- {"dir":"/path/to/project"}
      -> {"confidence":0.9,"files":["Procfile"],"reason":"Procfile present"}
  ankra-module-<name> convert
      <- {"dir":"...","cluster_name":"...","namespace":"...","options":{"k":"v"}}
      -> {"cluster":{<ImportCluster>},"files":{"manifests/web.yaml":"..."},
          "warnings":["..."]}

Confidence runs 0 (not mine) to 1 (certain); reserve 1 for an unambiguous
marker file so a specific module wins over a general one. File paths in the
reply are relative to the output directory and may not escape it. A non-zero
exit fails the verb and the module's stderr is shown as the reason.
Warnings are for anything the module could not translate faithfully: a
partial conversion a person can finish beats none.

A module that lists "export" under "capabilities" in its description also
answers a fourth verb, which backs 'ankra migrate export':

  ankra-module-<name> export
      <- {"dir":"...","output_dir":"/abs/path","namespace":"...","options":{}}
      -> {"databases":[{"workload":"db","engine":"postgres","server_version":"17.2",
           "target":{"namespace":"...","host":"db","port":5432,"username":"app",
                     "password_secret":"db-secrets","password_key":"POSTGRES_PASSWORD"},
           "artifacts":[{"path":"db/globals.sql","kind":"globals","format":"sql"},
                        {"path":"db/app.dump","kind":"database","format":"pg_custom","database":"app"}]}],
          "warnings":["..."]}

The module writes the dumps under output_dir and reports their paths; the
CLI measures sizes and checksums itself. Stderr is relayed live while an
export runs, so narrate progress there.

Install a module without touching PATH with 'ankra migrate modules
install <https-url-or-file>' (validated via its describe verb; --sha256
pins the download) and remove it with 'ankra migrate modules uninstall
<name>'.

The protocol version is ` + fmt.Sprint(migrate.ProtocolVersion) + `. The reference implementation is the built-in
'docker' module; a worked external example lives in the CLI repository under
examples/modules/.`,
	Args: cobra.NoArgs,
	RunE: runMigrateModules,
}

func init() {
	registerStructuredOutputFlags(migrateModulesCmd)
	migrateCmd.AddCommand(migrateModulesCmd)
}

func runMigrateModules(cmd *cobra.Command, _ []string) error {
	modules, notes := newMigrateRegistry().Modules(cmd.Context())

	descriptions := make([]migrate.Description, 0, len(modules))
	for _, module := range modules {
		descriptions = append(descriptions, module.Describe())
	}

	for _, note := range notes {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), note)
	}

	if handled, err := renderStructured(cmd, descriptions); handled || err != nil {
		return err
	}

	writer := table.NewWriter()
	writer.SetOutputMirror(cmd.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"NAME", "VERSION", "SOURCE", "SUMMARY"})
	for _, description := range descriptions {
		source := "built-in"
		if !description.Builtin {
			source = description.Path
		}
		writer.AppendRow(table.Row{description.Name, description.Version, source, description.Summary})
	}
	writer.Render()
	return nil
}
