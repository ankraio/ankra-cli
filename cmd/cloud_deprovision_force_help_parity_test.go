package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every cloud provider's `deprovision --force` deletes the cluster's storage
// volumes and load balancers. The help text has to say so, because the flag is
// destructive in a direction the operator cannot undo: the volumes hold their
// data.
//
// Hetzner's said the opposite until ankra-eg2v5 ("cloud resources may leak"),
// which was the pre-ankra-phep meaning of the flag — it survived three sibling
// rewrites because nothing pinned the wording. This test is that pin.
func TestDeprovisionForceHelpNamesWhatItDeletes(t *testing.T) {
	tests := []struct {
		name    string
		command *cobra.Command
	}{
		{name: "hetzner", command: hetznerDeprovisionCmd},
		{name: "upcloud", command: upcloudDeprovisionCmd},
		{name: "digitalocean", command: digitaloceanDeprovisionCmd},
		{name: "ovh", command: ovhDeprovisionCmd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := tt.command.Flags().Lookup("force")
			if flag == nil {
				t.Fatal("no --force flag registered")
			}
			usage := strings.ToLower(flag.Usage)

			if !strings.Contains(usage, "volumes") {
				t.Errorf("--force help does not mention volumes: %q", flag.Usage)
			}
			if !strings.Contains(usage, "load balancers") {
				t.Errorf("--force help does not mention load balancers: %q", flag.Usage)
			}
			// The stale wording claimed the opposite of what the flag does:
			// --force is the mode in which resources do NOT leak.
			if strings.Contains(usage, "may leak") {
				t.Errorf("--force help still carries the stale leak wording: %q", flag.Usage)
			}
		})
	}
}
