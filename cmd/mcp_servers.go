package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/chzyer/readline"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var mcpPermissionTiers = []string{"read_only", "read_write"}
var mcpTransports = []string{"http", "sse", "stdio"}

var mcpServersCmd = &cobra.Command{
	Use:     "mcp-servers",
	Aliases: []string{"mcp-server", "mcp"},
	Short:   "Manage MCP tool servers agent runs can call",
	Long: `Register, inspect, and gate external MCP (Model Context Protocol) tool
servers for the organisation. A registered server's tools become callable
from agent runs, subject to the server's permission tier, its allowed-tools
list, and per-tool role grants.

Credential headers are never stored or sent in plaintext: 'add
--secret-header' first stores the value in an organisation secret slot and
registers the server with the slot's "${SECRET_SLOT:<id>}" sentinel - the
backend refuses plaintext values under sensitive-looking header names. The
curated adapter catalog ('ankra org mcp-servers catalog') documents the
exact header value form each provider expects (for example "Bearer <token>",
or "Sentry-Bearer <token>" for Sentry).

  ankra org mcp-servers catalog
  ankra org mcp-servers add sentry --adapter sentry --url https://mcp.sentry.dev/mcp \
    --secret-header Authorization
  ankra org mcp-servers grant sentry get_issue --role member
  ankra org mcp-servers health sentry

Managing MCP servers requires organisation admin rights.`,
}

var mcpServersCatalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List the curated MCP server adapters",
	Long: `List the curated adapters: known MCP server products with their
transport, URL placeholder, expected credential headers (including the exact
value form, e.g. "Bearer <token>"), and a recommended tool allow-list.

Passing an adapter key to 'add --adapter' records the pairing and seeds the
server's allowed tools from the adapter's recommendation when --allowed-tools
is not given.`,
	Example: "  ankra org mcp-servers catalog\n  ankra org mcp-servers catalog -o json",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		catalog, catalogError := apiClient.MCPCatalog(requestContext)
		if catalogError != nil {
			return fmt.Errorf("read MCP adapter catalog: %w", catalogError)
		}
		if handled, renderError := renderStructured(cmd, catalog); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		out := cmd.OutOrStdout()
		if len(catalog.Adapters) == 0 {
			_, _ = fmt.Fprintln(out, "No curated adapters available.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(out)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"KEY", "NAME", "TRANSPORT", "URL PLACEHOLDER", "CREDENTIAL HEADER"})
		for _, adapter := range catalog.Adapters {
			headers := make([]string, 0, len(adapter.Credentials))
			for _, credential := range adapter.Credentials {
				headers = append(headers, credential.Header)
			}
			t.AppendRow(table.Row{adapter.Key, adapter.DisplayName, adapter.Transport,
				adapter.URLPlaceholder, strings.Join(headers, ", ")})
		}
		t.Render()
		return nil
	},
}

var mcpServersListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the organisation's MCP servers",
	Long: `List every registered MCP server with its transport, enabled state,
permission tier, adapter pairing, grant count, and last connection error.`,
	Example: "  ankra org mcp-servers list\n  ankra org mcp-servers list -o json",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		servers, listError := apiClient.ListMCPServers(requestContext)
		if listError != nil {
			return fmt.Errorf("list MCP servers: %w", listError)
		}
		if handled, renderError := renderStructured(cmd, servers); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		out := cmd.OutOrStdout()
		if len(servers) == 0 {
			_, _ = fmt.Fprintln(out, "No MCP servers. Register one with: ankra org mcp-servers add <name> --url <url>")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(out)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"NAME", "TRANSPORT", "ENABLED", "TIER", "ADAPTER", "GRANTS", "LAST ERROR"})
		for _, server := range servers {
			adapter := ""
			if server.AdapterKey != nil {
				adapter = *server.AdapterKey
			}
			lastError := ""
			if server.LastError != nil {
				lastError = truncateForDisplay(*server.LastError, 40)
			}
			t.AppendRow(table.Row{server.Name, server.Transport, server.Enabled,
				server.PermissionTier, adapter, server.ToolGrantsCount, lastError})
		}
		t.Render()
		return nil
	},
}

