package cmd

// Cluster groups and scoped role assignments (platform RBAC, backend ADR
// 0007 extension): `ankra org cluster-groups ...` manages named sets of
// clusters (static list or label selector), `ankra org assign` grants a
// role over the organisation, one cluster, or a cluster group, and
// `ankra org roles create` defines custom roles that may bundle Kubernetes
// access (auto-provisioned across the assignment's scope).

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterGroupsCmd = &cobra.Command{
	Use:     "cluster-groups",
	Aliases: []string{"cluster-group", "groups"},
	Short:   "Manage cluster groups (named sets of clusters for role scoping)",
}

var clusterGroupsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's cluster groups",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		groups, err := apiClient.ListClusterGroups()
		if err != nil {
			return fmt.Errorf("listing cluster groups: %w", err)
		}
		if rendered, err := renderStructured(cmd, groups); rendered || err != nil {
			return err
		}
		if len(groups) == 0 {
			fmt.Println("No cluster groups found. Create one with 'ankra org cluster-groups create'.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Slug", "Membership", "Selector", "Clusters"})
		for _, group := range groups {
			memberCount := "-"
			if members, previewErr := apiClient.PreviewClusterGroup(group.ID); previewErr == nil {
				memberCount = fmt.Sprintf("%d", len(members))
			}
			t.AppendRow(table.Row{
				group.Name,
				group.Slug,
				group.MembershipMode,
				formatSelector(group.Selector),
				memberCount,
			})
		}
		t.Render()
		return nil
	},
}

var clusterGroupsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a cluster group",
	Long: `Create a cluster group.

Without --selector the group is static: pin clusters afterwards with
'ankra org cluster-groups add-cluster'. With --selector the group is
dynamic: clusters whose labels match every key=value pair are members
automatically (the environment label seeds from the cluster's environment).

Examples:
  ankra org cluster-groups create "Production fleet" --selector environment=production
  ankra org cluster-groups create staging-pool`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		selectorPairs, _ := cmd.Flags().GetStringSlice("selector")
		slugFlag, _ := cmd.Flags().GetString("slug")

		selector, err := parseSelectorPairs(selectorPairs)
		if err != nil {
			return withExitCode(exitUsage, err)
		}
		slug := slugFlag
		if slug == "" {
			slug = slugifyGroupName(name)
		}
		request := client.CreateClusterGroupRequest{
			Name:           name,
			Slug:           slug,
			MembershipMode: "static",
		}
		if len(selector) > 0 {
			request.MembershipMode = "dynamic"
			request.Selector = selector
		}
		created, err := apiClient.CreateClusterGroup(request)
		if err != nil {
			return fmt.Errorf("creating cluster group: %w", err)
		}
		fmt.Printf("Cluster group %q created (slug: %s, membership: %s).\n",
			created.Name, created.Slug, created.MembershipMode)
		if created.MembershipMode == "static" {
			fmt.Println("Pin clusters with 'ankra org cluster-groups add-cluster " + created.Slug + " <cluster-name>'.")
		}
		return nil
	},
}

var clusterGroupsAddClusterCmd = &cobra.Command{
	Use:   "add-cluster <group> <cluster-name>",
	Short: "Add a cluster to a static cluster group",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		group, err := resolveClusterGroup(args[0])
		if err != nil {
			return err
		}
		if group.MembershipMode != "static" {
			return withExitCode(exitUsage, fmt.Errorf(
				"group %q membership is defined by its label selector; adjust labels or use set-selector", group.Slug))
		}
		cluster, err := apiClient.GetCluster(args[1])
		if err != nil {
			return fmt.Errorf("resolving cluster %q: %w", args[1], err)
		}
		currentMembers, err := apiClient.PreviewClusterGroup(group.ID)
		if err != nil {
			return fmt.Errorf("reading current group members: %w", err)
		}
		for _, memberID := range currentMembers {
			if memberID == cluster.ID {
				fmt.Printf("Cluster %q is already in group %q.\n", cluster.Name, group.Slug)
				return nil
			}
		}
		if err := apiClient.SetClusterGroupMembers(group.ID, append(currentMembers, cluster.ID)); err != nil {
			return fmt.Errorf("adding cluster to group: %w", err)
		}
		fmt.Printf("Cluster %q added to group %q (%d clusters).\n", cluster.Name, group.Slug, len(currentMembers)+1)
		return nil
	},
}

