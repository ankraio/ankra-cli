package client

// Add-on definitions and the add-on catalog (ankra-hq41xb.15): the curated
// descriptions of an add-on and which charts it matches, under
// /api/v1/org/helm/addon-definitions, and the add-on-first view of the
// organisation's charts under /api/v1/org/helm/addon-catalog.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// HelmAddonDefinition is one add-on definition. Source says whose it is:
// Ankra's global definitions are read-only.
type HelmAddonDefinition struct {
	ID                   string   `json:"id" yaml:"id"`
	Name                 string   `json:"name" yaml:"name"`
	DisplayName          string   `json:"display_name" yaml:"display_name"`
	Description          *string  `json:"description" yaml:"description"`
	WebsiteURL           *string  `json:"website_url" yaml:"website_url"`
	DocumentationURL     *string  `json:"documentation_url" yaml:"documentation_url"`
	IconURL              *string  `json:"icon_url" yaml:"icon_url"`
	Category             *string  `json:"category" yaml:"category"`
	MatchChartNames      []string `json:"match_chart_names" yaml:"match_chart_names"`
	MatchKeywords        []string `json:"match_keywords" yaml:"match_keywords"`
	MatchHomeURLPrefixes []string `json:"match_home_url_prefixes" yaml:"match_home_url_prefixes"`
	Source               string   `json:"source" yaml:"source"`
	CreatedAt            string   `json:"created_at" yaml:"created_at"`
	UpdatedAt            string   `json:"updated_at" yaml:"updated_at"`
}

// HelmAddonChartLink is a manual include or exclude of one chart in one
// registry, overriding a definition's match rules for that chart.
type HelmAddonChartLink struct {
	ID          string `json:"id" yaml:"id"`
	ChartName   string `json:"chart_name" yaml:"chart_name"`
	RegistryURL string `json:"registry_url" yaml:"registry_url"`
	LinkType    string `json:"link_type" yaml:"link_type"`
	Source      string `json:"source" yaml:"source"`
	CreatedAt   string `json:"created_at" yaml:"created_at"`
}

// HelmAddonDefinitionDetail is one definition with its manual chart links.
type HelmAddonDefinitionDetail struct {
	HelmAddonDefinition `yaml:",inline"`
	ChartLinks          []HelmAddonChartLink `json:"chart_links" yaml:"chart_links"`
}

// HelmAddonDefinitionWrite is the create and update body. Name is sent on
// create only: on update the path names the definition. The update replaces
// the definition's fields, so a caller sends the whole definition.
type HelmAddonDefinitionWrite struct {
	Name                 string   `json:"name,omitempty"`
	DisplayName          string   `json:"display_name"`
	Description          *string  `json:"description"`
	WebsiteURL           *string  `json:"website_url"`
	DocumentationURL     *string  `json:"documentation_url"`
	IconURL              *string  `json:"icon_url"`
	Category             *string  `json:"category"`
	MatchChartNames      []string `json:"match_chart_names"`
	MatchKeywords        []string `json:"match_keywords"`
	MatchHomeURLPrefixes []string `json:"match_home_url_prefixes"`
}

func (write *HelmAddonDefinitionWrite) withoutNilLists() HelmAddonDefinitionWrite {
	body := *write
	if body.MatchChartNames == nil {
		body.MatchChartNames = []string{}
	}
	if body.MatchKeywords == nil {
		body.MatchKeywords = []string{}
	}
	if body.MatchHomeURLPrefixes == nil {
		body.MatchHomeURLPrefixes = []string{}
	}
	return body
}

// HelmAddonChartLinkRequest names one chart link: the chart, its registry
// and whether the link includes or excludes it.
type HelmAddonChartLinkRequest struct {
	ChartName   string `json:"chart_name"`
	RegistryURL string `json:"registry_url"`
	LinkType    string `json:"link_type"`
}

type helmAddonDefinitionListing struct {
	Result []*HelmAddonDefinition `json:"result"`
}

// ListHelmAddonDefinitions reads the definitions visible to the
// organisation: Ankra's global ones and its own.
func (c *Client) ListHelmAddonDefinitions(ctx context.Context) ([]HelmAddonDefinition, error) {
	var listing helmAddonDefinitionListing
	address := c.servicesURL("/api/v1/org/helm/addon-definitions", nil)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &listing); getError != nil {
		return nil, getError
	}
	definitions := make([]HelmAddonDefinition, 0, len(listing.Result))
	for _, definition := range listing.Result {
		if definition != nil {
			definitions = append(definitions, *definition)
		}
	}
	return definitions, nil
}