var mcpServersGetCmd = &cobra.Command{
	Use:   "get <name-or-id>",
	Short: "Show one MCP server in detail",
	Long: `Show a server's full configuration: URL, transport, permission tier,
allowed tools, configured env/header names (values are never echoed back -
credential values live in secret slots), cluster allow-list, and connection
state.`,
	Example: "  ankra org mcp-servers get sentry\n  ankra org mcp-servers get sentry -o json",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		serverID, resolveError := resolveMCPServer(requestContext, args[0])
		if resolveError != nil {
			return resolveError
		}
		server, getError := apiClient.GetMCPServer(requestContext, serverID)
		if getError != nil {
			if errors.Is(getError, client.ErrMCPServerNotFound) {
				return withExitCode(exitNotFound, fmt.Errorf("MCP server %q not found", args[0]))
			}
			return fmt.Errorf("get MCP server: %w", getError)
		}
		if handled, renderError := renderStructured(cmd, server); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		renderMCPServerDetail(cmd.OutOrStdout(), server)
		return nil
	},
}

var mcpServersAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register an MCP tool server",
	Long: `Register an MCP server so agent runs can call its tools.

Credential headers come in two forms. --header sends a plaintext header (for
non-secret values only; the backend refuses plaintext under
sensitive-looking names). --secret-header stores the value in an
organisation secret slot labeled "<server-name> <Header>" and registers the
server with the slot's "${SECRET_SLOT:<id>}" sentinel, so the secret never
lands in the server record. Prefer passing only the header name
(--secret-header Authorization) to be prompted with hidden input, or pipe
the value on stdin - the inline Key=Value form works but leaves the secret
in your shell history and visible in process listings. The catalog
documents the exact value form each curated provider expects. If a later
step of the registration fails, slots already created for it are removed
again so no secret material is left orphaned.

With --adapter and no --allowed-tools, the allowed-tools list is seeded from
the adapter's recommended tools. Servers start enabled unless --disabled is
given, and --cluster (repeatable) restricts which clusters' agent runs may
use the server.

--url is required for every transport. For --transport stdio the platform
stores the value as the server's identifier only and never dials it, so a
descriptive placeholder such as cmd://<binary-name> is expected there.`,
	Example: `  ankra org mcp-servers add sentry --adapter sentry --url https://mcp.sentry.dev/mcp \
    --secret-header Authorization
  ankra org mcp-servers add internal-tools --url https://mcp.example.internal/sse \
    --transport sse --tier read_write --allowed-tools search_docs,create_ticket
  ankra org mcp-servers add staging-only --url https://mcp.example.com/mcp \
    --cluster 6f1f9aca-2c3d-4e5f-8a9b-0c1d2e3f4a5b --disabled`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		serverURL := mustFlagString(cmd, "url")
		transport := mustFlagString(cmd, "transport")
		tier := mustFlagString(cmd, "tier")
		adapterKey := mustFlagString(cmd, "adapter")
		if validationError := validateMCPTransport(transport); validationError != nil {
			return validationError
		}
		if validationError := validateMCPTier(tier); validationError != nil {
			return validationError
		}
		clusters, _ := cmd.Flags().GetStringArray("cluster")
		for _, clusterID := range clusters {
			if !looksLikeUUID(clusterID) {
				return withExitCode(exitUsage, fmt.Errorf("--cluster wants a cluster UUID, got %q", clusterID))
			}
		}

		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		env := map[string]string{}
		plainHeaders, _ := cmd.Flags().GetStringArray("header")
		for _, pair := range plainHeaders {
			key, value, found := strings.Cut(pair, "=")
			if !found || key == "" {
				return withExitCode(exitUsage, fmt.Errorf("--header wants Key=Value, got %q", pair))
			}
			env[key] = value
		}
		// Validate every --secret-header pair before creating any slot. A
		// malformed pair or a duplicate key discovered halfway through the
		// creation loop would leave already-created slots behind - and a
		// duplicate key would orphan its first slot even on success, because
		// the second sentinel overwrites the first in env.
		secretHeaders, _ := cmd.Flags().GetStringArray("secret-header")
		secretHeaderSeen := make(map[string]bool, len(secretHeaders))
		for _, pair := range secretHeaders {
			key, _, _ := strings.Cut(pair, "=")
			if key == "" {
				return withExitCode(exitUsage, fmt.Errorf("--secret-header wants Key=Value or Key, got %q", pair))
			}
			if secretHeaderSeen[key] {
				return withExitCode(exitUsage, fmt.Errorf(
					"--secret-header %s given more than once; the second value would overwrite the first and orphan its secret slot - pass each header once", key))
			}
			if _, collidesWithPlain := env[key]; collidesWithPlain {
				return withExitCode(exitUsage, fmt.Errorf(
					"header %s given as both --header and --secret-header; the secret-slot sentinel would overwrite the plaintext value - pass it once, as --secret-header", key))
			}
			secretHeaderSeen[key] = true
		}

		// Secret slots created for this registration are tracked so a later
		// failure (another slot, the catalog fetch, the registration itself)
		// does not orphan slots holding real secret material.
		var createdSlotIDs []string
		cleanupSlots := cleanupRegistrationSecretSlots(cmd, &createdSlotIDs)

		// One reader shared by every prompt in this invocation: a fresh
		// bufio.Reader per prompt would read ahead past the first line and
		// make a second piped secret see EOF.
		secretStdinReader := bufio.NewReader(cmd.InOrStdin())
		for _, pair := range secretHeaders {
			key, value, _ := strings.Cut(pair, "=")
			if value == "" {
				promptedValue, promptError := promptMCPSecretHeaderValue(cmd, secretStdinReader, key)
				if promptError != nil {
					return cleanupSlots(promptError)
				}
				value = promptedValue
			}
			slot, slotError := apiClient.CreateSecretSlot(requestContext, fmt.Sprintf("%s %s", name, key), value)
			if slotError != nil {
				return cleanupSlots(fmt.Errorf("create secret slot for header %s: %w", key, slotError))
			}
			createdSlotIDs = append(createdSlotIDs, slot.SlotID)
			env[key] = slot.Sentinel
		}

		allowedTools, _ := cmd.Flags().GetStringSlice("allowed-tools")
		if adapterKey != "" && !cmd.Flags().Changed("allowed-tools") {
			catalog, catalogError := apiClient.MCPCatalog(requestContext)
			if catalogError != nil {
				return cleanupSlots(fmt.Errorf("read MCP adapter catalog to seed allowed tools: %w", catalogError))
			}
			for _, adapter := range catalog.Adapters {
				if adapter.Key == adapterKey {
					allowedTools = adapter.RecommendedTools
					break
				}
			}
		}

		request := client.CreateMCPServerRequest{
			Name:             name,
			Description:      mustFlagString(cmd, "description"),
			Transport:        transport,
			URL:              serverURL,
			AllowedTools:     allowedTools,
			PermissionTier:   tier,
			AdapterKey:       adapterKey,
			ClusterAllowList: clusters,
		}
		if len(env) > 0 {
			request.Env = env
		}
		if disabled, _ := cmd.Flags().GetBool("disabled"); disabled {
			enabled := false
			request.Enabled = &enabled
		}

		server, createError := apiClient.CreateMCPServer(requestContext, request)
		if createError != nil {
			return cleanupSlots(fmt.Errorf("register MCP server: %w", createError))
		}
		if handled, renderError := renderStructured(cmd, server); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "MCP server %q registered (id %s).\n", server.Name, server.ID)
		if !server.Enabled {
			_, _ = fmt.Fprintf(out, "The server is disabled; enable it with: ankra org mcp-servers enable %s\n", server.Name)
		}
		_, _ = fmt.Fprintf(out, "Check reachability with: ankra org mcp-servers health %s\n", server.Name)
		return nil
	},
}