var clusterGroupsSetSelectorCmd = &cobra.Command{
	Use:   "set-selector <group> <key=value> [key=value ...]",
	Short: "Switch a group to dynamic label-selector membership",
	Long: `Switch a cluster group to dynamic membership: clusters whose labels match
every key=value pair become members automatically, and pinned static
members are cleared.

Example:
  ankra org cluster-groups set-selector prod-fleet environment=production`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		group, err := resolveClusterGroup(args[0])
		if err != nil {
			return err
		}
		selector, err := parseSelectorPairs(args[1:])
		if err != nil {
			return withExitCode(exitUsage, err)
		}
		if err := apiClient.SetClusterGroupSelector(group.ID, selector); err != nil {
			return fmt.Errorf("setting group selector: %w", err)
		}
		members, previewErr := apiClient.PreviewClusterGroup(group.ID)
		if previewErr != nil {
			fmt.Printf("Selector set on group %q.\n", group.Slug)
			return nil
		}
		fmt.Printf("Selector set on group %q; %d clusters currently match.\n", group.Slug, len(members))
		return nil
	},
}

var clusterGroupsPreviewCmd = &cobra.Command{
	Use:   "preview <group>",
	Short: "Show the clusters a group currently resolves to",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		group, err := resolveClusterGroup(args[0])
		if err != nil {
			return err
		}
		memberIDs, err := apiClient.PreviewClusterGroup(group.ID)
		if err != nil {
			return fmt.Errorf("resolving group membership: %w", err)
		}
		if rendered, err := renderStructured(cmd, memberIDs); rendered || err != nil {
			return err
		}
		if len(memberIDs) == 0 {
			fmt.Printf("Group %q currently matches no clusters.\n", group.Slug)
			return nil
		}
		namesByID := clusterNamesByID()
		fmt.Printf("Group %q resolves to %d cluster(s):\n", group.Slug, len(memberIDs))
		for _, memberID := range memberIDs {
			if name, known := namesByID[memberID]; known {
				fmt.Printf("  %s (%s)\n", name, memberID)
			} else {
				fmt.Printf("  %s\n", memberID)
			}
		}
		return nil
	},
}

var orgRolesCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a custom role, optionally bundling Kubernetes access",
	Long: `Create an organisation custom role.

--permission grants platform permissions (repeatable; 'domain.action'
strings). --kube-access bundles Kubernetes access that is provisioned
automatically on every cluster an assignment of this role covers
(repeatable; '<role>' for whole-cluster or '<role>:<namespace>' for one
namespace, with role one of view, edit, admin, cluster-admin).

Examples:
  ankra org roles create "Platform on-call" \
    --permission clusters.operate --permission stacks.deploy \
    --kube-access edit:app-services --kube-access view`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		permissions, _ := cmd.Flags().GetStringSlice("permission")
		kubeAccess, _ := cmd.Flags().GetStringSlice("kube-access")
		slugFlag, _ := cmd.Flags().GetString("slug")

		kubeGrants, err := parseKubeAccessFlags(kubeAccess)
		if err != nil {
			return withExitCode(exitUsage, err)
		}
		slug := slugFlag
		if slug == "" {
			slug = slugifyGroupName(name)
		}
		created, err := apiClient.CreateCustomRole(client.CreateCustomRoleRequest{
			Slug:        slug,
			Name:        name,
			Permissions: permissions,
			KubeGrants:  kubeGrants,
		})
		if err != nil {
			return fmt.Errorf("creating role: %w", err)
		}
		fmt.Printf("Role %q created (%d permissions, %d Kubernetes access levels).\n",
			created.Name, len(created.Permissions), len(kubeGrants))
		return nil
	},
}

