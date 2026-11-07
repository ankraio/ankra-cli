package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// Scopes a role assignment can carry (platform ADR 0007).
const (
	assignmentScopeOrganisation = "organisation"
	assignmentScopeCluster      = "cluster"
	assignmentScopeClusterGroup = "cluster_group"
	assignmentScopeProject      = "project"
)

var assignmentScopes = []string{assignmentScopeOrganisation, assignmentScopeCluster, assignmentScopeClusterGroup, assignmentScopeProject}

var orgAssignCmd = &cobra.Command{
	Use:   "assign <member>",
	Short: "Grant a member a role at a scope (organisation, cluster, cluster group or project)",
	Long: `Grant a member of the current organisation a role at one scope, through
the platform's RBAC assignments API. The member is named by email or user id
(see 'ankra org members').

--role is a built-in role slug (owner, admin, operator, member, viewer) or the
id of an organisation custom role. --scope is one of organisation (the
default), cluster, cluster_group or project; every scope but organisation
needs --scope-id:
  cluster        a cluster name or id
  cluster_group  a cluster group id
  project        a project slug, name or id (the global --project works too)

Examples:
  ankra org assign dev@example.com --role viewer
  ankra org assign dev@example.com --role operator --scope cluster --scope-id prod-eu
  ankra org assign dev@example.com --role admin --scope project --scope-id edge`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		role, _ := cmd.Flags().GetString("role")
		role = strings.TrimSpace(role)
		if role == "" {
			return withExitCode(exitUsage, errors.New("--role is required"))
		}
		scope, _ := cmd.Flags().GetString("scope")
		scope = strings.ToLower(strings.TrimSpace(scope))
		if !containsString(assignmentScopes, scope) {
			return withExitCode(exitUsage, fmt.Errorf("--scope must be one of %s, not %q", strings.Join(assignmentScopes, ", "), scope))
		}
		scopeReference, _ := cmd.Flags().GetString("scope-id")
		scopeID, scopeError := resolveAssignmentScopeID(cmd, scope, strings.TrimSpace(scopeReference))
		if scopeError != nil {
			return scopeError
		}

		organisationID, organisationError := resolveActingOrganisationID()
		if organisationError != nil {
			return fmt.Errorf("%w\nSelect an organisation first with 'ankra org switch <org_id>'", organisationError)
		}
		member, memberError := resolveOrganisationMember(organisationID, args[0])
		if memberError != nil {
			return memberError
		}

		request := client.CreateRoleAssignmentRequest{AnkraUserID: member.userID, ScopeType: scope, ScopeID: scopeID}
		if isLikelyUUID(role) {
			request.RoleID = &role
		} else {
			roleSlug := strings.ToLower(role)
			request.RoleSlug = &roleSlug
		}
		assignment, assignError := apiClient.CreateRoleAssignment(request)
		if assignError != nil {
			return fmt.Errorf("assigning role: %w", assignError)
		}
		if rendered, renderError := renderStructured(cmd, assignment); rendered || renderError != nil {
			return renderError
		}
		scopeLabel := scope
		if scopeID != nil {
			scopeLabel = fmt.Sprintf("%s %s", scope, *scopeID)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Assigned %s to %s at %s (assignment %s)\n", role, member.label, scopeLabel, assignment.ID)
		return nil
	},
}

// resolveAssignmentScopeID turns --scope-id into the id the assignments API
// takes: none for the organisation scope, a cluster name or id, a cluster
// group id, or a project slug, name or id (falling back to the global
// --project for the project scope).
func resolveAssignmentScopeID(cmd *cobra.Command, scope string, reference string) (*string, error) {
	switch scope {
	case assignmentScopeOrganisation:
		if reference != "" {
			return nil, withExitCode(exitUsage, errors.New("--scope-id does not apply to --scope organisation"))
		}
		return nil, nil
	case assignmentScopeProject:
		if reference == "" {
			if projectFlag := cmd.Root().PersistentFlags().Lookup("project"); projectFlag != nil && projectFlag.Changed {
				if projectID := strings.TrimSpace(apiClient.ProjectOverride()); projectID != "" {
					return &projectID, nil
				}
			}
			return nil, withExitCode(exitUsage, errors.New("--scope project needs --scope-id <slug|name|id> (or the global --project)"))
		}
		projectID, projectError := resolveProjectReferenceToID(reference)
		if projectError != nil {
			return nil, projectError
		}
		return &projectID, nil
	}
	if reference == "" {
		return nil, withExitCode(exitUsage, fmt.Errorf("--scope %s needs --scope-id", scope))
	}
	if isLikelyUUID(reference) {
		return &reference, nil
	}
	if scope == assignmentScopeClusterGroup {
		return nil, withExitCode(exitUsage, fmt.Errorf("--scope cluster_group takes the cluster group id, not %q", reference))
	}
	cluster, clusterError := apiClient.GetCluster(reference)
	if clusterError != nil {
		return nil, withExitCode(exitNotFound, fmt.Errorf("cluster %q not found: %w", reference, clusterError))
	}
	return &cluster.ID, nil
}