var mcpServersUpdateCmd = &cobra.Command{
	Use:   "update <name-or-id>",
	Short: "Update an MCP server's configuration",
	Long: `Apply a partial update to a server. Only the flags you pass change;
everything else keeps its current value. --clear-allowed-tools removes the
allow-list entirely (every tool the permission tier allows becomes
callable), and is mutually exclusive with --allowed-tools. Use the separate
'enable' and 'disable' subcommands to flip the server on or off.`,
	Example: `  ankra org mcp-servers update sentry --url https://mcp.sentry.dev/mcp
  ankra org mcp-servers update sentry --tier read_write --description "Sentry issue triage"
  ankra org mcp-servers update sentry --clear-allowed-tools`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var update client.MCPServerUpdate
		changed := false
		if cmd.Flags().Changed("description") {
			description := mustFlagString(cmd, "description")
			update.Description = &description
			changed = true
		}
		if cmd.Flags().Changed("url") {
			serverURL := mustFlagString(cmd, "url")
			update.URL = &serverURL
			changed = true
		}
		if cmd.Flags().Changed("tier") {
			tier := mustFlagString(cmd, "tier")
			if validationError := validateMCPTier(tier); validationError != nil {
				return validationError
			}
			update.PermissionTier = &tier
			changed = true
		}
		if cmd.Flags().Changed("allowed-tools") {
			allowedTools, _ := cmd.Flags().GetStringSlice("allowed-tools")
			update.AllowedTools = &allowedTools
			changed = true
		}
		if clear, _ := cmd.Flags().GetBool("clear-allowed-tools"); clear {
			var cleared []string
			update.AllowedTools = &cleared
			changed = true
		}
		if !changed {
			return withExitCode(exitUsage, errors.New(
				"nothing to update: pass at least one of --description, --url, --tier, --allowed-tools, --clear-allowed-tools"))
		}

		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		serverID, resolveError := resolveMCPServer(requestContext, args[0])
		if resolveError != nil {
			return resolveError
		}
		server, updateError := apiClient.UpdateMCPServer(requestContext, serverID, update)
		if updateError != nil {
			if errors.Is(updateError, client.ErrMCPServerNotFound) {
				return withExitCode(exitNotFound, fmt.Errorf("MCP server %q not found", args[0]))
			}
			return fmt.Errorf("update MCP server: %w", updateError)
		}
		if handled, renderError := renderStructured(cmd, server); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "MCP server %q updated.\n", server.Name)
		return nil
	},
}