var orgAssignCmd = &cobra.Command{
	Use:   "assign <member-email>",
	Short: "Assign a role to a member at a scope",
	Long: `Assign a built-in or custom role to a member.

--scope selects where the role applies:
  org (default)      the whole organisation
  cluster:<name>     one cluster
  group:<slug>       every cluster in a cluster group

Examples:
  ankra org assign dev@example.com --role operator --scope group:prod-fleet
  ankra org assign dev@example.com --role platform-on-call --scope cluster:staging-1
  ankra org assign lead@example.com --role admin`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		roleName, _ := cmd.Flags().GetString("role")
		scopeFlag, _ := cmd.Flags().GetString("scope")
		if roleName == "" {
			return withExitCode(exitUsage, errors.New("--role is required"))
		}

		memberID, err := resolveMemberID(args[0])
		if err != nil {
			return err
		}
		request := client.CreateRoleAssignmentRequest{AnkraUserID: memberID}

		if err := resolveAssignmentRole(roleName, &request); err != nil {
			return err
		}
		if err := resolveAssignmentScope(scopeFlag, &request); err != nil {
			return err
		}

		created, err := apiClient.CreateRoleAssignment(request)
		if err != nil {
			return fmt.Errorf("creating assignment: %w", err)
		}
		fmt.Printf("Assigned %s to %s (%s scope). Assignment ID: %s\n",
			roleName, args[0], request.ScopeType, created.ID)
		return nil
	},
}

var orgAssignmentsCmd = &cobra.Command{
	Use:   "assignments <member-email>",
	Short: "List a member's role assignments",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		memberID, err := resolveMemberID(args[0])
		if err != nil {
			return err
		}
		assignments, err := apiClient.ListMemberAssignments(memberID)
		if err != nil {
			return fmt.Errorf("listing assignments: %w", err)
		}
		if rendered, err := renderStructured(cmd, assignments); rendered || err != nil {
			return err
		}
		if len(assignments) == 0 {
			fmt.Println("No explicit assignments; the member has their base membership role only.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Role", "Scope", "Scope ID"})
		for _, assignment := range assignments {
			role := "custom"
			if assignment.RoleSlug != nil {
				role = *assignment.RoleSlug
			} else if assignment.RoleID != nil {
				role = "custom:" + *assignment.RoleID
			}
			scopeID := "-"
			if assignment.ScopeID != nil {
				scopeID = *assignment.ScopeID
			}
			t.AppendRow(table.Row{assignment.ID, role, assignment.ScopeType, scopeID})
		}
		t.Render()
		return nil
	},
}

var orgUnassignCmd = &cobra.Command{
	Use:   "unassign <assignment-id>",
	Short: "Revoke a role assignment",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if err := apiClient.DeleteRoleAssignment(args[0]); err != nil {
			return fmt.Errorf("revoking assignment: %w", err)
		}
		fmt.Printf("Assignment %s revoked.\n", args[0])
		return nil
	},
}

// --- helpers ---------------------------------------------------------------

func formatSelector(selector map[string]string) string {
	if len(selector) == 0 {
		return "-"
	}
	var pairs []string
	for key, value := range selector {
		pairs = append(pairs, key+"="+value)
	}
	return strings.Join(pairs, ",")
}

func parseSelectorPairs(pairs []string) (map[string]string, error) {
	selector := map[string]string{}
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !found || key == "" || value == "" {
			return nil, fmt.Errorf("invalid selector %q; expected key=value", pair)
		}
		selector[key] = value
	}
	return selector, nil
}

