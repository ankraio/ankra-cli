package cmd

// The organisation cluster access policy, over GET
// /api/v1/org/cluster-access-policy (PLA-878): the limits every kubectl access
// grant is held to. Reading is all the CLI does: changing the policy needs the
// kube_access.policy permission, which the platform refuses to an API token
// unless the token was made for exactly that, so it is changed from a
// signed-in session (Organisation settings, Roles, Kubernetes access policy).

import (
	"context"
	"fmt"
	"io"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var orgAccessPolicyCmd = &cobra.Command{
	Use:   "access-policy",
	Short: "Show the organisation's Kubernetes access policy",
	Long: `Show the limits every kubectl access grant in this organisation is held
to, an admin's included:

  creator role       what whoever creates or imports a cluster gets on it
  ceiling            the most access anyone can be given
  elevated lifetime  how long a grant above view may last (also the window
                     'ankra cluster access elevate' may ask for; 4 hours
                     when the policy sets none)
  reason from        the lowest role whose grants need a reason

Reading requires managing kubectl access, the policy, or holding
break-glass (kube_access.elevate). Changing the policy needs an owner or
admin signed in: Organisation settings, Roles, Kubernetes access policy.`,
}

var orgAccessPolicyGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the organisation's Kubernetes access policy",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		policy, err := apiClient.GetClusterAccessPolicy(ctx)
		if err != nil {
			return fmt.Errorf("get the cluster access policy: %w", err)
		}
		if rendered, renderError := renderStructured(cmd, policy); rendered || renderError != nil {
			return renderError
		}
		renderClusterAccessPolicy(cmd.OutOrStdout(), policy)
		return nil
	},
}

// renderClusterAccessPolicy prints the policy in plain words.
func renderClusterAccessPolicy(out io.Writer, policy *client.ClusterAccessPolicy) {
	suffix := ""
	if !policy.IsConfigured {
		_, _ = fmt.Fprintln(out, "No policy is set: whoever creates or imports a cluster gets cluster-admin on it, and anyone can be given any access.")
		suffix = " (default)"
	}
	_, _ = fmt.Fprintf(out, "Creator role:       %s%s\n", policy.CreatorGrantRole, suffix)
	_, _ = fmt.Fprintf(out, "Ceiling:            %s%s\n", policy.MaxGrantRole, suffix)
	elevated := "no limit (break-glass: 4h)"
	if policy.ElevatedMaxTTLSeconds != nil {
		elevated = (time.Duration(*policy.ElevatedMaxTTLSeconds) * time.Second).String()
	}
	_, _ = fmt.Fprintf(out, "Elevated lifetime:  %s\n", elevated)
	reason := "only for break-glass"
	if policy.RequireReasonFromRole != nil {
		reason = *policy.RequireReasonFromRole + " and above"
	}
	_, _ = fmt.Fprintf(out, "Reason required:    %s\n", reason)
}

func init() {
	registerStructuredOutputFlags(orgAccessPolicyGetCmd)
	orgAccessPolicyCmd.AddCommand(orgAccessPolicyGetCmd)
	orgCmd.AddCommand(orgAccessPolicyCmd)
}