var mcpServersRemoveCmd = &cobra.Command{
	Use:     "remove <name-or-id>",
	Aliases: []string{"rm", "delete"},
	Short:   "Delete an MCP server",
	Long: `Delete a server and its tool grants. Agent runs can no longer call its
tools; secret slots its headers referenced are not deleted (remove those
separately if nothing else uses them).`,
	Example: "  ankra org mcp-servers remove sentry\n  ankra org mcp-servers remove sentry --yes",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")

		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		serverID, resolveError := resolveMCPServer(requestContext, args[0])
		if resolveError != nil {
			return resolveError
		}
		if confirmError := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete MCP server %q? [y/N]: ", args[0]),
			yes,
		); confirmError != nil {
			return confirmError
		}
		if _, deleteError := apiClient.DeleteMCPServer(requestContext, serverID); deleteError != nil {
			if errors.Is(deleteError, client.ErrMCPServerNotFound) {
				return withExitCode(exitNotFound, fmt.Errorf("MCP server %q not found", args[0]))
			}
			return fmt.Errorf("delete MCP server: %w", deleteError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "MCP server %q deleted.\n", args[0])
		return nil
	},
}

var mcpServersEnableCmd = &cobra.Command{
	Use:     "enable <name-or-id>",
	Short:   "Enable an MCP server",
	Long:    `Enable a server so agent runs can call its granted tools again.`,
	Example: "  ankra org mcp-servers enable sentry",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setMCPServerEnabledCommand(cmd, args[0], true)
	},
}

var mcpServersDisableCmd = &cobra.Command{
	Use:   "disable <name-or-id>",
	Short: "Disable an MCP server",
	Long: `Disable a server without deleting it. Its configuration and grants are
kept, but agent runs cannot call its tools until it is enabled again.`,
	Example: "  ankra org mcp-servers disable sentry",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setMCPServerEnabledCommand(cmd, args[0], false)
	},
}

var mcpServersHealthCmd = &cobra.Command{
	Use:   "health <name-or-id>",
	Short: "Probe an MCP server's reachability",
	Long: `Probe the server: whether the platform can reach it and which of its
tools the current configuration allows. The raw probe document is available
with -o json.`,
	Example: "  ankra org mcp-servers health sentry\n  ankra org mcp-servers health sentry -o json",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return renderMCPProbeCommand(cmd, args[0], "health",
			func(requestContext context.Context, serverID string) (json.RawMessage, error) {
				return apiClient.GetMCPServerHealth(requestContext, serverID)
			})
	},
}

var mcpServersToolsCmd = &cobra.Command{
	Use:   "tools <name-or-id>",
	Short: "List the tools an MCP server exposes",
	Long: `List the server's tool inventory as the platform sees it, after the
allowed-tools filter. The raw inventory document is available with -o json.`,
	Example: "  ankra org mcp-servers tools sentry\n  ankra org mcp-servers tools sentry -o json",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return renderMCPProbeCommand(cmd, args[0], "tools",
			func(requestContext context.Context, serverID string) (json.RawMessage, error) {
				return apiClient.ListMCPServerTools(requestContext, serverID)
			})
	},
}

