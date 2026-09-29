// Package systemtest holds the tests for lifecycle_systemtest.sh's lane plan:
// which selected provider lanes run, and which are skipped because their
// configuration is missing. They run the script with
// ANKRA_SYSTEMTEST_PLAN_ONLY=1, which stops after the plan, so no ankra
// binary, platform or cloud account is involved.
package systemtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type planResult struct {
	output   string
	exitCode int
	summary  string
	outputs  string
}

// runPlan runs the lane plan with only the given environment (plus PATH and
// an empty HOME), so nothing from the developer's shell - a saved login, AWS
// keys, a ~/.aws directory - leaks into what the plan sees.
func runPlan(t *testing.T, env map[string]string) planResult {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	summaryFile := filepath.Join(dir, "step_summary")
	outputFile := filepath.Join(dir, "step_output")
	for _, file := range []string{summaryFile, outputFile} {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := map[string]string{
		"PATH":                       os.Getenv("PATH"),
		"HOME":                       dir,
		"TMPDIR":                     dir,
		"ANKRA_CONFIG_FILE":          filepath.Join(dir, "no-saved-login.yaml"),
		"ANKRA_SYSTEMTEST_PLAN_ONLY": "1",
		"GITHUB_STEP_SUMMARY":        summaryFile,
		"GITHUB_OUTPUT":              outputFile,
	}
	for key, value := range env {
		base[key] = value
	}
	cmd := exec.Command(bash, "lifecycle_systemtest.sh")
	for key, value := range base {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	result := planResult{output: string(out)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		result.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("running the lane plan: %v", err)
	}
	summary, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	result.summary, result.outputs = string(summary), string(outputs)
	return result
}

func TestLanePlan(t *testing.T) {
	cases := []struct {
		name         string
		env          map[string]string
		wantExit     int
		wantOutput   []string
		rejectOutput []string
		wantSummary  []string
		wantOutputs  []string
	}{
		{
			// The failure every scheduled run hit from 2026-09-14: the token
			// was there, the Hetzner credential id was not, and the preflight
			// died instead of naming the lane it could not run.
			name: "a lane missing its credential is skipped by name, not fatal",
			env: map[string]string{
				"ANKRA_API_TOKEN":                      "token",
				"SSH_KEY_CREDENTIAL_ID":                "ssh",
				"ANKRA_SYSTEMTEST_PROVIDERS":           "hetzner",
				"ANKRA_SYSTEMTEST_MANAGED_PROVIDERS":   "none",
				"ANKRA_SYSTEMTEST_FAIL_IF_ALL_SKIPPED": "0",
			},
			wantExit:     0,
			wantOutput:   []string{"SKIP: hetzner lane: missing HETZNER_CREDENTIAL_ID", "1 of 1 provider lanes skipped: missing HETZNER_CREDENTIAL_ID"},
			rejectOutput: []string{"FATAL"},
			wantOutputs:  []string{"requested_lanes=1", "skipped_lanes=1", "runnable_lanes=0"},
		},
		{
			name: "a run in which every lane is skipped fails by default",
			env: map[string]string{
				"ANKRA_API_TOKEN":                    "token",
				"SSH_KEY_CREDENTIAL_ID":              "ssh",
				"ANKRA_SYSTEMTEST_PROVIDERS":         "hetzner",
				"ANKRA_SYSTEMTEST_MANAGED_PROVIDERS": "",
			},
			wantExit:   2,
			wantOutput: []string{"SKIP: hetzner lane: missing HETZNER_CREDENTIAL_ID", "no provider lane can run"},
		},
		{
			// The job summary CI shows: configured lanes run, the rest are
			// counted and named by the repository secret they need.
			name: "configured lanes run and the summary counts the skipped ones",
			env: map[string]string{
				"ANKRA_API_TOKEN":                "token",
				"SSH_KEY_CREDENTIAL_ID":          "ssh",
				"HETZNER_CREDENTIAL_ID":          "hetzner-id",
				"UPCLOUD_CREDENTIAL_ID":          "upcloud-id",
				"ANKRA_SYSTEMTEST_CONFIG_PREFIX": "SYSTEMTEST_",
				"GITHUB_ACTIONS":                 "true",
			},
			wantExit: 0,
			wantOutput: []string{
				"SKIP: ovh lane: missing SYSTEMTEST_OVH_CREDENTIAL_ID",
				"SKIP: managed/doks lane: missing SYSTEMTEST_DIGITALOCEAN_CREDENTIAL_ID",
				"::warning title=Lifecycle lanes skipped::7 of 10 provider lanes skipped",
				"runnable: hetzner upcloud / managed: uks",
			},
			wantSummary: []string{
				"**7 of 10 provider lanes skipped: missing SYSTEMTEST_OVH_CREDENTIAL_ID, SYSTEMTEST_DIGITALOCEAN_CREDENTIAL_ID, SYSTEMTEST_GKE_CREDENTIAL_ID, SYSTEMTEST_AKS_CREDENTIAL_ID, SYSTEMTEST_EKS_CREDENTIAL_ID**",
				"| `hetzner` | Ankra-managed | runs | |",
				"| `managed/gke` | cloud-managed | **skipped** | SYSTEMTEST_GKE_CREDENTIAL_ID |",
			},
			wantOutputs: []string{"requested_lanes=10", "skipped_lanes=7", "runnable_lanes=3"},
		},
		{
			name: "without an API token or a saved login every lane is skipped",
			env: map[string]string{
				"SSH_KEY_CREDENTIAL_ID":              "ssh",
				"HETZNER_CREDENTIAL_ID":              "hetzner-id",
				"ANKRA_SYSTEMTEST_PROVIDERS":         "hetzner",
				"ANKRA_SYSTEMTEST_MANAGED_PROVIDERS": "",
				"GITHUB_ACTIONS":                     "true",
			},
			wantExit:   2,
			wantOutput: []string{"SKIP: hetzner lane: missing ANKRA_API_TOKEN", "::error title=No lifecycle lane can run::"},
			wantSummary: []string{
				"**1 of 1 provider lanes skipped: missing ANKRA_API_TOKEN**",
				"No lane can run, so nothing is provisioned or tested.",
			},
		},
		{
			name: "the aws lane needs the leak-check keys as well as its credential",
			env: map[string]string{
				"ANKRA_API_TOKEN":                      "token",
				"SSH_KEY_CREDENTIAL_ID":                "ssh",
				"AWS_CREDENTIAL_ID":                    "aws-id",
				"ANKRA_SYSTEMTEST_PROVIDERS":           "aws",
				"ANKRA_SYSTEMTEST_MANAGED_PROVIDERS":   "none",
				"ANKRA_SYSTEMTEST_FAIL_IF_ALL_SKIPPED": "0",
			},
			wantExit:   0,
			wantOutput: []string{"SKIP: aws lane: missing AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY"},
		},
		{
			name: "the aws lane runs once its credential and keys exist",
			env: map[string]string{
				"ANKRA_API_TOKEN":                    "token",
				"SSH_KEY_CREDENTIAL_ID":              "ssh",
				"AWS_CREDENTIAL_ID":                  "aws-id",
				"AWS_ACCESS_KEY_ID":                  "key",
				"AWS_SECRET_ACCESS_KEY":              "secret",
				"ANKRA_SYSTEMTEST_PROVIDERS":         "aws",
				"ANKRA_SYSTEMTEST_MANAGED_PROVIDERS": "none",
			},
			wantExit:     0,
			wantOutput:   []string{"All 1 provider lanes configured", "runnable: aws / managed: none"},
			rejectOutput: []string{"SKIP"},
		},
		{
			// A present-but-contradictory setting is a mistake, not a
			// missing one: it must stay loud.
			name: "a partial aws VPC set stays fatal",
			env: map[string]string{
				"ANKRA_API_TOKEN":                    "token",
				"SSH_KEY_CREDENTIAL_ID":              "ssh",
				"AWS_CREDENTIAL_ID":                  "aws-id",
				"AWS_ACCESS_KEY_ID":                  "key",
				"AWS_SECRET_ACCESS_KEY":              "secret",
				"AWS_NODE_SUBNET_IDS":                "subnet-a",
				"ANKRA_SYSTEMTEST_PROVIDERS":         "aws",
				"ANKRA_SYSTEMTEST_MANAGED_PROVIDERS": "",
			},
			wantExit:   2,
			wantOutput: []string{"FATAL: AWS_NODE_SUBNET_IDS/AWS_BASTION_SUBNET_ID only apply with AWS_VPC_ID"},
		},
		{
			name: "an unknown provider is a usage error, not a skipped lane",
			env: map[string]string{
				"ANKRA_API_TOKEN":            "token",
				"ANKRA_SYSTEMTEST_PROVIDERS": "hetznr",
			},
			wantExit:     2,
			wantOutput:   []string{"FATAL: unknown provider in ANKRA_SYSTEMTEST_PROVIDERS: hetznr"},
			rejectOutput: []string{"SKIP"},
		},
		{
			name: "none empties a family, and doks falls back to the DigitalOcean credential",
			env: map[string]string{
				"ANKRA_API_TOKEN":                    "token",
				"DIGITALOCEAN_CREDENTIAL_ID":         "do-id",
				"ANKRA_SYSTEMTEST_PROVIDERS":         "none",
				"ANKRA_SYSTEMTEST_MANAGED_PROVIDERS": "doks",
			},
			wantExit:    0,
			wantOutput:  []string{"runnable: none / managed: doks"},
			wantOutputs: []string{"requested_lanes=1", "runnable_lanes=1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runPlan(t, tc.env)
			if got.exitCode != tc.wantExit {
				t.Errorf("exit code = %d, want %d\n%s", got.exitCode, tc.wantExit, got.output)
			}
			for _, want := range tc.wantOutput {
				if !strings.Contains(got.output, want) {
					t.Errorf("output lacks %q\n%s", want, got.output)
				}
			}
			for _, reject := range tc.rejectOutput {
				if strings.Contains(got.output, reject) {
					t.Errorf("output unexpectedly contains %q\n%s", reject, got.output)
				}
			}
			for _, want := range tc.wantSummary {
				if !strings.Contains(got.summary, want) {
					t.Errorf("job summary lacks %q\n%s", want, got.summary)
				}
			}
			for _, want := range tc.wantOutputs {
				if !strings.Contains(got.outputs, want+"\n") {
					t.Errorf("step outputs lack %q\n%s", want, got.outputs)
				}
			}
		})
	}
}

// Since ankra-cli#383 a deprovision off a terminal refuses to delete a
// cluster's persistent volumes, or volumes Ankra cannot list, without
// --accept-volume-data-loss. A lane deprovision or abort cleanup without it
// leaves the cluster running and billing (run 36561535646 left an UpCloud
// cluster behind), so every self-managed deprovision the script issues must
// carry the consent.
func TestEveryDeprovisionAcceptsVolumeDataLoss(t *testing.T) {
	script, err := os.ReadFile("lifecycle_systemtest.sh")
	if err != nil {
		t.Fatal(err)
	}
	consentDefined := false
	deprovisions := 0
	for number, line := range strings.Split(string(script), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		if strings.HasPrefix(code, "DEPROVISION_CONSENT=(") {
			consentDefined = strings.Contains(code, "--accept-volume-data-loss")
		}
		if strings.Contains(code, "cluster deprovision ") {
			deprovisions++
			if !strings.Contains(code, `"${DEPROVISION_CONSENT[@]}"`) {
				t.Errorf("line %d deprovisions without the volume-data-loss consent: %s", number+1, code)
			}
		}
	}
	if !consentDefined {
		t.Error("DEPROVISION_CONSENT is not defined with --accept-volume-data-loss")
	}
	if deprovisions == 0 {
		t.Error("found no cluster deprovision calls; the check no longer matches the script")
	}
}