func slugifyGroupName(name string) string {
	lowered := strings.ToLower(name)
	var builder strings.Builder
	lastHyphen := true
	for _, character := range lowered {
		isAlphanumeric := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if isAlphanumeric {
			builder.WriteRune(character)
			lastHyphen = false
		} else if !lastHyphen {
			builder.WriteRune('-')
			lastHyphen = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if len(slug) > 50 {
		slug = strings.Trim(slug[:50], "-")
	}
	return slug
}

// parseKubeAccessFlags parses '<role>' or '<role>:<namespace>' values.
func parseKubeAccessFlags(values []string) ([]client.KubeGrant, error) {
	validRoles := map[string]bool{"view": true, "edit": true, "admin": true, "cluster-admin": true}
	var grants []client.KubeGrant
	for _, value := range values {
		role, namespace, hasNamespace := strings.Cut(value, ":")
		role = strings.TrimSpace(role)
		if !validRoles[role] {
			return nil, fmt.Errorf(
				"invalid kube access %q; expected view|edit|admin|cluster-admin, optionally ':<namespace>'", value)
		}
		grant := client.KubeGrant{Scope: "cluster", K8sRole: role}
		if hasNamespace {
			namespace = strings.TrimSpace(namespace)
			if namespace == "" {
				return nil, fmt.Errorf("invalid kube access %q; namespace after ':' is empty", value)
			}
			grant.Scope = "namespace"
			grant.Namespace = &namespace
		}
		grants = append(grants, grant)
	}
	return grants, nil
}

// resolveClusterGroup matches a group by slug, name, or id.
func resolveClusterGroup(reference string) (*client.ClusterGroup, error) {
	groups, err := apiClient.ListClusterGroups()
	if err != nil {
		return nil, fmt.Errorf("listing cluster groups: %w", err)
	}
	trimmed := strings.TrimSpace(reference)
	for index := range groups {
		if strings.EqualFold(groups[index].Slug, trimmed) ||
			strings.EqualFold(groups[index].Name, trimmed) ||
			groups[index].ID == trimmed {
			return &groups[index], nil
		}
	}
	var available []string
	for _, group := range groups {
		available = append(available, group.Slug)
	}
	hint := "No cluster groups exist yet."
	if len(available) > 0 {
		hint = "Available groups: " + strings.Join(available, ", ")
	}
	return nil, fmt.Errorf("cluster group %q not found. %s", reference, hint)
}

// resolveMemberID maps a member email (or a raw user id) to the platform
// user id assignments target.
func resolveMemberID(reference string) (string, error) {
	orgID, err := resolveOrganisationID()
	if err != nil {
		return "", fmt.Errorf("%w\nSelect an organisation first with 'ankra org switch <org_id>'", err)
	}
	users, err := apiClient.ListOrganisationUsers(orgID)
	if err != nil {
		return "", fmt.Errorf("listing organisation members: %w", err)
	}
	trimmed := strings.TrimSpace(reference)
	for _, user := range users {
		if user.ID == nil {
			continue
		}
		if *user.ID == trimmed || (user.Email != nil && strings.EqualFold(*user.Email, trimmed)) {
			return *user.ID, nil
		}
	}
	return "", fmt.Errorf("member %q not found in the organisation", reference)
}

// resolveAssignmentRole fills role_slug (built-in) or role_id (custom,
// matched by slug or name against the server's role list).
func resolveAssignmentRole(roleName string, request *client.CreateRoleAssignmentRequest) error {
	trimmed := strings.ToLower(strings.TrimSpace(roleName))
	roles, err := apiClient.ListRoles()
	if err != nil {
		return fmt.Errorf("listing roles: %w", err)
	}
	for index := range roles {
		if strings.EqualFold(roles[index].Slug, trimmed) || strings.EqualFold(roles[index].Name, roleName) {
			if roles[index].Builtin {
				slug := roles[index].Slug
				request.RoleSlug = &slug
			} else {
				request.RoleID = roles[index].ID
			}
			return nil
		}
	}
	var available []string
	for _, role := range roles {
		available = append(available, role.Slug)
	}
	return fmt.Errorf("role %q not found. Available roles: %s", roleName, strings.Join(available, ", "))
}

// resolveAssignmentScope parses 'org', 'cluster:<name>', or 'group:<slug>'.
func resolveAssignmentScope(scopeFlag string, request *client.CreateRoleAssignmentRequest) error {
	trimmed := strings.TrimSpace(scopeFlag)
	if trimmed == "" || strings.EqualFold(trimmed, "org") || strings.EqualFold(trimmed, "organisation") {
		request.ScopeType = "organisation"
		return nil
	}
	kind, target, found := strings.Cut(trimmed, ":")
	if !found || strings.TrimSpace(target) == "" {
		return withExitCode(exitUsage, fmt.Errorf(
			"invalid --scope %q; expected org, cluster:<name>, or group:<slug>", scopeFlag))
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "cluster":
		cluster, err := apiClient.GetCluster(strings.TrimSpace(target))
		if err != nil {
			return fmt.Errorf("resolving cluster %q: %w", target, err)
		}
		request.ScopeType = "cluster"
		request.ScopeID = &cluster.ID
		return nil
	case "group", "cluster-group", "cluster_group":
		group, err := resolveClusterGroup(strings.TrimSpace(target))
		if err != nil {
			return err
		}
		request.ScopeType = "cluster_group"
		request.ScopeID = &group.ID
		return nil
	default:
		return withExitCode(exitUsage, fmt.Errorf(
			"invalid --scope kind %q; expected org, cluster:<name>, or group:<slug>", kind))
	}
}

// clusterNamesByID builds a best-effort id -> name map for display.
func clusterNamesByID() map[string]string {
	names := map[string]string{}
	clusters, err := listAllClusters()
	if err != nil {
		return names
	}
	for _, cluster := range clusters {
		names[cluster.ID] = cluster.Name
	}
	return names
}

func init() {
	clusterGroupsCreateCmd.Flags().StringSlice("selector", nil,
		"Label selector pair key=value (repeatable); makes the group dynamic")
	clusterGroupsCreateCmd.Flags().String("slug", "", "Slug for the group (defaults to a slugified name)")

	clusterGroupsCmd.AddCommand(clusterGroupsListCmd)
	clusterGroupsCmd.AddCommand(clusterGroupsCreateCmd)
	clusterGroupsCmd.AddCommand(clusterGroupsAddClusterCmd)
	clusterGroupsCmd.AddCommand(clusterGroupsSetSelectorCmd)
	clusterGroupsCmd.AddCommand(clusterGroupsPreviewCmd)
	registerStructuredOutputFlags(clusterGroupsListCmd, clusterGroupsPreviewCmd)

	orgRolesCreateCmd.Flags().StringSlice("permission", nil,
		"Platform permission 'domain.action' (repeatable)")
	orgRolesCreateCmd.Flags().StringSlice("kube-access", nil,
		"Kubernetes access '<role>' or '<role>:<namespace>' (repeatable; view|edit|admin|cluster-admin)")
	orgRolesCreateCmd.Flags().String("slug", "", "Slug for the role (defaults to a slugified name)")
	orgRolesCmd.AddCommand(orgRolesCreateCmd)

	orgAssignCmd.Flags().String("role", "", "Role slug or custom role name to assign")
	orgAssignCmd.Flags().String("scope", "org", "Assignment scope: org, cluster:<name>, or group:<slug>")
	registerStructuredOutputFlags(orgAssignmentsCmd)

	orgCmd.AddCommand(clusterGroupsCmd)
	orgCmd.AddCommand(orgAssignCmd)
	orgCmd.AddCommand(orgAssignmentsCmd)
	orgCmd.AddCommand(orgUnassignCmd)
}