var mcpServersGrantsCmd = &cobra.Command{
	Use:   "grants <name-or-id>",
	Short: "List a server's per-tool role grants",
	Long: `List which tools are granted to which organisation roles on a server.
A tool with no grant is not callable from agent runs regardless of the
allowed-tools list.`,
	Example: "  ankra org mcp-servers grants sentry\n  ankra org mcp-servers grants sentry -o json",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		serverID, resolveError := resolveMCPServer(requestContext, args[0])
		if resolveError != nil {
			return resolveError
		}
		grants, listError := apiClient.ListMCPToolGrants(requestContext, serverID)
		if listError != nil {
			if errors.Is(listError, client.ErrMCPServerNotFound) {
				return withExitCode(exitNotFound, fmt.Errorf("MCP server %q not found", args[0]))
			}
			return fmt.Errorf("list tool grants: %w", listError)
		}
		if handled, renderError := renderStructured(cmd, grants); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		out := cmd.OutOrStdout()
		if len(grants) == 0 {
			_, _ = fmt.Fprintf(out, "No tool grants. Add one with: ankra org mcp-servers grant %s <tool_name>\n", args[0])
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(out)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"TOOL", "ROLE", "CREATED"})
		for _, grant := range grants {
			t.AppendRow(table.Row{grant.ToolName, grant.AllowedToRole, formatTimeAgo(grant.CreatedAt)})
		}
		t.Render()
		return nil
	},
}

var mcpServersGrantCmd = &cobra.Command{
	Use:   "grant <name-or-id> <tool_name>",
	Short: "Grant one of a server's tools to a role",
	Long: `Grant a tool to an organisation role, making it callable from agent
runs started by members holding that role. Grants are additive; revoke one
with 'revoke-grant'.`,
	Example: `  ankra org mcp-servers grant sentry get_issue
  ankra org mcp-servers grant sentry create_ticket --role admin`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		role := mustFlagString(cmd, "role")

		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		serverID, resolveError := resolveMCPServer(requestContext, args[0])
		if resolveError != nil {
			return resolveError
		}
		grant, grantError := apiClient.GrantMCPTool(requestContext, serverID, args[1], role)
		if grantError != nil {
			if errors.Is(grantError, client.ErrMCPServerNotFound) {
				return withExitCode(exitNotFound, fmt.Errorf("MCP server %q not found", args[0]))
			}
			return fmt.Errorf("grant tool: %w", grantError)
		}
		if handled, renderError := renderStructured(cmd, grant); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Granted tool %q to role %q on MCP server %q.\n",
			grant.ToolName, grant.AllowedToRole, args[0])
		return nil
	},
}

var mcpServersRevokeGrantCmd = &cobra.Command{
	Use:   "revoke-grant <name-or-id> <tool_name>",
	Short: "Revoke a tool grant from a role",
	Long: `Revoke one tool's grant from one role. Other roles' grants on the same
tool are untouched.`,
	Example: `  ankra org mcp-servers revoke-grant sentry get_issue
  ankra org mcp-servers revoke-grant sentry create_ticket --role admin`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		role := mustFlagString(cmd, "role")

		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		serverID, resolveError := resolveMCPServer(requestContext, args[0])
		if resolveError != nil {
			return resolveError
		}
		if _, revokeError := apiClient.RevokeMCPToolGrant(requestContext, serverID, args[1], role); revokeError != nil {
			if errors.Is(revokeError, client.ErrMCPServerNotFound) {
				return withExitCode(exitNotFound,
					fmt.Errorf("no grant of tool %q to role %q on MCP server %q", args[1], role, args[0]))
			}
			return fmt.Errorf("revoke tool grant: %w", revokeError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked tool %q from role %q on MCP server %q.\n",
			args[1], role, args[0])
		return nil
	},
}

// resolveMCPServer resolves a server reference to its id: a UUID passes
// through untouched, anything else is matched by name against the listing.
// A miss carries exitNotFound and names the servers that do exist.
func resolveMCPServer(requestContext context.Context, reference string) (string, error) {
	if looksLikeUUID(reference) {
		return reference, nil
	}
	servers, listError := apiClient.ListMCPServers(requestContext)
	if listError != nil {
		return "", fmt.Errorf("list MCP servers: %w", listError)
	}
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		if strings.EqualFold(server.Name, reference) {
			return server.ID, nil
		}
		names = append(names, server.Name)
	}
	hint := "none registered yet"
	if len(names) > 0 {
		hint = strings.Join(names, ", ")
	}
	return "", withExitCode(exitNotFound,
		fmt.Errorf("no MCP server matches %q. Available servers: %s", reference, hint))
}

