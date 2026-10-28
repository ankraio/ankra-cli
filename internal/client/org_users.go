package client

// Organisation membership lookup. (The RBAC admin surface — roles,
// assignments, cluster groups — that used to live here was removed: the
// CLI called /api/v1/org/organisation/* routes the backend only serves to
// browser sessions, so none of it ever worked.)

import "fmt"

// OrganisationUser is one row of the organisation detail's users array
// (id is the platform user UUID).
type OrganisationUser struct {
	ID          *string `json:"id"`
	Email       *string `json:"email"`
	Status      *string `json:"status"`
	Role        *string `json:"role"`
	UserCurrent *bool   `json:"user_current"`
	// ServicePrincipalKind and ServicePrincipalName say a member is a
	// machine identity (kind "service_account", or the board agent) and
	// what it is called; both are nil for a person and from a platform
	// that predates them.
	ServicePrincipalKind *string `json:"service_principal_kind"`
	ServicePrincipalName *string `json:"service_principal_name"`
}

// ListOrganisationUsers returns the organisation's members with their
// platform user ids.
func (c *Client) ListOrganisationUsers(orgID string) ([]OrganisationUser, error) {
	var response struct {
		Users []OrganisationUser `json:"users"`
	}
	url := fmt.Sprintf("%s/api/v1/org/organisation/%s", c.BaseURL, orgID)
	if err := c.getJSON(url, &response); err != nil {
		return nil, err
	}
	return response.Users, nil
}