// organisationMemberReference is a member resolved from an email or a user id.
type organisationMemberReference struct {
	userID string
	label  string
}

// resolveOrganisationMember finds a member of the organisation by user id or
// email (case-insensitively). A member with no account yet (a pending invite)
// has no user id and cannot hold an assignment.
func resolveOrganisationMember(organisationID string, reference string) (organisationMemberReference, error) {
	reference = strings.TrimSpace(reference)
	organisation, fetchError := apiClient.GetOrganisation(organisationID)
	if fetchError != nil {
		return organisationMemberReference{}, fmt.Errorf("fetching organisation members: %w", fetchError)
	}
	for _, member := range organisation.Members {
		userID := trimmedOptional(member.ID)
		email := trimmedOptional(member.Email)
		if userID == "" {
			continue
		}
		if userID == reference || (email != "" && strings.EqualFold(email, reference)) {
			label := email
			if label == "" {
				label = userID
			}
			return organisationMemberReference{userID: userID, label: label}, nil
		}
	}
	return organisationMemberReference{}, withExitCode(exitNotFound,
		fmt.Errorf("no member %q in this organisation; 'ankra org members' lists them", reference))
}

var orgMemberCmd = &cobra.Command{
	Use:   "member",
	Short: "Manage one member of the current organisation",
}

var orgMemberRoleCmd = &cobra.Command{
	Use:   "role <member>",
	Short: "Change a member's organisation role, including to a project-only member",
	Long: `Change the organisation role of a member (named by email or user id).

Valid roles: owner, admin, operator, member, viewer, read-only, project.
--role project makes the member project-only: they reach the one project
named with the global --project (slug, name or id) and nothing else of the
organisation, with the project role --project-role (admin, operator, member
or viewer; member by default). Needs projects enabled for the organisation.

Examples:
  ankra org member role dev@example.com --role viewer
  ankra org member role contractor@example.com --role project --project edge --project-role operator`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		role, _ := cmd.Flags().GetString("role")
		role = strings.ToLower(strings.TrimSpace(role))
		if !isValidAssignableRole(role) {
			return withExitCode(exitUsage, fmt.Errorf(
				"invalid role %q; valid roles: %s", role, strings.Join(assignableRoles, ", ")))
		}
		projectID, projectRole, grantError := inviteProjectGrant(cmd, role)
		if grantError != nil {
			return grantError
		}
		organisationID, organisationError := resolveActingOrganisationID()
		if organisationError != nil {
			return fmt.Errorf("%w\nSelect an organisation first with 'ankra org switch <org_id>'", organisationError)
		}
		member, memberError := resolveOrganisationMember(organisationID, args[0])
		if memberError != nil {
			return memberError
		}
		request := client.UpdateMemberRoleRequest{
			UserID:         member.userID,
			OrganisationID: organisationID,
			Role:           toLegacyWireRole(role),
			ProjectID:      projectID,
		}
		if projectID != nil {
			request.ProjectRoleSlug = &projectRole
		}
		result, updateError := apiClient.UpdateMemberRole(request)
		if errors.Is(updateError, client.ErrMemberRoleRouteMissing) {
			return withExitCode(exitNotFound, updateError)
		}
		if updateError != nil {
			return fmt.Errorf("changing role: %w", updateError)
		}
		if projectID != nil {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is now a project-only member: %s in project %s\n", member.label, projectRole, *projectID)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s now has role %s\n", member.label, role)
		}
		if result.Message != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Message: %s\n", result.Message)
		}
		return nil
	},
}

func init() {
	orgAssignCmd.Flags().String("role", "", "Built-in role slug (owner, admin, operator, member, viewer) or a custom role id")
	orgAssignCmd.Flags().String("scope", assignmentScopeOrganisation, "Scope of the assignment: organisation, cluster, cluster_group or project")
	orgAssignCmd.Flags().String("scope-id", "", "The cluster (name or id), cluster group (id) or project (slug, name or id) the role applies to")
	registerStructuredOutputFlags(orgAssignCmd)

	orgMemberRoleCmd.Flags().String("role", "", "New role (owner, admin, operator, member, viewer, read-only, project)")
	orgMemberRoleCmd.Flags().String("project-role", "member", "With --role project: the role inside the project (admin, operator, member or viewer)")

	orgMemberCmd.AddCommand(orgMemberRoleCmd)
	orgCmd.AddCommand(orgAssignCmd)
	orgCmd.AddCommand(orgMemberCmd)
}