func dropNilChartLinks(links []*HelmAddonChartLink) []HelmAddonChartLink {
	kept := make([]HelmAddonChartLink, 0, len(links))
	for _, link := range links {
		if link != nil {
			kept = append(kept, *link)
		}
	}
	return kept
}

type helmAddonDefinitionDetailWire struct {
	HelmAddonDefinition
	ChartLinks []*HelmAddonChartLink `json:"chart_links"`
}

// GetHelmAddonDefinition reads one definition with its chart links.
func (c *Client) GetHelmAddonDefinition(ctx context.Context, definitionName string) (*HelmAddonDefinitionDetail, error) {
	var wire helmAddonDefinitionDetailWire
	address := c.servicesURL("/api/v1/org/helm/addon-definitions/%s", nil, definitionName)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &wire); getError != nil {
		return nil, getError
	}
	return &HelmAddonDefinitionDetail{HelmAddonDefinition: wire.HelmAddonDefinition, ChartLinks: dropNilChartLinks(wire.ChartLinks)}, nil
}

// CreateHelmAddonDefinition creates an organisation add-on definition. A
// name already in use answers 409.
func (c *Client) CreateHelmAddonDefinition(ctx context.Context, definition HelmAddonDefinitionWrite) (*HelmAddonDefinition, error) {
	var created HelmAddonDefinition
	address := c.servicesURL("/api/v1/org/helm/addon-definitions", nil)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, definition.withoutNilLists(), &created); postError != nil {
		return nil, postError
	}
	return &created, nil
}

// UpdateHelmAddonDefinition replaces an organisation add-on definition's
// fields. Ankra's global definitions are read-only and answer 403.
func (c *Client) UpdateHelmAddonDefinition(ctx context.Context, definitionName string, definition HelmAddonDefinitionWrite) (*HelmAddonDefinition, error) {
	definition.Name = ""
	var updated HelmAddonDefinition
	address := c.servicesURL("/api/v1/org/helm/addon-definitions/%s", nil, definitionName)
	if putError := c.sendJSONContext(ctx, http.MethodPut, address, definition.withoutNilLists(), &updated); putError != nil {
		return nil, putError
	}
	return &updated, nil
}

// DeleteHelmAddonDefinition deletes an organisation add-on definition.
func (c *Client) DeleteHelmAddonDefinition(ctx context.Context, definitionName string) error {
	address := c.servicesURL("/api/v1/org/helm/addon-definitions/%s", nil, definitionName)
	return c.sendJSONContext(ctx, http.MethodDelete, address, nil, nil)
}

// LinkHelmAddonChart adds an include or exclude link between a chart in a
// registry and a definition.
func (c *Client) LinkHelmAddonChart(ctx context.Context, definitionName string, link HelmAddonChartLinkRequest) (*HelmAddonChartLink, error) {
	var created HelmAddonChartLink
	address := c.servicesURL("/api/v1/org/helm/addon-definitions/%s/chart-links", nil, definitionName)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, link, &created); postError != nil {
		return nil, postError
	}
	return &created, nil
}

// UnlinkHelmAddonChart removes one chart link, which the route identifies by
// query parameters.
func (c *Client) UnlinkHelmAddonChart(ctx context.Context, definitionName string, link HelmAddonChartLinkRequest) error {
	query := url.Values{}
	query.Set("chart_name", link.ChartName)
	query.Set("registry_url", link.RegistryURL)
	query.Set("link_type", link.LinkType)
	address := c.servicesURL("/api/v1/org/helm/addon-definitions/%s/chart-links", query, definitionName)
	return c.sendJSONContext(ctx, http.MethodDelete, address, nil, nil)
}

// HelmAddonCatalogChart is one chart grouped under a catalog entry.
// IsPrivate says the chart's registry pulls with a credential, without
// naming it.
type HelmAddonCatalogChart struct {
	Name           string  `json:"name" yaml:"name"`
	Version        string  `json:"version" yaml:"version"`
	RegistryName   string  `json:"registry_name" yaml:"registry_name"`
	RegistryURL    string  `json:"registry_url" yaml:"registry_url"`
	Description    *string `json:"description" yaml:"description"`
	Icon           *string `json:"icon" yaml:"icon"`
	IsDeprecated   bool    `json:"is_deprecated" yaml:"is_deprecated"`
	IsPrivate      bool    `json:"is_private" yaml:"is_private"`
	IsGlobal       bool    `json:"is_global" yaml:"is_global"`
	InstalledCount int     `json:"installed_count" yaml:"installed_count"`
}

