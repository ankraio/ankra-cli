package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skills are matched against what the user happens to say. A workflow is the
// other direction: a named entry point the user invokes deliberately for a
// job that spans several skills and a fixed order of steps — ship a new
// service, wire an app to a platform service, triage a broken rollout. Each
// client spells them differently (Claude Code and Cursor commands, Codex
// prompts, Copilot prompt files, Gemini TOML, Windsurf workflows), so the
// body is authored once and rendered per format.

// Workflow is one named, multi-step entry point.
type Workflow struct {
	// Name is the file stem and, for most clients, the slash command.
	Name string
	// Description is the one-line summary the client lists.
	Description string
	// Body is the markdown instruction the client feeds the agent.
	Body string
}

// workflowFilePrefix is stripped from the file name when the commands
// directory is already namespaced (Gemini's .gemini/commands/ankra), so the
// invocation stays /ankra:ship-service rather than /ankra:ankra-ship-service.
const workflowFilePrefix = "ankra-"

var workflowRegistry = []Workflow{
	{
		Name:        "ankra-ship-service",
		Description: "Take source code from a repository to a production deployment on one or many Ankra clusters",
		Body: `Take the service in the current repository (or the path the user names) from source
to a running production deployment on Ankra. Read the ` + "`ankra-applications`" + ` skill first,
then ` + "`ankra-cicd`" + `, ` + "`ankra-stacks-addons`" + ` and ` + "`ankra-platform-principles`" + `.

Work in this order and confirm the target with the user before anything mutating:

1. **Orient.** ` + "`ankra org current`" + `, ` + "`ankra cluster list`" + `. Establish which organisation, which
   clusters are the dev/staging/production targets, and whether the repo is already an
   application (` + "`ankra application list`" + `).
2. **Check the source is deployable.** A Dockerfile (or the language default Ankra can
   generate one for), a listening port, health endpoints, and configuration read from the
   environment — not baked-in files. Fix what is missing before registering.
3. **Register the application.** ` + "`ankra application add . --name <name>`" + `. If the organisation
   publishes images to a registry it already operates (Harbor, ECR, GAR, ACR), declare it in
   the same command with ` + "`--registry-url oci://<host>/<project>`" + ` and ` + "`--registry-credential <name>`" + `:
   a registry added afterwards leaves a build workflow that logs in with the wrong one.
4. **Review the generated setup.** ` + "`ankra application get <id>`" + `, ` + "`ankra application branch-files <id>`" + `.
   Ankra opens a setup pull request with the Dockerfile, chart and build workflow. Read the
   diff, adjust, and use ` + "`ankra application files`" + ` to commit changes back to that branch.
5. **Supply configuration and secrets.** ` + "`ankra application env-secrets list <id>`" + ` shows what the
   manifests expect. Set each with ` + "`ankra application env-secrets set`" + ` (pipe the value on stdin —
   never ` + "`--value`" + ` on the command line), then ` + "`ankra application env-secrets apply <id>`" + `.
6. **Deploy to the first cluster.** ` + "`ankra application deploy <id> --cluster <cluster-id> --namespace <ns>`" + `.
   Watch it land with ` + "`ankra cluster operations list`" + ` and ` + "`ankra cluster get pods -n <ns>`" + `.
7. **Verify.** Logs (` + "`ankra cluster logs -l app=<name> -n <ns> --follow=false`" + `), events, and the
   application's own health endpoint. Do not proceed to production on an unverified rollout.
8. **Wire continuous delivery.** Decide with the user between ` + "`ankra application auto-deploy set on`" + `
   (a build on the tracked branch rolls itself out) and an explicit gate. Add scanning with
   ` + "`ankra application upgrade-workflow`" + `, and read the findings with ` + "`ankra application code-security`" + `
   and ` + "`ankra application container-security`" + `.
9. **Reach the rest of the fleet.** For many clusters, publish the manifests once with
   ` + "`ankra application publish-addon <id> --version <semver>`" + ` and install that add-on per cluster, or
   capture the deployed stack as a stack profile (see the ` + "`ankra-stack-profiles`" + ` skill) and apply it
   per cluster with the cluster-specific values bound as parameters. Do not copy-paste YAML per
   cluster.

Report back: the application id, the registry it publishes to, which clusters it is live on,
the URL(s), and what is still manual.`,
	},
	{
		Name:        "ankra-new-cluster",
		Description: "Stand up a new Ankra cluster: provider, region, instance family, GitOps, ingress, DNS and TLS",
		Body: `Stand up a cluster on Ankra for the user, from the provider choice through to a
hostname that resolves with TLS. Read the ` + "`ankra-getting-started`" + ` skill first, then
` + "`ankra-cloud-clusters`" + ` (or ` + "`ankra-managed-kubernetes`" + `) and ` + "`ankra-domains-dns`" + `.

Settle these five before creating anything - creating first and retrofitting is the expensive path:

1. **Import or build?** If Kubernetes already runs, adopt it (` + "`ankra-import-cluster`" + `, or
   ` + "`ankra cluster managed discover`" + ` / ` + "`import`" + ` for a managed cluster) instead of building a second one.
2. **Who runs the control plane?** ` + "`ankra cluster managed create`" + ` when the provider should own its
   uptime and you do not need cluster-admin over it; ` + "`ankra cluster <provider> create`" + ` when you need
   control-plane access, node-level control, or a provider with no managed offering.
3. **Region and instance family.** Never guess these. List them for the actual credential:
   ` + "`ankra cluster hetzner locations|server-types`" + `, ` + "`ankra cluster digitalocean regions|sizes`" + `,
   ` + "`ankra cluster ovh regions --with-zones`" + `, ` + "`ankra cluster proxmox sizes|hosts`" + `,
   ` + "`ankra cluster morpheus plans|layouts`" + `. A region the account cannot deploy in fails late, at
   private-network setup. Size the control plane and workers per the table in
   ` + "`ankra-cloud-clusters/reference.md`" + `, and use ` + "`--control-plane-count 3`" + ` for anything that must
   survive losing a node.
4. **The GitOps repository.** Pass ` + "`--gitops-repository`" + `, ` + "`--gitops-credential-name`" + ` and
   ` + "`--gitops-branch`" + ` on the create command itself, so the generated cloud-provider stack lands as a
   reviewable commit rather than existing only in the platform.
5. **The domain.** The generated ` + "`<cluster>.ankra.cc`" + ` subdomain needs nothing and gives you HTTPS
   today. Your own domain either delegates to Ankra (` + "`ankra org domain set`" + `) or stays in your DNS
   account and is declared with ` + "`ankra org custom-dns-zones add`" + `. Declare org-wide, or clusters
   created later come up with your hostnames silently unserved.

Then:

- **Store the credentials first** (` + "`ankra credentials <provider> create`" + `, plus an SSH key credential);
  ` + "`create`" + ` takes credential **IDs**, which ` + "`ankra credentials list`" + ` gives you.
- **Take the batteries.** ` + "`--external-cloud-provider`" + `, ` + "`--include-networking`" + ` and ` + "`--include-dns`" + `
  default on and give load balancers, volumes, ingress, DNS and TLS from the first minute.
- **Create, then watch.** ` + "`create`" + ` has no ` + "`--wait`" + `: follow with ` + "`ankra cluster operations list`" + ` and
  ` + "`ankra cluster agent status`" + `. Do not re-submit.
- **Verify before handing it over:** the agent is online, nodes are Ready, a test Ingress on the
  cluster domain resolves and serves a valid certificate.

Report the cluster id, its domain, what was installed, what it costs per month at this size, and
the one command to tear it down.`,
	},
	{
		Name:        "ankra-connect-app",
		Description: "Connect an application to an existing platform service and its secrets (LiteLLM, Harbor, a database, an internal API)",
		Body: `Connect the application the user names to a service that already runs in this
organisation — an LLM gateway (LiteLLM), a container registry (Harbor), a database, an
internal API — using the credentials that already exist rather than minting new ones by hand.
Read the ` + "`ankra-app-integrations`" + ` skill first, then ` + "`ankra-sops-secrets`" + ` and ` + "`ankra-security`" + `.

1. **Find the service.** ` + "`ankra cluster stacks list`" + ` and ` + "`ankra cluster addons list`" + ` show what is
   deployed. ` + "`ankra cluster get services -n <namespace>`" + ` gives the in-cluster DNS name and port —
   that, not a public URL, is what a same-cluster consumer should use.
2. **Find the credential that already exists.** ` + "`ankra cluster addons values <addon>`" + ` (and
   ` + "`ankra cluster decrypt addon`" + ` where values are SOPS-encrypted) shows how the service itself is
   configured; ` + "`ankra credentials list`" + ` and ` + "`ankra helm credentials list`" + ` show the stored ones.
   Reuse an existing key or issue a scoped one from the service — never copy an admin token
   into an application.
3. **Decide where the value lives.** Application environment secrets
   (` + "`ankra application env-secrets set`" + `) for values only that application needs; a SOPS-encrypted
   manifest in the GitOps repo with ` + "`encrypted_paths`" + ` declared for anything a stack deploys; a
   cluster or organisation variable (` + "`ankra cluster variables set`" + ` / ` + "`ankra org variables set`" + `) for
   non-secret values such as base URLs and model names.
4. **Wire the consumer.** Point the application at the in-cluster endpoint, inject the secret by
   reference (a Secret ` + "`envFrom`" + `/` + "`valueFrom`" + `, not a literal), and pin any model, chart or image
   version it depends on.
5. **Prove it end to end.** Roll the consumer, then read its logs for a successful call. For an
   LLM gateway, make one real request and confirm the gateway logged it. For a registry, confirm
   a pull with the generated ` + "`dockerconfigjson`" + ` pull secret actually succeeds.
6. **Leave it reproducible.** Every change lands in committed YAML or in Ankra's stored state —
   nothing configured only by hand in a live pod.

Never print a decrypted secret into the terminal transcript, a pull request, or chat.`,
	},
	{
		Name:        "ankra-triage",
		Description: "Investigate a failing workload, deployment, or Ankra operation and propose the fix",
		Body: `Triage what is broken on the Ankra-managed cluster the user names. Read the
` + "`ankra-troubleshooting`" + ` skill first. Work read-only until you have a diagnosis; propose the
change, do not apply it unasked.

1. **Scope it.** ` + "`ankra cluster info`" + ` to confirm the cluster, then
   ` + "`ankra cluster operations list`" + ` — a failed platform execution explains far more failures than
   the pod logs do. ` + "`ankra cluster operations steps <id>`" + ` for the failing step.
2. **Look at the object.** ` + "`ankra cluster get pods -n <ns>`" + `, then
   ` + "`ankra cluster describe pod <name> -n <ns>`" + ` for conditions and its own events, and
   ` + "`ankra cluster events -n <ns>`" + ` for the namespace timeline.
3. **Read the log that has the failure in it.** For CrashLoopBackOff that is the previous
   container: ` + "`ankra cluster logs <pod> -n <ns> --previous`" + `. For a set of replicas use
   ` + "`ankra cluster logs -l <selector> -n <ns> --follow=false --tail 200`" + `, and ` + "`--all-containers`" + ` when an
   init container is the suspect.
4. **Check resources before blaming the code.** ` + "`ankra cluster top pods -n <ns>`" + ` and
   ` + "`ankra cluster top nodes`" + ` read the metrics API directly; ` + "`ankra cluster metrics query '<promql>'`" + `
   for trends where Prometheus is installed.
5. **Classify.** Configuration or secret missing, image or chart version, scheduling and capacity,
   dependency ordering (a stack resource whose ` + "`parents`" + ` are wrong deploys too early), an
   external dependency, or genuine application code.
6. **Propose the smallest correct fix**, at the right layer: stack/addon values and ordering for
   platform problems, the application repository and a pull request for code problems. Say which
   one it is. For a terminal execution that failed on a transient cause, ` + "`ankra cluster operations retry <id>`" + `
   is the right move — say so rather than re-applying everything.

Finish with: what is broken, the evidence, the fix, and the blast radius of applying it.`,
	},
	{
		Name:        "ankra-promote",
		Description: "Promote a verified change from dev or staging to production across one or many clusters",
		Body: `Promote a change that is already verified in a lower environment to the next one,
without rebuilding it. Read ` + "`ankra-platform-principles`" + `, ` + "`ankra-stack-profiles`" + ` and ` + "`ankra-gitops`" + `.

1. **Establish what is verified.** The exact image tag or digest, the chart version, and the
   cluster it is verified on. If nothing is pinned, stop: promote a pinned artefact or nothing.
2. **Diff the environments.** ` + "`ankra cluster stacks list <stack> -o json`" + ` on both clusters, or
   ` + "`ankra stack-profiles diff <profile> --from <v> --to <v>`" + ` for a profile. Name every difference that
   is deliberate (sizes, replicas, domains) and every one that is drift.
3. **Promote the same artefact.** Commit the verified tag to the production path in the GitOps
   repository, or ` + "`ankra stack-profiles apply <profile> --cluster <prod> --version <v>`" + ` binding the
   production values with ` + "`--set`" + ` / ` + "`--set-file`" + ` / ` + "`--set-env`" + `. Never rebuild for production.
4. **Land it as a reviewable draft first.** ` + "`ankra stack-profiles apply`" + ` without ` + "`--deploy`" + `, or
   ` + "`ankra cluster draft -f cluster.yaml`" + `, stages the change for review instead of deploying it.
   Use ` + "`--dry-run`" + ` to show exactly which value every input resolves to.
5. **Roll out, then verify** with operations, pods, and logs before touching the next cluster.
   For a fleet, do one cluster, verify, then the rest — never all at once.
6. **Know the rollback.** ` + "`git revert`" + ` for a GitOps change, ` + "`ankra cluster roll-to`" + ` for a resource
   version, ` + "`ankra stack-profiles set-current-version`" + ` for a profile. State it before you deploy.`,
	},
	{
		Name:        "ankra-harden",
		Description: "Run a security pass over an Ankra organisation: tokens, access grants, secrets, and image findings",
		Body: `Review the security posture of the Ankra organisation the user names and report
findings ranked by exposure. Read the ` + "`ankra-security`" + ` and ` + "`ankra-sops-secrets`" + ` skills first.
This is a read-and-report pass: change nothing without asking.

1. **Identity and tokens.** ` + "`ankra tokens list`" + ` — flag tokens with no expiry, tokens holding
   ` + "`mcp:write`" + ` that only need ` + "`mcp:read`" + `, and anything unused. ` + "`ankra org members`" + ` and
   ` + "`ankra org roles`" + ` for who holds what.
2. **Cluster access.** ` + "`ankra cluster access list`" + ` per cluster — flag every ` + "`cluster-admin`" + ` and every
   cluster-wide grant that could be namespace-scoped.
3. **Secrets.** Confirm nothing sensitive is committed in plaintext: every SOPS-encrypted value has
   its ` + "`encrypted_paths`" + ` declared (` + "`ankra cluster stacks list <stack> -o json`" + `), and
   ` + "`ankra cluster sops-config`" + ` shows the key in use. Check application environment secrets are set
   rather than defaulted (` + "`ankra application env-secrets list`" + `).
4. **Supply chain.** ` + "`ankra application container-security <id>`" + ` and ` + "`ankra application code-security <id>`" + `
   per application; flag any application whose build workflow has no scanning step
   (` + "`ankra application upgrade-workflow`" + ` adds it). Flag floating image tags and unpinned chart
   versions — a mutable tag defeats every other control here.
5. **Agent autonomy.** ` + "`ankra org mcp-servers list`" + ` and ` + "`ankra org mcp-servers grants <server>`" + ` — flag
   read_write tiers and tool grants wider than the role needs. Check which integrations run in
   Agent rather than Ask mode.
6. **Registry and credential scope.** ` + "`ankra credentials list`" + `, ` + "`ankra helm credentials list`" + ` — flag
   any credential that is broader than the repositories it is used for
   (` + "`ankra credentials repositories <name>`" + ` shows what it can actually reach).

Report a ranked list: what is exposed, how it could be used, and the one command or change that
closes it. Do not print secret values.`,
	},
	{
		Name:        "ankra-profile",
		Description: "Capture a working cluster stack as a reusable, parameterised stack profile for the fleet",
		Body: `Turn a stack that works on one cluster into a reusable stack profile other clusters
and organisations can launch. Read the ` + "`ankra-stack-profiles`" + ` skill first, then ` + "`ankra-stacks-addons`" + `.

1. **Pick the source.** ` + "`ankra cluster stacks list`" + ` on the cluster where it works. The source stack
   should be one concern, pinned, and already verified.
2. **Open a builder draft from it.**
   ` + "`ankra stack-profiles drafts create --name <profile> --source-cluster <cluster> --source-stack <stack>`" + `.
3. **Turn the cluster-specific values into parameters.** ` + "`ankra stack-profiles drafts get <draft>`" + ` lists
   what was detected. Every domain, size, replica count, storage class and credential must be a
   parameter, not a literal carried over from the source cluster. Annotate each one with
   ` + "`ankra stack-profiles drafts annotate`" + ` so the launch form explains itself.
4. **Group the choices that move together.** Where one decision drives several inputs (a "model
   size" that sets the model id, the context length and the volume), declare it with
   ` + "`ankra stack-profiles drafts options set`" + ` so whoever launches it picks one thing instead of keeping
   four consistent.
5. **Mark the secrets.** Secret parameters must be declared as such, and any encrypted value needs
   its ` + "`encrypted_paths`" + `. Opening a draft on a published profile drops encrypted paths — re-declare
   them before publishing.
6. **Validate, then publish.** ` + "`ankra stack-profiles drafts validate <draft>`" + `, then
   ` + "`ankra stack-profiles drafts publish <draft> --changelog \"...\"`" + `.
7. **Prove it launches clean.** ` + "`ankra stack-profiles apply <profile> --cluster <other> --dry-run`" + ` shows
   the value every input resolves to; then apply for real as a draft and review before deploying.
8. **Distribute.** ` + "`ankra stack-profiles share add`" + ` for named organisations,
   ` + "`ankra stack-profiles export-iac`" + ` to keep the definition in Git.`,
	},
}

