package cmd

// Role vocabulary for the CLI (platform RBAC, backend ADR 0007). The invite
// endpoint keeps the frozen three-value wire contract (member/admin/
// read-only) until the RBAC assignments API ships, so wider role names the
// CLI accepts are mapped onto their closest legacy value before the request.

import "strings"

// assignableRoles are the role names `ankra org invite --role` accepts.
var assignableRoles = []string{"owner", "admin", "operator", "member", "viewer", "read-only"}

// roleDescriptions documents each assignable role for `ankra org roles`.
var roleDescriptions = map[string]string{
	"owner":     "Full control including organisation deletion (aliases to admin on invite)",
	"admin":     "Full control except deleting the organisation",
	"operator":  "Day-2 operations: deploy, operate clusters, act on alerts (aliases to member on invite)",
	"member":    "Build and deploy stacks, variables, and applications",
	"viewer":    "Read-only access plus ask-mode AI",
	"read-only": "Legacy alias of viewer",
}

// isValidAssignableRole reports whether the value names a role the CLI
// accepts for invites.
func isValidAssignableRole(role string) bool {
	for _, candidate := range assignableRoles {
		if candidate == role {
			return true
		}
	}
	return false
}

// toLegacyWireRole maps a role name onto the value the invite/role
// endpoints accept (member/admin/read-only), aliasing the new slugs.
func toLegacyWireRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner", "admin":
		return "admin"
	case "viewer", "read-only":
		return "read-only"
	default:
		return "member"
	}
}
