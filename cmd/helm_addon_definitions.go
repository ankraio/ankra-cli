package cmd

// `ankra helm addon-definitions ...` and `ankra helm addon-catalog ...`
// (ankra-hq41xb.15): the CLI surface over /api/v1/org/helm/addon-definitions
// (list, get, create, update, delete and the manual chart links) and
// /api/v1/org/helm/addon-catalog (the add-on-first view of the
// organisation's charts).

import (
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var helmAddonDefinitionsCmd = &cobra.Command{
	Use:     "addon-definitions",
	Aliases: []string{"addon-definition"},
	Short:   "Manage add-on definitions: what an add-on is and which charts it matches",
	Long: `An add-on definition describes one add-on (its name, description, links and
category) and which charts it matches: by chart name, by chart keyword and by
the prefix of a chart's home URL. The add-on catalog groups the organisation's
charts under the definitions that match them.

The organisation sees Ankra's global definitions and its own. Ankra's are
read-only; changing or deleting one is refused (403). A chart link is a manual
override: it includes a chart the rules miss, or excludes one they match.`,
}

var helmAddonDefinitionsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the add-on definitions visible to the organisation",
	Example: `  ankra helm addon-definitions list
  ankra helm addon-definitions list -o json`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		definitions, listError := apiClient.ListHelmAddonDefinitions(command.Context())
		if listError != nil {
			return fmt.Errorf("listing add-on definitions: %w", listError)
		}
		if rendered, renderError := renderStructured(command, definitions); rendered || renderError != nil {
			return renderError
		}
		out := command.OutOrStdout()
		if len(definitions) == 0 {
			_, _ = fmt.Fprintln(out, "No add-on definitions found.")
			return nil
		}
		rows := make([]table.Row, 0, len(definitions))
		for _, definition := range definitions {
			rows = append(rows, table.Row{definition.Name, definition.DisplayName, optionalText(definition.Category),
				definition.Source, strings.Join(definition.MatchChartNames, ", ")})
		}
		renderRoundedTable(out, table.Row{"Name", "Display name", "Category", "Source", "Matches charts"}, rows)
		return nil
	},
}

func printHelmAddonDefinition(out io.Writer, definition client.HelmAddonDefinition) {
	_, _ = fmt.Fprintf(out, "Add-on definition: %s\n", definition.Name)
	_, _ = fmt.Fprintf(out, "  Display name:       %s\n", definition.DisplayName)
	_, _ = fmt.Fprintf(out, "  Source:             %s\n", definition.Source)
	_, _ = fmt.Fprintf(out, "  Category:           %s\n", optionalText(definition.Category))
	_, _ = fmt.Fprintf(out, "  Description:        %s\n", optionalText(definition.Description))
	_, _ = fmt.Fprintf(out, "  Website:            %s\n", optionalText(definition.WebsiteURL))
	_, _ = fmt.Fprintf(out, "  Documentation:      %s\n", optionalText(definition.DocumentationURL))
	_, _ = fmt.Fprintf(out, "  Chart names:        %s\n", joinedOrDash(definition.MatchChartNames))
	_, _ = fmt.Fprintf(out, "  Chart keywords:     %s\n", joinedOrDash(definition.MatchKeywords))
	_, _ = fmt.Fprintf(out, "  Home URL prefixes:  %s\n", joinedOrDash(definition.MatchHomeURLPrefixes))
}

func joinedOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

var helmAddonDefinitionsGetCmd = &cobra.Command{
	Use:     "get <name>",
	Short:   "Show one add-on definition with its chart links",
	Example: `  ankra helm addon-definitions get cert-manager`,
	Args:    cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		definition, getError := apiClient.GetHelmAddonDefinition(command.Context(), arguments[0])
		if getError != nil {
			return fmt.Errorf("reading add-on definition %q: %w", arguments[0], getError)
		}
		if rendered, renderError := renderStructured(command, definition); rendered || renderError != nil {
			return renderError
		}
		out := command.OutOrStdout()
		printHelmAddonDefinition(out, definition.HelmAddonDefinition)
		if len(definition.ChartLinks) == 0 {
			_, _ = fmt.Fprintln(out, "  Chart links:        none")
			return nil
		}
		_, _ = fmt.Fprintln(out, "  Chart links:")
		for _, link := range definition.ChartLinks {
			_, _ = fmt.Fprintf(out, "    %s %s from %s\n", link.LinkType, link.ChartName, link.RegistryURL)
		}
		return nil
	},
}