// Workflows returns the workflow entry points, in registry order.
func Workflows() []Workflow {
	out := make([]Workflow, len(workflowRegistry))
	copy(out, workflowRegistry)
	return out
}

// WriteWorkflows renders every workflow into the target's commands directory
// in that client's format, returning the paths written. It is a no-op for
// clients with no command mechanism.
func WriteWorkflows(target Target) ([]string, error) {
	if !target.SupportsCommands() {
		return nil, nil
	}
	if err := os.MkdirAll(target.CommandsDirectory, 0o755); err != nil {
		return nil, err
	}
	written := make([]string, 0, len(workflowRegistry))
	for _, workflow := range workflowRegistry {
		path := workflowPath(target, workflow)
		content, err := renderWorkflow(target.Client.CommandFormat, workflow)
		if err != nil {
			return written, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

// RemoveWorkflows deletes the workflow files install wrote for a target,
// returning how many existed. Files the CLI did not write are left alone.
func RemoveWorkflows(target Target) (int, error) {
	if !target.SupportsCommands() {
		return 0, nil
	}
	removed := 0
	for _, workflow := range workflowRegistry {
		path := workflowPath(target, workflow)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// workflowPath resolves one workflow's file name for a target, dropping the
// "ankra-" stem when the commands directory is already the ankra namespace.
func workflowPath(target Target, workflow Workflow) string {
	name := workflow.Name
	if filepath.Base(target.CommandsDirectory) == "ankra" {
		name = strings.TrimPrefix(name, workflowFilePrefix)
	}
	return filepath.Join(target.CommandsDirectory, name+workflowExtension(target.Client.CommandFormat))
}

func workflowExtension(format string) string {
	switch format {
	case "copilot":
		return ".prompt.md"
	case "toml":
		return ".toml"
	default:
		return ".md"
	}
}

// renderWorkflow renders one workflow in a client's command file format.
func renderWorkflow(format string, workflow Workflow) (string, error) {
	switch format {
	case "markdown":
		return fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", workflow.Description, workflow.Body), nil
	case "copilot":
		return fmt.Sprintf("---\nmode: agent\ndescription: %s\n---\n\n%s\n", workflow.Description, workflow.Body), nil
	case "toml":
		return fmt.Sprintf("description = %s\nprompt = \"\"\"\n%s\n\"\"\"\n",
			tomlString(workflow.Description), escapeTOMLBlock(workflow.Body)), nil
	default:
		return "", fmt.Errorf("unsupported command format %q", format)
	}
}

func tomlString(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// escapeTOMLBlock protects a multi-line basic string: backslashes must not
// start an escape and a literal triple quote would close the block early.
func escapeTOMLBlock(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"""`, `\"\"\"`).Replace(value)
}