// setMCPServerEnabledCommand is the shared body of the enable and disable
// subcommands.
func setMCPServerEnabledCommand(cmd *cobra.Command, reference string, enabled bool) error {
	requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	serverID, resolveError := resolveMCPServer(requestContext, reference)
	if resolveError != nil {
		return resolveError
	}
	result, actionError := apiClient.SetMCPServerEnabled(requestContext, serverID, enabled)
	if actionError != nil {
		if errors.Is(actionError, client.ErrMCPServerNotFound) {
			return withExitCode(exitNotFound, fmt.Errorf("MCP server %q not found", reference))
		}
		verb := "disable"
		if enabled {
			verb = "enable"
		}
		return fmt.Errorf("%s MCP server: %w", verb, actionError)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "MCP server %q %s.\n", reference, result.Status)
	return nil
}

// renderMCPProbeCommand runs a health or tools probe and renders the
// backend-defined document: passed through raw under -o json|yaml, otherwise
// summarised via the well-known healthy/ok/error/tools members.
func renderMCPProbeCommand(cmd *cobra.Command, reference string, probeLabel string,
	probe func(context.Context, string) (json.RawMessage, error)) error {
	requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	serverID, resolveError := resolveMCPServer(requestContext, reference)
	if resolveError != nil {
		return resolveError
	}
	raw, probeError := probe(requestContext, serverID)
	if probeError != nil {
		if errors.Is(probeError, client.ErrMCPServerNotFound) {
			return withExitCode(exitNotFound, fmt.Errorf("MCP server %q not found", reference))
		}
		return fmt.Errorf("read MCP server %s: %w", probeLabel, probeError)
	}
	var document interface{}
	if decodeError := json.Unmarshal(raw, &document); decodeError != nil {
		return fmt.Errorf("parse %s response: %w", probeLabel, decodeError)
	}
	if handled, renderError := renderStructured(cmd, document); renderError != nil {
		return renderError
	} else if handled {
		return nil
	}
	renderMCPProbeDocument(cmd.OutOrStdout(), document)
	return nil
}

// renderMCPProbeDocument prints the well-known members of a probe document
// (healthy/ok/error/tools); anything else falls back to indented JSON so no
// backend field is ever silently dropped.
func renderMCPProbeDocument(out io.Writer, document interface{}) {
	fields, isObject := document.(map[string]interface{})
	if !isObject {
		printIndentedJSON(out, document)
		return
	}
	printed := false
	for _, key := range []string{"healthy", "ok", "error"} {
		value, present := fields[key]
		if !present || value == nil {
			continue
		}
		_, _ = fmt.Fprintf(out, "%-8s %v\n", strings.ToUpper(key[:1])+key[1:]+":", value)
		printed = true
	}
	if tools, present := fields["tools"]; present {
		if toolList, isList := tools.([]interface{}); isList {
			_, _ = fmt.Fprintf(out, "Tools (%d):\n", len(toolList))
			for _, tool := range toolList {
				switch typedTool := tool.(type) {
				case string:
					_, _ = fmt.Fprintf(out, "  %s\n", typedTool)
				case map[string]interface{}:
					if name, hasName := typedTool["name"].(string); hasName {
						_, _ = fmt.Fprintf(out, "  %s\n", name)
					} else {
						printIndentedJSON(out, typedTool)
					}
				default:
					_, _ = fmt.Fprintf(out, "  %v\n", typedTool)
				}
			}
			printed = true
		}
	}
	if !printed {
		printIndentedJSON(out, document)
	}
}

func printIndentedJSON(out io.Writer, document interface{}) {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(document)
}