// addonDefinitionTextFlags are the optional text members of a definition:
// the flag that sets each and where it lands in the write body.
var addonDefinitionTextFlags = []struct {
	flag   string
	usage  string
	target func(*client.HelmAddonDefinitionWrite) **string
}{
	{"description", "What the add-on is", func(write *client.HelmAddonDefinitionWrite) **string { return &write.Description }},
	{"website-url", "The add-on's website", func(write *client.HelmAddonDefinitionWrite) **string { return &write.WebsiteURL }},
	{"documentation-url", "The add-on's documentation", func(write *client.HelmAddonDefinitionWrite) **string { return &write.DocumentationURL }},
	{"icon-url", "The add-on's icon", func(write *client.HelmAddonDefinitionWrite) **string { return &write.IconURL }},
	{"category", "The catalog category the add-on is listed under", func(write *client.HelmAddonDefinitionWrite) **string { return &write.Category }},
}

// addonDefinitionListFlags are the match rules of a definition.
var addonDefinitionListFlags = []struct {
	flag   string
	usage  string
	target func(*client.HelmAddonDefinitionWrite) *[]string
}{
	{"match-chart-name", "A chart name this add-on matches (repeat or comma-separate)", func(write *client.HelmAddonDefinitionWrite) *[]string { return &write.MatchChartNames }},
	{"match-keyword", "A chart keyword this add-on matches (repeat or comma-separate)", func(write *client.HelmAddonDefinitionWrite) *[]string { return &write.MatchKeywords }},
	{"match-home-url-prefix", "A chart home URL prefix this add-on matches (repeat or comma-separate)", func(write *client.HelmAddonDefinitionWrite) *[]string { return &write.MatchHomeURLPrefixes }},
}

func registerAddonDefinitionWriteFlags(command *cobra.Command) {
	command.Flags().String("display-name", "", "The add-on's name as people read it")
	for _, text := range addonDefinitionTextFlags {
		command.Flags().String(text.flag, "", text.usage)
	}
	for _, list := range addonDefinitionListFlags {
		command.Flags().StringSlice(list.flag, nil, list.usage)
	}
	registerStructuredOutputFlags(command)
}

// applyAddonDefinitionFlags writes every flag the user passed into the
// body and leaves the rest as it is, so an update changes only what was
// named. An optional text flag passed empty clears the member.
func applyAddonDefinitionFlags(command *cobra.Command, write *client.HelmAddonDefinitionWrite) {
	if command.Flags().Changed("display-name") {
		displayName, _ := command.Flags().GetString("display-name")
		write.DisplayName = strings.TrimSpace(displayName)
	}
	for _, text := range addonDefinitionTextFlags {
		if !command.Flags().Changed(text.flag) {
			continue
		}
		value, _ := command.Flags().GetString(text.flag)
		if value == "" {
			*text.target(write) = nil
			continue
		}
		*text.target(write) = &value
	}
	for _, list := range addonDefinitionListFlags {
		if !command.Flags().Changed(list.flag) {
			continue
		}
		values, _ := command.Flags().GetStringSlice(list.flag)
		kept := []string{}
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				kept = append(kept, trimmed)
			}
		}
		*list.target(write) = kept
	}
}

var helmAddonDefinitionsCreateCmd = &cobra.Command{
	Use:   "create <name> --display-name <text>",
	Short: "Create an organisation add-on definition",
	Long: `Create an add-on definition for the organisation. <name> is a lowercase slug
(letters, digits and dashes). The match rules say which charts the catalog
groups under the add-on: any chart whose name, keyword or home URL prefix
matches. A name already in use is refused (409).`,
	Example: `  ankra helm addon-definitions create internal-gateway --display-name "Internal gateway" \
    --category networking --match-chart-name internal-gateway --match-keyword gateway`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		write := client.HelmAddonDefinitionWrite{Name: strings.TrimSpace(arguments[0])}
		applyAddonDefinitionFlags(command, &write)
		if write.DisplayName == "" {
			return withExitCode(exitUsage, fmt.Errorf("--display-name is required: the add-on's name as people read it"))
		}
		created, createError := apiClient.CreateHelmAddonDefinition(command.Context(), write)
		if createError != nil {
			return fmt.Errorf("creating add-on definition %q: %w", write.Name, createError)
		}
		if rendered, renderError := renderStructured(command, created); rendered || renderError != nil {
			return renderError
		}
		printHelmAddonDefinition(command.OutOrStdout(), *created)
		return nil
	},
}

var helmAddonDefinitionsUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Change an organisation add-on definition",
	Long: `Change an organisation add-on definition. Only the flags you pass change: the
command reads the definition, applies them and sends the whole definition back,
because the platform replaces every field on an update. A match flag replaces
that whole list, and an optional text flag passed empty (--category "") clears
the member. Ankra's global definitions are read-only (403).`,
	Example: `  ankra helm addon-definitions update internal-gateway --category ingress
  ankra helm addon-definitions update internal-gateway --match-chart-name internal-gateway,edge-gateway`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		changed := command.Flags().Changed("display-name")
		for _, text := range addonDefinitionTextFlags {
			changed = changed || command.Flags().Changed(text.flag)
		}
		for _, list := range addonDefinitionListFlags {
			changed = changed || command.Flags().Changed(list.flag)
		}
		if !changed {
			return withExitCode(exitUsage, fmt.Errorf("nothing to change: pass at least one flag; see 'ankra helm addon-definitions update --help'"))
		}
		name := strings.TrimSpace(arguments[0])
		current, getError := apiClient.GetHelmAddonDefinition(command.Context(), name)
		if getError != nil {
			return fmt.Errorf("reading add-on definition %q: %w", name, getError)
		}
		write := client.HelmAddonDefinitionWrite{
			DisplayName:          current.DisplayName,
			Description:          current.Description,
			WebsiteURL:           current.WebsiteURL,
			DocumentationURL:     current.DocumentationURL,
			IconURL:              current.IconURL,
			Category:             current.Category,
			MatchChartNames:      current.MatchChartNames,
			MatchKeywords:        current.MatchKeywords,
			MatchHomeURLPrefixes: current.MatchHomeURLPrefixes,
		}
		applyAddonDefinitionFlags(command, &write)
		if write.DisplayName == "" {
			return withExitCode(exitUsage, fmt.Errorf("--display-name cannot be empty"))
		}
		updated, updateError := apiClient.UpdateHelmAddonDefinition(command.Context(), name, write)
		if updateError != nil {
			return fmt.Errorf("updating add-on definition %q: %w", name, updateError)
		}
		if rendered, renderError := renderStructured(command, updated); rendered || renderError != nil {
			return renderError
		}
		printHelmAddonDefinition(command.OutOrStdout(), *updated)
		return nil
	},
}

var helmAddonDefinitionsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an organisation add-on definition",
	Long: `Delete an organisation add-on definition. The charts it grouped stay in their
registries and installed add-ons keep running; the catalog just stops grouping
them under it. It asks first; --yes skips the question and is required when
there is no terminal to ask on. Ankra's global definitions are read-only (403).`,
	Example: `  ankra helm addon-definitions delete internal-gateway --yes`,
	Args:    cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		name := strings.TrimSpace(arguments[0])
		if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
			"Deleting add-on definition %q removes it and its chart links from the organisation's catalog.", name)); confirmError != nil {
			return confirmError
		}
		if deleteError := apiClient.DeleteHelmAddonDefinition(command.Context(), name); deleteError != nil {
			return fmt.Errorf("deleting add-on definition %q: %w", name, deleteError)
		}
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Add-on definition '%s' deleted.\n", name)
		return nil
	},
}

func chartLinkFromFlags(command *cobra.Command) (client.HelmAddonChartLinkRequest, error) {
	chartName, chartError := requiredTextFlag(command, "chart", "the chart's name")
	if chartError != nil {
		return client.HelmAddonChartLinkRequest{}, chartError
	}
	registryURL, registryError := requiredTextFlag(command, "registry-url", "the URL of the registry the chart is in ('ankra helm registries list')")
	if registryError != nil {
		return client.HelmAddonChartLinkRequest{}, registryError
	}
	linkType, _ := command.Flags().GetString("type")
	if linkType != "include" && linkType != "exclude" {
		return client.HelmAddonChartLinkRequest{}, withExitCode(exitUsage, fmt.Errorf("--type must be include or exclude, got %q", linkType))
	}
	return client.HelmAddonChartLinkRequest{ChartName: chartName, RegistryURL: registryURL, LinkType: linkType}, nil
}