// HelmAddonCatalogEntry is one catalog entry: a curated add-on definition
// with its matched charts (type "addon"), or a chart no definition matches
// (type "chart").
type HelmAddonCatalogEntry struct {
	Type             string                   `json:"type" yaml:"type"`
	Name             string                   `json:"name" yaml:"name"`
	DisplayName      string                   `json:"display_name" yaml:"display_name"`
	Description      *string                  `json:"description" yaml:"description"`
	WebsiteURL       *string                  `json:"website_url" yaml:"website_url"`
	DocumentationURL *string                  `json:"documentation_url" yaml:"documentation_url"`
	IconURL          *string                  `json:"icon_url" yaml:"icon_url"`
	Category         *string                  `json:"category" yaml:"category"`
	Source           *string                  `json:"source" yaml:"source"`
	ChartCount       int                      `json:"chart_count" yaml:"chart_count"`
	InstalledCount   int                      `json:"installed_count" yaml:"installed_count"`
	Charts           []*HelmAddonCatalogChart `json:"charts" yaml:"charts"`
	ManifestAddon    map[string]any           `json:"manifest_addon,omitempty" yaml:"manifest_addon,omitempty"`
}

// HelmAddonCatalogCategory is one entry of the category facet.
type HelmAddonCatalogCategory struct {
	Name  string `json:"name" yaml:"name"`
	Count int    `json:"count" yaml:"count"`
}

// HelmAddonCatalogPage is one page of the add-on catalog.
type HelmAddonCatalogPage struct {
	Result     []*HelmAddonCatalogEntry   `json:"result" yaml:"result"`
	Pagination Pagination                 `json:"pagination" yaml:"pagination"`
	Categories []HelmAddonCatalogCategory `json:"categories" yaml:"categories"`
	// CategoryOptions is every category an entry can be listed under.
	CategoryOptions []string `json:"category_options" yaml:"category_options"`
}

// HelmAddonCatalogOptions pages and narrows the catalog. An empty filter is
// not sent.
type HelmAddonCatalogOptions struct {
	Page      int
	PageSize  int
	Search    string
	Category  string
	Source    string
	Connected string
	Types     string
}

func (options HelmAddonCatalogOptions) query() url.Values {
	query := url.Values{}
	if options.Page > 0 {
		query.Set("page", strconv.Itoa(options.Page))
	}
	if options.PageSize > 0 {
		query.Set("page_size", strconv.Itoa(options.PageSize))
	}
	for name, value := range map[string]string{
		"search": options.Search, "category": options.Category, "source": options.Source,
		"connected": options.Connected, "types": options.Types,
	} {
		if value != "" {
			query.Set(name, value)
		}
	}
	return query
}

// ListHelmAddonCatalog reads one page of the add-on catalog.
func (c *Client) ListHelmAddonCatalog(ctx context.Context, options HelmAddonCatalogOptions) (*HelmAddonCatalogPage, error) {
	var page HelmAddonCatalogPage
	address := c.servicesURL("/api/v1/org/helm/addon-catalog", options.query())
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); getError != nil {
		return nil, getError
	}
	return &page, nil
}

// HelmAddonCatalogCluster is one cluster placement of a chart matched to an
// add-on.
type HelmAddonCatalogCluster struct {
	ClusterID        string  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName      string  `json:"cluster_name" yaml:"cluster_name"`
	ClusterKind      string  `json:"cluster_kind" yaml:"cluster_kind"`
	AddonName        string  `json:"addon_name" yaml:"addon_name"`
	StackName        *string `json:"stack_name" yaml:"stack_name"`
	InstalledVersion *string `json:"installed_version" yaml:"installed_version"`
	ChartName        string  `json:"chart_name" yaml:"chart_name"`
	RegistryName     string  `json:"registry_name" yaml:"registry_name"`
}

// HelmAddonCatalogDetail is one add-on of the catalog with its matching
// charts and the organisation's clusters that run it.
type HelmAddonCatalogDetail struct {
	Addon          *HelmAddonDefinition       `json:"addon" yaml:"addon"`
	Charts         []*HelmAddonCatalogChart   `json:"charts" yaml:"charts"`
	Clusters       []*HelmAddonCatalogCluster `json:"clusters" yaml:"clusters"`
	InstalledCount int                        `json:"installed_count" yaml:"installed_count"`
	// CategoryOptions is every category an entry can be listed under.
	CategoryOptions []string `json:"category_options" yaml:"category_options"`
}

// GetHelmAddonCatalogEntry reads one add-on of the catalog.
func (c *Client) GetHelmAddonCatalogEntry(ctx context.Context, definitionName string) (*HelmAddonCatalogDetail, error) {
	var detail HelmAddonCatalogDetail
	address := c.servicesURL("/api/v1/org/helm/addon-catalog/%s", nil, definitionName)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &detail); getError != nil {
		return nil, getError
	}
	return &detail, nil
}
