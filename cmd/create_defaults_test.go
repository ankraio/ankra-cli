package cmd

// The self-managed create commands leave instance types (and DigitalOcean's
// network range) to the platform unless a flag is given (ankra-u70wp). The
// CLI used to carry its own defaults, which drifted from the API's: an OVH
// cluster created from the CLI got b2-7/b2-15 where the portal got c3-4/b3-16,
// an UpCloud one 1xCPU-2GB/2xCPU-4GB where the portal got
// STARTER-1xCPU-1GB/PREMIUM-2xCPU-4GB, a Hetzner one the retired cx23/cx33
// that the platform now refuses, and every DigitalOcean one the same
// 10.0.0.0/16. Omitting the field is what keeps the two from drifting again;
// the help text names the default, so these tests pin that text to the API.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// apiCreateDefaults are the create defaults the platform applies when a field
// is absent, read from cluster go/internal/providerapi/lifecycle.go on
// origin/main (decodeCreate<Provider>ClusterRequest). Hetzner has no fixed
// value: go/internal/usecase/providers/createcluster.go
// applyHetznerServerTypeDefaults resolves one per role from the location's
// live availability. If the API changes a default, change it here and in the
// flag's help text together.
var apiCreateDefaults = []struct {
	command    *cobra.Command
	flag       string
	helpNaming string
}{
	{hetznerCreateCmd, "bastion-server-type", "server default: the cheapest x86 type"},
	{hetznerCreateCmd, "control-plane-server-type", "server default: the cheapest x86 type"},
	{hetznerCreateCmd, "worker-server-type", "server default: the cheapest x86 type"},
	{hetznerCreateCmd, "etcd-server-type", "server default: the cheapest x86 type"},
	{ovhCreateCmd, "gateway-flavor-id", "server default: c3-4"},
	{ovhCreateCmd, "control-plane-flavor-id", "server default: b3-16"},
	{ovhCreateCmd, "worker-flavor-id", "server default: b3-16"},
	{ovhCreateCmd, "etcd-flavor-id", "server default: b3-16"},
	{upcloudCreateCmd, "bastion-plan", "server default: STARTER-1xCPU-1GB"},
	{upcloudCreateCmd, "control-plane-plan", "server default: PREMIUM-2xCPU-4GB"},
	{upcloudCreateCmd, "worker-plan", "server default: PREMIUM-2xCPU-4GB"},
	{upcloudCreateCmd, "etcd-plan", "server default: PREMIUM-2xCPU-4GB"},
	{digitaloceanCreateCmd, "bastion-size", "server default: s-1vcpu-1gb"},
	{digitaloceanCreateCmd, "control-plane-size", "server default: s-2vcpu-4gb"},
	{digitaloceanCreateCmd, "worker-size", "server default: s-2vcpu-4gb"},
	{digitaloceanCreateCmd, "etcd-size", "server default: s-2vcpu-4gb"},
	{digitaloceanCreateCmd, "network-ip-range", "derives a /24 from the cluster id"},
	{proxmoxCreateCmd, "bastion-instance-type", "server default: px-small"},
	{proxmoxCreateCmd, "control-plane-instance-type", "server default: px-medium"},
	{proxmoxCreateCmd, "worker-instance-type", "server default: px-medium"},
	{proxmoxCreateCmd, "etcd-instance-type", "server default: px-medium"},
}

func TestCreateDefaultsFollowTheAPI(t *testing.T) {
	for _, entry := range apiCreateDefaults {
		t.Run(entry.command.Parent().Name()+"/"+entry.flag, func(t *testing.T) {
			flag := entry.command.Flags().Lookup(entry.flag)
			if flag == nil {
				t.Fatalf("flag --%s is not registered", entry.flag)
			}
			if flag.DefValue != "" {
				t.Errorf("--%s defaults to %q; leave it empty so the API default applies", entry.flag, flag.DefValue)
			}
			if !strings.Contains(flag.Usage, entry.helpNaming) {
				t.Errorf("--%s help %q does not name the API default (%q)", entry.flag, flag.Usage, entry.helpNaming)
			}
		})
	}

	// The node-group API requires instance_type, so the deprecated
	// per-provider `node-group add` commands still need a value of their own:
	// the worker default of the create API, so an added group matches the
	// workers a default create gets.
	nodeGroupDefaults := map[*cobra.Command]string{
		ovhNodeGroupAddCmd:          "b3-16",
		upcloudNodeGroupAddCmd:      "PREMIUM-2xCPU-4GB",
		digitaloceanNodeGroupAddCmd: "s-2vcpu-4gb",
	}
	for command, want := range nodeGroupDefaults {
		if got := command.Flags().Lookup("instance-type").DefValue; got != want {
			t.Errorf("%s --instance-type defaults to %q, want the API's worker default %q",
				command.CommandPath(), got, want)
		}
	}
}

// wireKeys marshals a captured request the way the client sends it and
// returns its top-level members.
func wireKeys(t *testing.T, request any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var members map[string]any
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return members
}

func assertOmitted(t *testing.T, members map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if value, present := members[key]; present {
			t.Errorf("%s = %v is on the wire; an unset flag must leave it to the API default", key, value)
		}
	}
}