func registerChartLinkFlags(command *cobra.Command) {
	command.Flags().String("chart", "", "The chart's name (required)")
	command.Flags().String("registry-url", "", "The URL of the registry the chart is in (required)")
	command.Flags().String("type", "include", "include groups the chart under the add-on; exclude keeps it out")
}

var helmAddonDefinitionsLinkChartCmd = &cobra.Command{
	Use:   "link-chart <name> --chart <chart> --registry-url <url>",
	Short: "Include a chart in an add-on definition, or exclude one",
	Long: `Add a manual link between one chart in one registry and an add-on definition,
overriding the definition's match rules for that chart: --type include (the
default) groups a chart the rules miss under the add-on, --type exclude keeps
out one they match.`,
	Example: `  ankra helm addon-definitions link-chart cert-manager --chart cert-manager-fork --registry-url https://charts.example.com
  ankra helm addon-definitions link-chart cert-manager --chart unrelated --registry-url https://charts.example.com --type exclude`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		link, linkError := chartLinkFromFlags(command)
		if linkError != nil {
			return linkError
		}
		created, createError := apiClient.LinkHelmAddonChart(command.Context(), strings.TrimSpace(arguments[0]), link)
		if createError != nil {
			return fmt.Errorf("linking chart %q to add-on definition %q: %w", link.ChartName, arguments[0], createError)
		}
		if rendered, renderError := renderStructured(command, created); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Chart '%s' from %s is now linked to '%s' (%s).\n",
			created.ChartName, created.RegistryURL, arguments[0], created.LinkType)
		return nil
	},
}

var helmAddonDefinitionsUnlinkChartCmd = &cobra.Command{
	Use:   "unlink-chart <name> --chart <chart> --registry-url <url>",
	Short: "Remove a manual chart link from an add-on definition",
	Long: `Remove one manual chart link, named by its chart, registry and --type (include
by default). The definition's match rules decide for that chart again.`,
	Example: `  ankra helm addon-definitions unlink-chart cert-manager --chart cert-manager-fork --registry-url https://charts.example.com`,
	Args:    cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		link, linkError := chartLinkFromFlags(command)
		if linkError != nil {
			return linkError
		}
		if unlinkError := apiClient.UnlinkHelmAddonChart(command.Context(), strings.TrimSpace(arguments[0]), link); unlinkError != nil {
			return fmt.Errorf("unlinking chart %q from add-on definition %q: %w", link.ChartName, arguments[0], unlinkError)
		}
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Chart '%s' from %s is no longer linked to '%s'.\n", link.ChartName, link.RegistryURL, arguments[0])
		return nil
	},
}

var helmAddonCatalogCmd = &cobra.Command{
	Use:   "addon-catalog",
	Short: "Browse the organisation's charts grouped by add-on",
	Long: `The add-on-first view of the organisation's charts: one entry per add-on
definition with the charts that match it and how many clusters run it, plus an
entry for each chart no definition matches.`,
}

var helmAddonCatalogListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List one page of the add-on catalog",
	Long: `List one page of the add-on catalog. --search, --category, --source and
--connected narrow it; --types takes the comma-separated entry types to include
(name manifest_addon to receive manifest add-ons). -o json prints the platform's
page with its pagination and category facet.`,
	Example: `  ankra helm addon-catalog list
  ankra helm addon-catalog list --search postgres --category databases
  ankra helm addon-catalog list --page 2 --page-size 50 -o json`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		options := client.HelmAddonCatalogOptions{}
		options.Page, _ = command.Flags().GetInt("page")
		options.PageSize, _ = command.Flags().GetInt("page-size")
		options.Search, _ = command.Flags().GetString("search")
		options.Category, _ = command.Flags().GetString("category")
		options.Source, _ = command.Flags().GetString("source")
		options.Connected, _ = command.Flags().GetString("connected")
		options.Types, _ = command.Flags().GetString("types")
		if options.Page < 1 || options.PageSize < 1 || options.PageSize > 100 {
			return withExitCode(exitUsage, fmt.Errorf("--page starts at 1 and --page-size is between 1 and 100"))
		}
		page, listError := apiClient.ListHelmAddonCatalog(command.Context(), options)
		if listError != nil {
			return fmt.Errorf("listing the add-on catalog: %w", listError)
		}
		if rendered, renderError := renderStructured(command, page); rendered || renderError != nil {
			return renderError
		}
		out := command.OutOrStdout()
		rows := []table.Row{}
		for _, entry := range page.Result {
			if entry == nil {
				continue
			}
			rows = append(rows, table.Row{entry.Name, entry.DisplayName, entry.Type, optionalText(entry.Category),
				entry.ChartCount, entry.InstalledCount})
		}
		if len(rows) == 0 {
			_, _ = fmt.Fprintln(out, "No catalog entries match.")
			return nil
		}
		renderRoundedTable(out, table.Row{"Name", "Display name", "Type", "Category", "Charts", "Installed"}, rows)
		_, _ = fmt.Fprintf(out, "Page %d of %d (%d entries).\n", page.Pagination.Page, page.Pagination.TotalPages, page.Pagination.TotalCount)
		return nil
	},
}