func renderMCPServerDetail(out io.Writer, server *client.MCPServer) {
	adapter := "-"
	if server.AdapterKey != nil && *server.AdapterKey != "" {
		adapter = *server.AdapterKey
	}
	lastConnected := "-"
	if server.LastConnectedAt != nil && *server.LastConnectedAt != "" {
		lastConnected = formatTimeAgo(*server.LastConnectedAt)
	}
	lastError := "-"
	if server.LastError != nil && *server.LastError != "" {
		lastError = *server.LastError
	}
	formatList := func(values []string) string {
		if len(values) == 0 {
			return "-"
		}
		return strings.Join(values, ", ")
	}
	_, _ = fmt.Fprintf(out, "Name:           %s\n", server.Name)
	_, _ = fmt.Fprintf(out, "ID:             %s\n", server.ID)
	_, _ = fmt.Fprintf(out, "Description:    %s\n", server.Description)
	_, _ = fmt.Fprintf(out, "Transport:      %s\n", server.Transport)
	_, _ = fmt.Fprintf(out, "URL:            %s\n", server.URL)
	_, _ = fmt.Fprintf(out, "Enabled:        %v\n", server.Enabled)
	_, _ = fmt.Fprintf(out, "Tier:           %s\n", server.PermissionTier)
	_, _ = fmt.Fprintf(out, "Adapter:        %s\n", adapter)
	_, _ = fmt.Fprintf(out, "Env keys:       %s\n", formatList(server.EnvKeys))
	_, _ = fmt.Fprintf(out, "Allowed tools:  %s\n", formatList(server.AllowedTools))
	_, _ = fmt.Fprintf(out, "Cluster allow:  %s\n", formatList(server.ClusterAllowList))
	_, _ = fmt.Fprintf(out, "Last connected: %s\n", lastConnected)
	_, _ = fmt.Fprintf(out, "Last error:     %s\n", lastError)
}

// promptMCPSecretHeaderValue reads a secret header value that was not given
// inline: a masked prompt on an interactive terminal, one line from stdin
// otherwise. Modeled on resolveSecretInput (cmd/ai.go); the value is never
// echoed back. stdinReader must be shared across every prompt of one
// invocation: bufio read-ahead means a per-prompt reader would swallow the
// lines meant for the prompts after it.
func promptMCPSecretHeaderValue(cmd *cobra.Command, stdinReader *bufio.Reader, headerName string) (string, error) {
	in := cmd.InOrStdin()
	label := fmt.Sprintf("Value for secret header %s", headerName)
	if file, isFile := in.(*os.File); isFile && readline.IsTerminal(int(file.Fd())) {
		prompt := promptui.Prompt{Label: label, Mask: '*', Stdin: file}
		value, promptError := prompt.Run()
		if promptError != nil {
			if isPromptCancellation(promptError) {
				return "", errCancelled
			}
			return "", fmt.Errorf("reading %s: %w", label, promptError)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", withExitCode(exitUsage, fmt.Errorf("secret header %s needs a value", headerName))
		}
		return value, nil
	}
	line, readError := stdinReader.ReadString('\n')
	if readError != nil && !errors.Is(readError, io.EOF) {
		return "", fmt.Errorf("reading secret header %s from stdin: %w", headerName, readError)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", withExitCode(exitUsage, fmt.Errorf(
			"secret header %s needs a value: pass one line per prompted header on stdin, or run interactively to be prompted",
			headerName))
	}
	return value, nil
}

// cleanupRegistrationSecretSlots returns a wrapper that, on a registration
// failure, best-effort deletes the secret slots this invocation created so
// the failed 'add' does not orphan stored secret material. Deletion runs on
// a fresh context because the failure may be the request context expiring.
// Slots that could not be removed are named in the error so they are never
// silently abandoned.
func cleanupRegistrationSecretSlots(cmd *cobra.Command, createdSlotIDs *[]string) func(error) error {
	return func(cause error) error {
		if len(*createdSlotIDs) == 0 {
			return cause
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var undeletedSlotIDs []string
		for _, slotID := range *createdSlotIDs {
			if _, deleteError := apiClient.DeleteSecretSlot(cleanupContext, slotID); deleteError != nil {
				undeletedSlotIDs = append(undeletedSlotIDs, slotID)
			}
		}
		if len(undeletedSlotIDs) > 0 {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: could not remove secret slot(s) %s created for this registration; delete them with the platform so the secret material is not left behind.\n",
				strings.Join(undeletedSlotIDs, ", "))
			return cause
		}
		return fmt.Errorf("%w (removed the %d secret slot(s) created for this registration)", cause, len(*createdSlotIDs))
	}
}