func assertSent(t *testing.T, members map[string]any, key string, want string) {
	t.Helper()
	if members[key] != want {
		t.Errorf("%s = %v, want the flag's value %q", key, members[key], want)
	}
}

func TestCreateOmitsUnsetInstanceTypes(t *testing.T) {
	t.Run("hetzner", func(t *testing.T) {
		resetOvhCommandFlags(t, hetznerCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, hetznerCreateCmd) })
		mock := &hetznerCreateMock{}
		captureStdout(t, func() {
			if _, err := runWithInput(t, mock, "", "cluster", "hetzner", "create", "--name", "defaults",
				"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--location", "hel1"); err != nil {
				t.Fatalf("create failed: %v", err)
			}
		})
		assertOmitted(t, wireKeys(t, mock.gotRequest),
			"bastion_server_type", "control_plane_server_type", "worker_server_type", "etcd_server_type")
	})
	t.Run("hetzner explicit", func(t *testing.T) {
		resetOvhCommandFlags(t, hetznerCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, hetznerCreateCmd) })
		mock := &hetznerCreateMock{}
		captureStdout(t, func() {
			if _, err := runWithInput(t, mock, "", "cluster", "hetzner", "create", "--name", "explicit",
				"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--location", "hel1",
				"--worker-server-type", "cpx32"); err != nil {
				t.Fatalf("create failed: %v", err)
			}
		})
		members := wireKeys(t, mock.gotRequest)
		assertSent(t, members, "worker_server_type", "cpx32")
		assertOmitted(t, members, "bastion_server_type", "control_plane_server_type")
	})
	t.Run("ovh", func(t *testing.T) {
		resetOvhCommandFlags(t, ovhCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, ovhCreateCmd) })
		mock := &ovhCreateZonesMock{}
		setMockClient(t, mock)
		if _, err := executeCommand("cluster", "ovh", "create", "--name", "defaults",
			"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--region", "GRA11"); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		assertOmitted(t, wireKeys(t, mock.gotRequest),
			"gateway_flavor_id", "control_plane_flavor_id", "worker_flavor_id", "etcd_flavor_id")
	})
	t.Run("ovh explicit", func(t *testing.T) {
		resetOvhCommandFlags(t, ovhCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, ovhCreateCmd) })
		mock := &ovhCreateZonesMock{}
		setMockClient(t, mock)
		if _, err := executeCommand("cluster", "ovh", "create", "--name", "explicit",
			"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--region", "GRA11",
			"--gateway-flavor-id", "b3-8"); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		members := wireKeys(t, mock.gotRequest)
		assertSent(t, members, "gateway_flavor_id", "b3-8")
		assertOmitted(t, members, "control_plane_flavor_id", "worker_flavor_id")
	})
	t.Run("upcloud", func(t *testing.T) {
		resetOvhCommandFlags(t, upcloudCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, upcloudCreateCmd) })
		mock := &upcloudCreateMock{}
		captureStdout(t, func() {
			if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "create", "--name", "defaults",
				"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--zone", "fi-hel2"); err != nil {
				t.Fatalf("create failed: %v", err)
			}
		})
		assertOmitted(t, wireKeys(t, mock.gotRequest), "bastion_plan", "control_plane_plan", "worker_plan", "etcd_plan")
	})
	t.Run("digitalocean", func(t *testing.T) {
		resetOvhCommandFlags(t, digitaloceanCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, digitaloceanCreateCmd) })
		mock := &digitaloceanCreateMock{}
		captureStdout(t, func() {
			if _, err := runWithInput(t, mock, "", "cluster", "digitalocean", "create", "--name", "defaults",
				"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--region", "fra1"); err != nil {
				t.Fatalf("create failed: %v", err)
			}
		})
		assertOmitted(t, wireKeys(t, mock.gotRequest),
			"network_ip_range", "bastion_size", "control_plane_size", "worker_size", "etcd_size")
	})
	t.Run("digitalocean explicit range", func(t *testing.T) {
		resetOvhCommandFlags(t, digitaloceanCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, digitaloceanCreateCmd) })
		mock := &digitaloceanCreateMock{}
		captureStdout(t, func() {
			if _, err := runWithInput(t, mock, "", "cluster", "digitalocean", "create", "--name", "explicit",
				"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--region", "fra1",
				"--network-ip-range", "10.60.0.0/20"); err != nil {
				t.Fatalf("create failed: %v", err)
			}
		})
		assertSent(t, wireKeys(t, mock.gotRequest), "network_ip_range", "10.60.0.0/20")
	})
	t.Run("proxmox", func(t *testing.T) {
		resetOvhCommandFlags(t, proxmoxCreateCmd)
		t.Cleanup(func() { resetOvhCommandFlags(t, proxmoxCreateCmd) })
		mock := &proxmoxCreateMock{}
		captureStdout(t, func() {
			if _, err := runWithInput(t, mock, "", "cluster", "proxmox", "create", "--name", "defaults",
				"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--node", "pve1",
				"--bridge", "vmbr0"); err != nil {
				t.Fatalf("create failed: %v", err)
			}
		})
		assertOmitted(t, wireKeys(t, mock.gotRequest),
			"bastion_instance_type", "control_plane_instance_type", "worker_instance_type", "etcd_instance_type")
	})
}