var helmAddonCatalogGetCmd = &cobra.Command{
	Use:     "get <name>",
	Short:   "Show one add-on of the catalog: its charts and the clusters that run it",
	Example: `  ankra helm addon-catalog get cert-manager`,
	Args:    cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		detail, getError := apiClient.GetHelmAddonCatalogEntry(command.Context(), arguments[0])
		if getError != nil {
			return fmt.Errorf("reading catalog entry %q: %w", arguments[0], getError)
		}
		if rendered, renderError := renderStructured(command, detail); rendered || renderError != nil {
			return renderError
		}
		out := command.OutOrStdout()
		if detail.Addon != nil {
			printHelmAddonDefinition(out, *detail.Addon)
		}
		_, _ = fmt.Fprintf(out, "\nCharts (%d):\n", len(detail.Charts))
		for _, chart := range detail.Charts {
			if chart == nil {
				continue
			}
			_, _ = fmt.Fprintf(out, "  %s %s from %s (installed on %d)\n", chart.Name, chart.Version, chart.RegistryName, chart.InstalledCount)
		}
		_, _ = fmt.Fprintf(out, "\nClusters running it (%d):\n", len(detail.Clusters))
		for _, placement := range detail.Clusters {
			if placement == nil {
				continue
			}
			_, _ = fmt.Fprintf(out, "  %s: %s %s (stack %s)\n", placement.ClusterName, placement.ChartName,
				optionalText(placement.InstalledVersion), optionalText(placement.StackName))
		}
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(helmAddonDefinitionsListCmd, helmAddonDefinitionsGetCmd, helmAddonDefinitionsLinkChartCmd,
		helmAddonCatalogListCmd, helmAddonCatalogGetCmd)
	registerAddonDefinitionWriteFlags(helmAddonDefinitionsCreateCmd)
	registerAddonDefinitionWriteFlags(helmAddonDefinitionsUpdateCmd)
	helmAddonDefinitionsDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	registerChartLinkFlags(helmAddonDefinitionsLinkChartCmd)
	registerChartLinkFlags(helmAddonDefinitionsUnlinkChartCmd)
	helmAddonDefinitionsCmd.AddCommand(helmAddonDefinitionsListCmd, helmAddonDefinitionsGetCmd, helmAddonDefinitionsCreateCmd,
		helmAddonDefinitionsUpdateCmd, helmAddonDefinitionsDeleteCmd, helmAddonDefinitionsLinkChartCmd, helmAddonDefinitionsUnlinkChartCmd)

	helmAddonCatalogListCmd.Flags().Int("page", 1, "Page number, from 1")
	helmAddonCatalogListCmd.Flags().Int("page-size", 20, "Entries per page (1 to 100)")
	helmAddonCatalogListCmd.Flags().String("search", "", "Free-text search")
	helmAddonCatalogListCmd.Flags().String("category", "", "Only this category")
	helmAddonCatalogListCmd.Flags().String("source", "", "Only entries from this source")
	helmAddonCatalogListCmd.Flags().String("connected", "", "Filter on whether a registry is connected (true or false)")
	helmAddonCatalogListCmd.Flags().String("types", "", "Comma-separated entry types to include")
	helmAddonCatalogCmd.AddCommand(helmAddonCatalogListCmd, helmAddonCatalogGetCmd)

	helmCmd.AddCommand(helmAddonDefinitionsCmd, helmAddonCatalogCmd)
}