func validateMCPTier(tier string) error {
	if tier == "" {
		return nil
	}
	for _, allowed := range mcpPermissionTiers {
		if tier == allowed {
			return nil
		}
	}
	return withExitCode(exitUsage,
		fmt.Errorf("invalid tier %q; valid tiers: %s", tier, strings.Join(mcpPermissionTiers, ", ")))
}

func validateMCPTransport(transport string) error {
	for _, allowed := range mcpTransports {
		if transport == allowed {
			return nil
		}
	}
	return withExitCode(exitUsage,
		fmt.Errorf("invalid transport %q; valid transports: %s", transport, strings.Join(mcpTransports, ", ")))
}

func init() {
	registerStructuredOutputFlags(mcpServersCatalogCmd, mcpServersListCmd, mcpServersGetCmd,
		mcpServersAddCmd, mcpServersUpdateCmd, mcpServersHealthCmd, mcpServersToolsCmd,
		mcpServersGrantsCmd, mcpServersGrantCmd)

	mcpServersAddCmd.Flags().String("url", "", "The MCP server endpoint URL (required for every transport; with --transport stdio it is stored as the server's identifier and never dialed, so use a placeholder like cmd://<binary-name>)")
	_ = mcpServersAddCmd.MarkFlagRequired("url")
	mcpServersAddCmd.Flags().String("adapter", "", "Curated adapter key from 'catalog' to pair with (seeds allowed tools)")
	mcpServersAddCmd.Flags().String("transport", "http", "Transport: http, sse, or stdio")
	mcpServersAddCmd.Flags().String("description", "", "Optional human-readable description")
	mcpServersAddCmd.Flags().String("tier", "", "Permission tier: read_only or read_write (default: backend's read_only)")
	mcpServersAddCmd.Flags().StringSlice("allowed-tools", nil, "Comma-separated tool allow-list (default: the adapter's recommended tools when --adapter is set)")
	mcpServersAddCmd.Flags().StringArray("cluster", nil, "Cluster UUID allowed to use the server (repeatable; default: all clusters)")
	mcpServersAddCmd.Flags().StringArray("header", nil, "Plaintext header as Key=Value (repeatable; non-secret values only)")
	mcpServersAddCmd.Flags().StringArray("secret-header", nil, "Secret header stored in a secret slot and referenced by sentinel (repeatable). Prefer Key alone (hidden prompt, or one line per header on piped stdin); the inline Key=Value form lands in shell history and process listings")
	mcpServersAddCmd.Flags().Bool("disabled", false, "Register the server disabled")

	mcpServersUpdateCmd.Flags().String("description", "", "New description")
	mcpServersUpdateCmd.Flags().String("url", "", "New endpoint URL")
	mcpServersUpdateCmd.Flags().String("tier", "", "New permission tier: read_only or read_write")
	mcpServersUpdateCmd.Flags().StringSlice("allowed-tools", nil, "Replace the tool allow-list (comma-separated)")
	mcpServersUpdateCmd.Flags().Bool("clear-allowed-tools", false, "Remove the tool allow-list entirely")
	mcpServersUpdateCmd.MarkFlagsMutuallyExclusive("allowed-tools", "clear-allowed-tools")

	mcpServersRemoveCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	mcpServersGrantCmd.Flags().String("role", "member", "Organisation role the tool is granted to")
	mcpServersRevokeGrantCmd.Flags().String("role", "member", "Organisation role the grant is revoked from")

	mcpServersCmd.AddCommand(mcpServersCatalogCmd)
	mcpServersCmd.AddCommand(mcpServersListCmd)
	mcpServersCmd.AddCommand(mcpServersGetCmd)
	mcpServersCmd.AddCommand(mcpServersAddCmd)
	mcpServersCmd.AddCommand(mcpServersUpdateCmd)
	mcpServersCmd.AddCommand(mcpServersRemoveCmd)
	mcpServersCmd.AddCommand(mcpServersEnableCmd)
	mcpServersCmd.AddCommand(mcpServersDisableCmd)
	mcpServersCmd.AddCommand(mcpServersHealthCmd)
	mcpServersCmd.AddCommand(mcpServersToolsCmd)
	mcpServersCmd.AddCommand(mcpServersGrantsCmd)
	mcpServersCmd.AddCommand(mcpServersGrantCmd)
	mcpServersCmd.AddCommand(mcpServersRevokeGrantCmd)
	orgCmd.AddCommand(mcpServersCmd)
}
