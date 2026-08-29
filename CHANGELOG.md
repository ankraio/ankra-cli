# Ankra CLI Changelog

## v0.14.0-rc5 — 2026-08-29

### Added

- **Scaleway clusters get the full command set.** `ankra cluster scaleway`
  now covers create (with `preflight` to prove capacity before paying for
  it), deprovision, worker counts, Kubernetes version and upgrade, the whole
  node-group surface (list, add, scale, instance type, labels, taints,
  delete, autoscaling), control plane, SSH keys and the catalogue reads
  (locations, instance types, gateway types, networks), and `ankra scaleway
  credentials` stores API and SSH-key credentials with the secret key taken
  from a masked prompt, never the command line. The shared `node-group`,
  `ssh-keys`, `scale`, `upgrade` and `delete` commands accept Scaleway
  clusters instead of refusing them.
- **UpCloud clusters can span zones.** `ankra cluster upcloud create --zones
  fi-hel1,fi-hel2,se-sto1` places a kubeadm cluster across three or more
  zones (`--zone` stays the primary), `--network-mode
  private_network|wireguard_mesh` picks the fabric (derived when omitted),
  `ankra cluster upcloud zones <cluster_id> --zones ...` grows the pool, and
  `--zone` on `node-group add` pins a group; `node-group list` shows each
  group's zones. The platform refuses these inputs on organisations without
  the network overlay enabled, and the CLI repeats that refusal verbatim.

- **Debug pods that impersonate a workload.** `ankra cluster debug create
  --namespace <ns> --from-pod <pod>` spins up a pod that mirrors the named
  pod - its service account, node, volumes and volume mounts, environment
  variables and `envFrom` sources, tolerations and security context - under
  an image chosen for its tools (netshoot by default; `--image` takes any
  reference, `debug images` lists the tag-pinned catalogue). Without
  `--from-pod` it is a plain shell in a namespace. `--container` picks which
  container to mirror, `--no-mounts` / `--no-env` leave those out, `--ttl`
  sets the lifetime (1m-8h, default 1h) the kubelet enforces on its own. The
  command prints the portal link to the new pod's terminal; `debug list` and
  `debug delete` manage what is running. Needs `kubernetes.write` and
  `kubernetes.exec`, and cluster agent 2.1.1074 or newer.
- **Recorded terminal sessions are readable.** Every pod terminal session is
  now recorded by the platform; `ankra org terminal-session <session-id>`
  prints a session's facts and, with `--transcript`, replays the recorded
  output as text (`--show-input` lists what was typed). The session id is
  the `terminal_session_id` on an `open_pod_terminal` audit row. Needs
  `audit.read`.
- **`ankra migrate export` dumps the databases a Docker deployment runs, ready
  to be restored into the cluster.** `ankra migrate convert` moved the
  workloads but left every database volume empty; the data had to be copied
  by hand. `ankra migrate export` now finds every PostgreSQL and MySQL/MariaDB
  service in a compose project (or the running daemon), dumps each database
  through the docker CLI - `pg_dump -Fc` plus roles and globals, or
  `mysqldump` - and writes a self-describing directory: the dumps, a
  `manifest.json` that names the Service and Secret `convert` generated for
  each database so a restore knows exactly where to load it, and a
  `SHA256SUMS` file. A deployment on another host is dumped the way docker
  reaches it (`--option docker-host=ssh://root@host`), with
  `--option project=<name>` for a compose project that runs under a
  different name, and `--option databases.<workload>=a,b` to pick databases.
  Passwords never cross the host's command line: every dump runs inside the
  container through its own environment, and the dumps are written readable
  by you alone. Roles come across without their passwords - the cluster's
  own user keeps the password from its Secret, and the export says which
  other roles need one set afterwards - and PostgreSQL's maintenance
  database `postgres` is left out unless the image keeps the application's
  data there (`POSTGRES_DB`, or its default), with a hint on how to include
  it otherwise. Module authors get the same verb:
  a module that lists `export` under its capabilities is asked to dump, with
  its stderr relayed live as progress (`examples/modules/README.md`).
- **`ankra migrate restore` loads an export into the cluster, and `ankra
  migrate data` does export and restore in one go.** The dumps go straight
  from this machine to the organisation's backup vault through presigned
  URLs, the platform verifies every object arrived at the size the export
  recorded, and the cluster's agent runs the restore inside the cluster -
  roles and globals first, then each database with the engine's own tools,
  against the Service and Secret `convert` generated. Ankra never holds the
  data, and nothing on this machine needs kubectl or a database client. The
  vault is picked automatically when the organisation has exactly one that
  is ready (`--vault` otherwise), the cluster defaults to the selected one,
  `--wait` follows the restore to completion or failure with every job's
  state as it changes (`--timeout` bounds only that wait, never an upload),
  and `ankra migrate restore-status <import-id>` reports on one started
  earlier. A dump above 5 GiB is refused before anything is registered -
  one presigned upload carries at most that much. A restore needs the `backups` feature,
  the converted stack applied, and a cluster agent that supports data
  restores; the platform names whichever is missing.
- **A converted database always gets a Service.** `ankra migrate convert`
  only generated a Service for workloads with published ports, so a compose
  database nobody exposed to the host - the usual case - was unreachable
  from the other workloads once in the cluster. Database images now get a
  Service on their default port regardless.
- **The Security Center is readable from the terminal.** `ankra security
  overview` prints the fleet's actionable totals, scanner coverage, the
  remediation candidates, and - first - how many vulnerabilities CISA lists
  as exploited in the wild, how many are past CISA's remediation deadline and
  how many CISA links to ransomware. `ankra security findings` lists findings
  exploited-in-the-wild first (CISA KEV, then EPSS, then severity), with
  `--known-exploited`, `--severity`, `--status`, `--fixable`, `--cluster`,
  `--addon`, `--namespace`, `--search`, `--sort` and `--order` mirroring the
  portal's filters; every row shows the CISA deadline in explicit tense and
  the EPSS probability. `ankra security finding <id>` quotes CISA's own entry
  - vulnerability name, deadline and required action - above every current
  occurrence, and `ankra security clusters` gives per-cluster posture with
  the known-exploited count. All four take `-o json` for reporting.

### Fixed

- **`--allow-repoint` says what a repoint does.** The flag's help claimed
  that resources the new source does not define are pruned, which reads as a
  prediction about the target's current contents and stopped a safe repoint
  from being run. `ankra cluster apply` now explains the actual sequence:
  Ankra writes the cluster's current state to the new source first and then
  syncs from it, and a target that cannot be written leaves the cluster
  unchanged.

## v0.14.0-rc4 — 2026-08-28

### Added

- **`ankra backup vaults provision` needs no arguments.** Letting Ankra set a
  vault up is now one command: the name defaults to `backups` (then
  `backups-2` and so on, against the vaults that already exist), the
  credential to the only one Ankra can provision from, and the region to that
  provider's usual one - and the command prints what it chose before it
  creates anything. Pass any of them to override. With several usable
  credentials it still asks which, rather than guessing.

### Changed

- **kubeadm is the default distribution (PLA-808).** `--distribution` on every
  provider `create` (Hetzner, OVH, UpCloud, DigitalOcean, Proxmox VE, HPE
  Morpheus) now defaults to `kubeadm` - vanilla upstream Kubernetes with
  Cilium. k3s stays available with `--distribution k3s` but is never a
  default; the `--kubernetes-version` and `ankra cluster upgrade` help lead
  with `kubeadm-versions`. Pairs with the platform default flip
  (ankraio/cluster#2086), which also makes an omitted `--cni` follow the
  distribution: Cilium on kubeadm, flannel on k3s.

### Fixed

- **A failed provisioning is no longer told to fix its access keys.** For a
  vault Ankra provisioned there are none to fix - the run never got as far
  as minting any - and re-verifying cannot make a bucket exist. `get` and a
  failed `provision` now say to delete it with
  `--destroy-provider-resources`, which also clears whatever the run had
  already created, and provision again. A vault that verified before, or one
  whose bucket you registered yourself, still points at the keys.
- **A vault name that cannot be looked up says why.** When listing failed,
  the name was handed to the API unchanged and came back as a uuid-parsing
  validation error - which reads as a typo for a name that was right. The
  command now reports the real reason it could not resolve the name; an id
  still needs no lookup at all.

## v0.14.0-rc3 — 2026-08-28

### Fixed

- **`ankra cluster encrypt -f` changes only the line it adds.** Recording an
  encrypted path used to re-encode the whole cluster file in the CLI's own
  YAML style, so a file the platform had written - indentless sequences,
  folded long lines - came back with every sequence re-indented: a 600-line
  diff in a GitOps repository to add one entry. The entry is now spliced into
  the file's own bytes at the position the parser reports, in whatever style
  the file uses; nothing else moves.
- **Section-qualified `encrypted_paths` entries are recognised.** The
  platform writes entries as `stringData.PASSWORD`; the CLI compared them to
  the bare key name, found no match, and appended every key again as a bare
  name - `--all-data` on a Secret with 15 recorded keys produced 31 entries
  in two spellings. Entries are now compared by key name with the section
  stripped, as the platform reads them, and a new entry is spelled the way
  the file already spells its entries. `ankra cluster manifests upgrade`
  merges its detected paths the same way.

## v0.14.0-rc2 — 2026-08-28

### Added

- **`ankra backup vaults delete --destroy-provider-resources` cleans up the
  cloud side too.** A plain delete still removes only Ankra's record and the
  keys it stored - the bucket and anything Ankra created for it stay in your
  account, and the command now says so. With the flag, the platform empties
  and deletes the bucket and removes the resource it minted (the UpCloud
  object storage service, the DigitalOcean Spaces key); the confirmation
  prompt names the bucket first, because every restore point in it is lost.
  It is refused for a vault that registers a bucket you created yourself.

## v0.14.0-rc1 — 2026-08-28

### Added

- **`ankra backup vaults provision` lets Ankra create the bucket.** Pass one
  of the organisation's Hetzner, UpCloud, DigitalOcean or Scaleway
  credentials (name or id) and a region, and the platform creates the
  bucket, mints or stores the access keys, verifies it and registers the
  vault; the vault reads `provisioning` until that finishes, and `--wait`
  blocks until it is `ready` (or exits non-zero with the failure excerpt).
  Hetzner alone is prompted for its Console-issued Object Storage key pair,
  because the Hetzner Cloud API cannot mint one; the other providers need
  nothing beyond the credential. `get` shows which credential a vault was
  provisioned via.

## v0.14.0-rc0 — 2026-08-27

### Added

- **`ankra backup vaults` manages the organisation's backup vaults from the
  terminal.** A vault is an S3-compatible bucket cluster backups are written
  to: `create` registers one (the access keys are prompted for when not
  passed as flags, the secret hidden, so they stay out of your shell
  history) and reports the platform's immediate credential check - a vault
  that fails verification exits non-zero with the failure excerpt instead
  of a green create. `list` shows every vault with its status and when it
  last verified, `get` describes one - including the failure excerpt when
  the last check failed - `verify` re-runs the check after rotating or
  fixing keys, and `delete` confirms first (or takes `--yes`). `get` and
  `list` take a vault name as well as an id and support `-o json|yaml`.
  Backup vaults are rolling out gradually: while the `backups` feature is
  not enabled for the selected organisation every vault command exits
  non-zero with "Backups are not enabled for this organisation." and says
  what to do about it (ask Ankra to enable the feature, or check `ankra
  org current`), rather than a bare 403.

### Fixed

- **`ankra cluster stacks clone` now reports the applications it cloned.**
  The platform clones application members alongside add-ons and
  manifests and answers with an `applications_cloned` count; the CLI
  dropped that field on decode and its summary listed add-ons and
  manifests only, so a stack whose only member was an application read
  as if nothing had been cloned. The count now decodes, prints as an
  `Applications:` line whenever it is non-zero, and is present in
  `-o json|yaml` output.

- **`kubectl` against an Ankra context no longer fails with "Cluster not
  found" when your selected organisation is not the one that owns the
  cluster.** The platform resolves a cluster reference inside the
  organisation the request is scoped to, so a cluster you have a grant on,
  whose id is right there in the kubeconfig server URL, came back as a bare
  404 that blamed the cluster - sending you to check access grants and RBAC
  for what was an organisation-selection problem. A cluster id the selected
  organisation does not have is now looked up across the organisations you
  belong to and the request runs against the one that owns it, so contexts
  written before `--org` was pinned into the exec args keep working across
  an `ankra org switch`. `ankra cluster kubeconfig add <id>` and
  `ankra cluster access` do the same lookup, so an id from another
  organisation writes a context pinned to the real owner instead of a
  context that 404s on first use. The cluster from `ankra cluster select`
  gets the same treatment, since that selection outlives the organisation
  chosen alongside it: omitting `--cluster` no longer fails where passing
  that very same id succeeds. Your selected organisation is never
  changed, and neither is an organisation you pinned yourself: `--org` and
  `ANKRA_ORG` are honoured as given, so a cluster that is not in the one you
  named is reported rather than quietly fetched from somewhere else. When
  the cluster genuinely is nowhere, the error names the organisation the
  request was scoped to and the ones that were searched - and only the ones
  that actually answered, so a lookup that failed is never reported as an
  absence.

## v0.13.0 — 2026-08-25

Promotes v0.13.0-rc0 through rc4 — the kubectl-shaped read surface
(`describe`, `events --for`, `top`, selector logs, `--previous`), stack
profiles carried from first draft to published version, organisation domains
and custom DNS zones, external MCP tool servers, the application CI
settings, env-secrets and registry-robot lanes, and working the AI board
from the terminal — and adds everything since: Ankra skills installed into
every AI assistant you use, the board's identity and the organisation's AI
kill switch from the terminal, and a true force stop on every self-managed
provider.

### Added

- **`ankra cluster <provider> stop <id> --force` is now a true force stop on
  every self-managed provider.** The platform side of `--force` grew teeth:
  it cancels every in-flight operation on the cluster, blocks new operations
  for 60 seconds so nothing freshly planned races the teardown, and lets the
  stop itself run no matter what - including on a cluster still being
  created. Proxmox VE and HPE Morpheus, the two providers whose `stop` had
  no `--force` at all, now accept it like the rest, and every provider's
  flag help spells out the full effect so you know an operation-cancelling
  stop is what you are asking for.

- **`ankra ai board-identity` and `ankra ai autonomy` put the AI settings a
  runbook needs behind a token instead of a browser session.** The sharpest
  gap cost real time: an organisation designates a board worker, every
  ticket escalates anyway, and the fix - giving the board an identity -
  existed only in the portal. `board-identity status` says outright when the
  board has none and names the fix, `provision` creates the identity
  (`--role operator|member|viewer`), and `revoke` stands it down.
  `ankra ai autonomy` is the organisation's AI switchboard: `status` reports
  whether AI is stopped, since when, by whom and why; `stop-all` and
  `start-all` work the hard kill switch; `pause --reason` and `resume` work
  the softer autonomy pause, and a pause reports what it switched off
  rather than claiming a silent success. The destructive verbs confirm and
  take `--yes`, and every command supports `-o json|yaml`.

- **`ankra skills install` now installs into every AI assistant you use, not
  just Cursor and Claude Code.** `--editor cursor|claude-code` covered two of
  the tools a team actually has open, so everyone else - Codex, GitHub
  Copilot, Windsurf, Gemini CLI, OpenCode, Cline, Zed, OpenClaw, the Claude
  app - either went without the skills or hand-copied them somewhere their
  assistant never read. `--client` replaces `--editor` (kept as a deprecated
  alias) and takes a repeatable, comma-separated list, `all`, or `auto`; with
  no `--client` at all, install now detects the assistants configured on the
  machine and does them together, falling back to Claude Code and Cursor when
  it finds none. `ankra skills clients` lists every supported assistant, what
  was detected, and where each one's skills would land.

- **Assistants that have no skills directory now get an index instead of
  nothing.** Claude Code and Cursor discover `SKILL.md` files by themselves;
  Codex, Copilot, Gemini, Windsurf and everything behind `AGENTS.md` do not.
  For those, install writes the skills into a client-neutral library and adds
  an index to the assistant's always-loaded instructions file - naming each
  skill, what it covers, and the path to open - inside the same managed block
  that already carried the Ankra routing rule. A project-scoped index names
  the repository-relative directory, so a committed `AGENTS.md` or
  `.github/copilot-instructions.md` works on every teammate's machine.

- **`ankra skills install --client claude-app` writes uploadable skill
  bundles.** The Claude app cannot read your filesystem, so install produces
  one `.zip` per skill - the skill directory at the archive root - ready to
  upload at claude.ai under Settings, Capabilities, Skills.

- **Ankra workflow commands are installed alongside the skills.** Skills are
  matched against whatever the user happens to say; a workflow is a named
  entry point for a job that spans several of them. `/ankra-ship-service`,
  `/ankra-connect-app`, `/ankra-triage`, `/ankra-promote`, `/ankra-harden`
  and `/ankra-profile` are written in each assistant's own command format
  (Claude Code and Cursor commands, Codex prompts, Copilot `.prompt.md`,
  Gemini TOML, Windsurf workflows, Cline workflows). Skip them with
  `--no-workflows`.

- **Two new skills for the parts of onboarding that had none, and a
  `/ankra-new-cluster` workflow.** `ankra-getting-started` is the ordered path
  from an empty organisation to a running application - credentials, the
  GitOps repository and the domain settled before the first cluster, because
  all three are wired at create time and painful to retrofit.
  `ankra-domains-dns` separates the four things that get conflated: the
  organisation root domain, a cluster's generated subdomain, a custom DNS zone
  served with your own credential, and the preview domain PR demos publish
  under - including that Ankra publishes nothing on a preview domain you own,
  so an unpublished wildcard costs you the URL and the TLS together.

- **Six new skills covering the work that spans several parts of the
  platform.** `ankra-applications` (source code to a running deployment on one
  or many clusters, registries, environment secrets, auto-deploy, PR demos,
  publishing as a catalogue add-on), `ankra-app-integrations` (wiring an app
  to an LLM gateway, Harbor, a database or an internal API with the
  credentials that already exist), `ankra-stack-profiles` (builder drafts,
  parameters, option sets, publishing and launching across a fleet),
  `ankra-troubleshooting` (operations, events, `--previous` logs, `top`,
  PromQL, and what each symptom classifies as), `ankra-security` (tokens and
  MCP scopes, roles, cluster access grants, credential scope, scanning
  findings, agent autonomy) and `ankra-ai-agents` (model provider and
  catalogue, MCP tool servers and per-tool role grants, agent runs and
  transcripts, the AI board).

### Changed

- **`ankra-cloud-clusters` and `ankra-managed-kubernetes` documented commands
  and flags that do not exist.** Between them they told an agent to run
  `ankra cluster ovh node-group|upgrade|scale|ssh-keys` (all removed in favour
  of the provider-agnostic `ankra cluster node-group|upgrade|scale|ssh-keys`),
  `--wait` on a provider `create` (never existed), `deprovision --auto-delete`
  (removed), `ankra cluster managed options` and `managed upgrades` (neither
  exists), `--provider mks` (it is `ovh_mks`), `--cluster-id` on
  `managed import` (it is `--provider-cluster-id`), and
  `--node-pool-autoscaling-min/max` / `--autoscaling-enabled` (they are
  `--autoscaling`, `--autoscaling-min`, `--autoscaling-max`). Every one of
  those makes an agent emit a command that fails. `ankra-cloud-clusters` also
  claimed `provision`/`deprovision` were a power-off, when deprovision is a
  teardown that releases cloud resources and uninstalls every stack.

- **`ankra-cloud-clusters` now covers cluster creation properly**: all seven
  providers, the discovery commands that list the regions and instance
  families a credential can actually deploy, k3s vs kubeadm and etcd topology,
  the `--external-cloud-provider` / `--include-networking` / `--include-dns`
  batteries, committing the generated stack to a GitOps repository with
  `--gitops-repository` at create time, OVH availability zones, and the
  difference between power, teardown and delete. A new `reference.md` carries
  the per-provider instance-family guide, role sizing, GPU node groups, the
  create-flag map and the cost traps.

- **`ankra-import-cluster` and `ankra-observability` taught the silently-broken
  `parents` shorthand.** Both showed `parents: - manifest: <name>` in their YAML
  examples, and the import skill's field reference endorsed it - but the
  parser requires a `kind` + `name` pair and silently drops anything else, so
  every dependency edge written that way vanishes while local and server-side
  validation both pass. This is the exact trap `ankra-stacks-addons` already
  warned about; the other two skills were teaching it. Both now use the
  `kind`/`name` form, and the import skill documents the trap and the
  `ankra cluster stacks list <stack> -o json` verification.

- **`ankra-alerts-webhooks` rewritten against the real `ankra alerts`
  surface.** The skill predated the CLI entirely - no commands, just concepts.
  It now covers destinations (webhook URLs, Slack/Teams bot channels via
  `destinations channels`, payload templates), routes (kind/severity/cluster
  filters, include/exclude modes, priorities and `--stop-on-match`),
  `routes preview` with the `--alert-id` dedup caveat, and the
  `test`/`test-url` delivery checks that exit non-zero for CI.

- **Gaps filled across the catalogue.** `ankra-helm-registries` gained the
  registry management surface (`registries create|sync|sync-jobs`, spec-file
  form, `--exclude-charts`) and chart discovery (`ankra charts
  list|search|info|values|template`); `ankra-observability` gained the
  metrics wiring and reading surface (`cluster metrics`, `top`);
  `ankra-getting-started` gained the organisation playground as the
  zero-credential first cluster; `ankra-cli` gained the support-request
  surface and the beta update channel; `ankra-gitops` gained
  `ankra cluster gitops status` and the create-time `--gitops-repository`
  wiring. `ankra-gitops` and `ankra-cicd` no longer credit syncing to
  ArgoCD - deploys roll out via the Ankra engine.

- **The `ankra-cli`, `ankra-sops-secrets`, `ankra-stacks-addons`,
  `ankra-cicd` and `ankra-platform-principles` skills now match the CLI they
  describe.** `ankra-cli` documented `ankra org select`, which does not
  exist - the command is `ankra org switch` - and predated applications,
  stack profiles, tickets, agents and the exit-code contract.
  `ankra-sops-secrets` documented `ankra cluster encrypt -f <file>`, which is
  not the signature: `encrypt` names keys with `--key` and takes cluster mode
  or file mode, and `encrypt --set` is how a new secret value is changed
  without committing plaintext first. `ankra-platform-principles` gained the
  routing map from a request to the right skill.

- **`ankra skills uninstall` with no `--client` now undoes what install
  did.** It resolves to the assistants that actually carry an Ankra install,
  rather than defaulting to Cursor, and a full uninstall also removes the
  workflow commands it wrote.

## v0.13.0-rc4 — 2026-08-25

### Added

- **`ankra tickets` works the AI board from the terminal, and answers the
  choice a blocked ticket is waiting on.** When an Ankra agent finds more
  than one way forward it blocks the ticket on a decision, and until now
  that choice could only be answered on the ticket page. `ankra tickets
  list` shows the board with a WAITING ON column that tells "your decision"
  apart from a plain block, a plan awaiting approval and a review; `ankra
  tickets get T-8` prints the agent's question with every option it offered,
  its summary, and a `*` on the one the agent recommends; `ankra tickets
  decide T-8 --option a` records that choice, `--answer "..."` answers with
  something else in your own words, and both together record the option with
  your note beside it. The answer lands on the timeline as a `Decision:`
  comment and the agent resumes from it without re-asking. `events`,
  `comment` and `transition` complete the lane, every command takes
  `-o json|yaml`, and a ticket is named by number (`8`, `T-8`) or UUID. An
  option that was never offered, and a ticket that is not waiting on a
  decision, are refused before the call is made. Requires cluster#1904 on
  the platform.

- **`ankra application registry robot` mints, rotates and revokes the push
  robot for one application.** Applications used to log in to the registry
  with a robot shared by the whole organisation, and an application
  publishing to a Harbor you operate got none from Ankra at all - you
  created one by hand and pasted it into the repository's Actions secrets.
  `robot ensure` mints the application's own robot and stores its login in
  the repository, `robot get` shows it, `robot rotate` replaces the secret,
  and `robot revoke` deletes it, so a leaked secret is rotated for that one
  application. On a registry you operate, name a credential with project
  administrator rights as `ankra application registry set
  --admin-credential <name>` and Ankra mints there too; without it your
  robots stay yours and untouched. Requires cluster#1868 on the platform.

- **`ankra application registry set` describes a registry whose repository
  layout is not Ankra's.** A monorepo's images were always addressed as
  `<project>/<app>/<component>`, so a registry that had been publishing
  `commerce-images/backend` for months looked unpublished.
  `--flat-repositories` uses `<project>/<component>` instead, and
  `--component-repository backend=commerce-backend` names a component's
  repository outright where the names differ altogether - both are used by
  publish readiness, the deploy gate and the generated workflows. Requires
  cluster#1878 and cluster#1884 on the platform.

- **`ankra application credential get|set` re-binds an application to
  another GitHub credential.** An application created against an App
  installation that cannot reach its repository had to be deleted and
  recreated; `ankra application credential set <application-id> --credential
  <name>` moves it instead, and `get` shows what it is bound to. Requires
  cluster#1878 on the platform.

- **`ankra cluster domain` reports the domain hostnames are actually
  published under.** An organisation serving its own domain from a custom
  DNS zone was still told its cluster domain was the generated
  `<cluster>.<org>.ankra.cc`. The command now prints a **Public domain**
  line whenever the resolved domain differs from the generated zone, and
  says which zone publishes it - the same value `${{ ankra.cluster_domain }}`
  resolves to. A backend too old to report one prints nothing extra.
  Requires cluster#1888 on the platform.

## v0.13.0-rc3 — 2026-08-25

### Added

- **`ankra cluster encrypt --key 'glob:<pattern>'` encrypts every key whose
  name matches - now and on every later platform re-encrypt, including keys
  added afterwards.** A pattern in `encrypted_paths` used to be escaped into
  a literal that matched nothing, and a hand-widened `encrypted_regex` in
  the sealed file was replaced by the platform's next write-back, so a
  Secret with a growing set of `DB_*` keys needed one `encrypt` per key and
  a new key committed in plaintext until someone noticed. `--key
  'glob:stringData.DB_*'` (only `*` is a wildcard; a leading `data.` or
  `stringData.` is accepted and ignored, as for an exact key) is recorded in
  `encrypted_paths` as written, which is the form the platform re-expands
  into the SOPS selector on every push. Exact keys keep their meaning
  byte-for-byte - a literal key containing a dot or a star is still matched
  literally; only the `glob:` prefix opts in. A pattern that matches no key
  fails, the same way a misspelled exact key does. Requires cluster#1867 on
  the platform. (PLA-798, support #1094)

- **`ankra org custom-dns-zones` serves your own zone from every cluster in
  the organisation - the ones you have and the ones you create next.**
  `ankra cluster custom-dns-zones` declared a zone on one named cluster, so a
  freshly created cluster came up with its ingress hostnames on your own
  domain dropped silently and its certificates stuck waiting for DNS that
  nothing was publishing. `ankra org custom-dns-zones add --zone <zone>
  --credential <name>` declares the zone once for the organisation: Ankra
  renders and reconciles one isolated external-dns for it on every cluster,
  each pinned to exactly the zone with its own record ownership, and on
  every cluster created afterwards without further declaration. `list` shows
  the organisation's declarations; `remove` withdraws one and tears down
  only the controllers Ankra rendered - clusters that declared the zone
  themselves keep theirs, and the zone's records are yours and are left
  untouched. `ankra cluster custom-dns-zones list` now shows a SOURCE column
  telling an inherited zone apart from the cluster's own, and a cluster's own
  declaration of a zone takes precedence over the organisation's on that
  cluster - the way to serve one zone with a different credential on one
  cluster. Requires cluster#1852 on the platform.

### Fixed

- **`ankra org domain --help` described the custom-domain lane as it was
  before cluster#1841.** It still said Ankra confines itself to one
  `<org_short_id>.<domain>` subzone and pins each cluster's external-dns to
  that cluster's subzone alone; a registered domain is adopted as the
  organisation's zone apex and every cluster's external-dns publishes
  anywhere under it. The help now says so, and points a zone hosted outside
  Ankra's own DNS account at `ankra org custom-dns-zones` instead.

## v0.13.0-rc2 — 2026-08-24

### Added

- **`ankra cluster custom-dns-zones` serves your own zones from a managed
  external-dns.** The external-dns Ankra provisions publishes only under the
  cluster's generated subdomain - its credential is scoped to that zone by the
  DNS provider - so ingress hostnames on your own zones were dropped silently
  and the answer was a hand-rolled second external-dns maintained outside the
  platform. Now `ankra org dns credentials create` stores your webhook
  credential (into the platform's secret store; no read surface returns it,
  because the URL embeds the token), and `ankra cluster custom-dns-zones
  add <cluster> --zone <zone> --credential <name>` has Ankra render and
  reconcile one isolated controller for that zone, pinned to exactly it with
  its own record ownership so it can never fight Ankra's controller, yours, or
  another cluster's. `list` shows what a cluster serves, `remove` withdraws a
  zone and tears down only the controller Ankra rendered - the zone's records
  are yours and are left untouched. Re-creating a credential under the same
  name re-points every binding at once, which is how a rotated token rolls
  out. (PLA-788)

- **`ankra credentials repositories <id|name>` says which repositories a
  GitHub credential can actually reach.** `credentials list` prints a REPOS
  count and nothing anywhere broke it down, so when the count disagreed with
  reality there was no way to see which repository was missing. That is
  exactly the state a customer spent a day in: a credential reporting one
  repository, up, available and freshly synced, while every call against the
  repository it was bound to answered 404. The new command reads the
  installation's repositories live from the provider, lists them against the
  repositories Ankra needs from that credential, and names the ones required
  but unreachable. Worth knowing why the old number misled: the REPOS count is
  a cache refreshed by a sweep that only runs while the credential reports
  healthy, so a credential that breaks stops refreshing the very rows that
  would show it broke. This command does not read that cache. A listing that
  could not be read is reported as exactly that, never as an installation that
  reaches nothing. (PLA-786)

## v0.13.0-rc1 — 2026-08-24

### Added

- **`ankra application env-secrets` sets the environment an application's
  manifests read.** The keys come from the application's own generated
  manifests, and until now the only way to answer them was the portal, so a
  pipeline that could create, build and deploy an application still stopped
  at the one step that made it run. `list` reports which keys exist and
  whether each has a value; `set` stores one; `delete` clears it; `apply`
  seals the stored values into the running deployments and rolls them.
  Storing is deliberately not applying, and `set` says so. A value only ever
  travels inbound: no route hands a stored value back, `list` never carries
  one, and nothing here prints one. Pipe the value or let it prompt rather
  than passing `--value`, which your shell records in its history. An empty
  value is refused on every path, so the `--value "$UNSET_VAR"` footgun cannot
  quietly store a secret the workload will never match, and a key that is not
  an environment variable name is refused before it reaches a request path.
- **`ankra application auto-deploy get|set` reads and flips push-to-deploy.**
  The read carries the newest build the platform observed on the tracked
  branch alongside the switch, so you can tell auto-deploy that is off from
  auto-deploy that is on and has had nothing to pick up. `set` requires
  `--enabled` explicitly: turning unattended deployment on and turning it off
  are both deliberate acts, and neither is a safe default to infer.
- **`ankra application settings get|set` reads and sets the organisation's CI
  runner label.** Generated pipelines used to carry a hardcoded
  `ubuntu-latest`, so an organisation whose GitHub-hosted runners are refused
  (Actions billing in arrears, or a spending limit) was handed a pipeline it
  could not execute. Any member may read it; only an organisation admin may
  change it. `--clear` returns future generations to the default.
- **`ankra application demo fix-build` repairs a branch with no demo image.**
  `demo build` is the check that reports no image exists for a branch, and it
  was already in the CLI; the remedy for exactly that answer was portal-only,
  because the endpoint had no bearer twin. It has one now (cluster#1717), so
  the check and its fix are finally on the same surface. Ankra applies its own
  deterministic fixes first and, when those cannot produce an image, dispatches
  a mission agent that investigates the repository and opens a pull request.
- **`ankra application manifest-addon` completes the add-on publishing lane.**
  `publish-addon` and `published-addon` already turned an application's
  manifests into a catalog entry and reported what that produced; there was
  then no way to inspect, compare, install or withdraw it without the portal.
  `get`, `diff --to`, `install --cluster-id`, `unpublish` and `delete` close
  that. `unpublish` and `delete` are not the same act and the prompts say
  which is which: unpublishing withdraws the catalog entry and leaves what is
  installed running, while deleting undeploys every installation that came
  from it.
- **`ankra cluster playground destroy <cluster_id>` tears the organisation's
  playground down.** The platform has served the DELETE since playgrounds
  shipped; only the CLI could not reach it, so `create` and `status` had no
  matching way out. It is idempotent — an environment already deprovisioning
  or removed answers its current phase rather than an error. It also has a
  second job: a live playground publishes a wildcard DNS record in the
  organisation's zone, that record is reconciled rather than written once,
  and it is therefore the one blocker of `ankra org domain set` that deleting
  cannot clear. Destroying the environment is what clears it.
- **`ankra org ai-environment get|set` reaches the preview settings.** The
  demo base domain, ingress class, TLS secret and certificate issuer are the
  fields you script when standing up on-demand environments on your own
  domain, and until now the CLI wrapped only the organisation's root domain
  through `ankra org domain`, a different setting on the same screen with a
  far wider blast radius. Only the flags you pass are written, and passing
  one with an empty value clears that field alone, so a script can set the
  base domain without disturbing an issuer somebody else configured. Saving
  reports back what previews will actually do: where they would be served
  over plain http, or where their hostnames will not resolve, the response
  says so rather than succeeding without comment. `ankra org domain --help`
  now names the preview domain as the separate setting it is and points at
  this command, which is the confusion that cost a customer a day.
  (PLA-773)

### Changed

- **`ankra cluster domain <cluster> --remove` now holds.** Removing a
  cluster's domain and watching it reappear about fifty seconds later, active
  and under the same label, was the reason the documented root-domain switch
  could not be completed: every removal was reverted before the next one
  could be made. The removal is now recorded as a deliberate act, so nothing
  re-creates the zone — not the external-dns Ankra runs on the cluster, and
  not the discovery that mints zones for clusters that have none. A read
  reports `Opted out: yes` for as long as the hold stands, which is what
  distinguishes a domain that is gone from one that is between passes.
  `--enable` withdraws the hold and re-creates the zone under the
  organisation's current root, under exactly the name it had before.
- **A refused `ankra org domain set` now separates the blockers you can clear
  from the ones you cannot, and names what to remove instead.** DNS records
  the platform publishes and re-asserts are listed apart from your own, with
  the writer named, because telling an admin to `ankra org dns delete` a
  record the playground provisioner writes back on its next pass is advice
  that cannot work. Live playground environments are listed outright, with
  the command that destroys them. The cluster-domain section now also says
  what those clusters' external-dns will do about the removal, and that a
  root switch never re-labels a zone — labels come from the cluster id, so
  `--txt-owner-id` and any GitOps path built from one survive the switch
  unchanged.
- **`ankra org domain --help` and `ankra org domain set --help` state what
  Ankra does to the domain you register.** Ankra creates one subzone,
  `<org_short_id>.<domain>`, and works only inside it: records already
  published at the apex or under any other name are never read, written or
  deleted, by the switch or by anything Ankra runs afterwards. A domain that
  already serves production hostnames is safe to register on that count, and
  it is now written down rather than something to infer.

### Fixed

- **`cluster get services` and `cluster get ingresses` print the address the
  load balancer actually has.** `EXTERNAL-IP` read `spec.externalIP`, a field
  the Kubernetes Service API does not have (the real ones are
  `spec.externalIPs` and `status.loadBalancer.ingress`), so the lookup
  returned nothing and the column rendered `<none>` for every service no
  matter what the API served. An ingress row hardcoded its `ADDRESS` and
  `PORTS` cells to empty. Both now follow kubectl's rule, reading
  `status.loadBalancer.ingress` and falling back to `<pending>` on a
  LoadBalancer with no address yet. This one cost a customer two wrong turns:
  reading `<none>` for 31 days, they reported their cloud load balancer as
  unprovisioned when it was healthy the whole time and only the column was
  lying. Addresses render in full rather than truncated, because these
  commands reject `-o wide` and a truncated address would reintroduce the
  same problem. (PLA-787)

- **Publishing a stack profile draft no longer reports a failure on work
  that succeeded.** Publishing does its whole job on the request path in one
  transaction (redact, derive parameters, insert the version, move
  latest/current), and on a profile of any size that can take longer to
  answer than the shared client's 30-second response-header deadline. The
  transport gave up while the platform was still working and the CLI printed
  `http2: timeout awaiting response headers`, on a publish that then landed.
  Publishing and instantiating a profile now ride a lane without that
  deadline, bounded by the overall five-minute timeout instead. If one does
  still time out, the CLI no longer calls it an error: it says the server may
  have completed the write, names the command that settles it, and names what
  a blind retry would do. Publishing a still-open draft twice mints two
  versions, which is exactly what the old message invited.

- **Every per-application command now accepts the application's name where it
  takes `<application-id>`.** `application list` prints names, so a name is
  what a user passes to `application branches`, `application demo list`,
  `application demo config get` and the rest — and each of those answered a
  bare `500 Internal Server Error`, because the name travelled to the
  backend's uuid-typed lookup unchecked. A name is now resolved through the
  same server-side search the `list` command uses, exactly the way
  stack-profiles, credentials and clusters already resolve theirs: a uuid
  passes straight through, an unknown name says to check
  `ankra application list`, and a name two applications share lists the
  candidate ids instead of picking one. The lookup walks every page of the
  listing, so a name still resolves in an organisation with more
  applications than one page holds, and a lookup that cannot run says so
  rather than sending the name on to be rejected. An empty argument — an
  unset variable in a script — is now refused as the usage error it is,
  instead of searching with no filter at all. A name that matches no
  application exits 3 (not found), the same code an id that does not exist
  already produced, and an ambiguous name exits 2. A lookup that cannot be
  completed — the listing failed, was unreadable, or was too large to read
  to the end — reports that instead of answering from what it managed to
  read. (PLA-786)

## v0.13.0-rc0 — 2026-08-22

### Added

- **`ankra org mcp-servers` registers external MCP tool servers agent runs
  can call — without a credential ever crossing the wire in a form the
  platform would store.** The group covers the whole lifecycle: `catalog`
  lists the curated adapters (Sentry, and friends) with the exact credential
  header form each provider expects; `add` registers a server; `get`, `list`,
  `update`, `remove`, `enable`, and `disable` manage it; `health` and `tools`
  probe reachability and the tool inventory; and `grants`/`grant`/
  `revoke-grant` gate which organisation role may call which tool. The
  interesting part is `add --secret-header`: the CLI first stores the header
  value in an organisation secret slot and registers the server with the
  slot's `${SECRET_SLOT:<id>}` sentinel, because the backend refuses
  plaintext under sensitive-looking header names — pass
  `Authorization=<value>` inline, or just `Authorization` to be prompted with
  hidden input. Pairing `add --adapter <key>` with no `--allowed-tools` seeds
  the allow-list from the adapter's recommended tools, so the safe default
  configuration is also the laziest one to type. Servers resolve by name or
  id everywhere, deletion confirms unless `--yes`, and every read supports
  `-o json|yaml`.
- **`ankra cluster logs --previous` reads the log a crash-looping container
  left behind.** When a container is in CrashLoopBackOff the only output
  worth reading belongs to the instance that already died, and nothing in
  the CLI could reach it — the one case where you most want to avoid handing
  out a kubectl grant was the one case that forced you to. `--previous`
  (`-p`) asks the platform for that terminated log; because the log is closed
  it is always a bounded read, so it ends on its own instead of hanging on a
  stream that can never produce another line. It needs a platform and a
  cluster agent that carry the parameter; an older agent serves the current
  container's log instead.
- **`ankra cluster logs -l <selector>` and `--all-containers`** read more
  than one container per invocation. A three-replica Deployment used to mean
  listing pods to learn the hashed names and then three separate commands;
  now one selector reads them all, and `--all-containers` expands each pod to
  every container it declares — init and ephemeral containers included, which
  is where a stuck pod's real error usually is. With more than one target
  each line is prefixed `[pod]` or `[pod/container]` so the interleaved
  output stays attributable, and a following read is capped at five
  concurrent streams rather than silently opening one connection per replica.
  `logs` also gains `-o json|yaml`, which groups the output per target for a
  pipeline to parse.
- **`ankra cluster describe <kind> <name>`** answers "why is this not ready?"
  in one call. `cluster get` returned lists and manifests, so conditions,
  per-container state, and the object's events had to be assembled by hand
  from three commands. `describe` prints the object's conditions, a pod's
  container statuses with the detail that explains them (CrashLoopBackOff and
  its exit code, ImagePullBackOff and its registry error, restart counts),
  and the events whose `involvedObject` is that resource. Kubectl's
  spellings work — `pod`, `pods`, `po`, `Pod` — and a kind outside the
  built-in set is reachable with `--group`/`--api-version` (both are
  required, so a custom resource that does not serve `v1` fails with a clear
  message rather than an empty read). `-o json|yaml` emits
  `{object, events}`. Describing a Secret redacts its values to byte counts
  and lists the keys, like `kubectl describe` does: the point of these
  commands is to need a kubectl grant less often, not to make reading secret
  material easier than kubectl makes it. Use `cluster get secrets <name> -o
  yaml` when you actually want the values.
- **`ankra cluster events --for <kind>/<name>`** scopes an event listing to a
  single object's `involvedObject` with a server-side field selector, rather
  than filtering a namespace-wide list by name. That is the difference
  between "the pod is Pending" and "no node matches the nodeSelector".
  `--type Normal|Warning` narrows further, and `-o json|yaml` prints the raw
  events. `ankra cluster get events` gains the same two flags while keeping
  its existing output, its `[name]` argument, and its `resource_responses`
  envelope under `-o json|yaml`, so nothing already scripted against it
  changes.
- **`ankra cluster top pods|nodes`** shows live CPU and memory from the
  Kubernetes metrics API. `cluster metrics` queries Prometheus, which is the
  right tool for trends but the wrong one for "which container just got
  OOMKilled" — and it needs Prometheus to have been installed at all.
  `top` reads metrics-server directly, so it works on any cluster that has
  one. `top pods` takes `--containers` for a per-container breakdown and
  `--sort-by cpu|memory`; `top nodes` shows usage against each node's
  allocatable capacity. A measurement is only meaningful live, so the read
  bypasses Ankra's resource cache, and an answer that comes back from the
  cache anyway (an offline cluster) is refused rather than rendered as if it
  were current.
- **Profile choices can now be authored from the terminal, and `apply` can
  show what it would deploy.** `stack-profiles drafts annotate` gains
  `--default`, `--type`, `--enum` and `--required`, and `--add --enum a,b`
  declares an input the draft does not have - such as a **Model size** choice
  that no manifest references - born with its enum values as choices, which
  is what keeps the platform from dropping it on the next save. The new
  `stack-profiles drafts options set <draft>
  --parameter model_size --value 32b --set model_id=Qwen/Qwen3-32B --set
  max_model_len=28672` adds or updates one choice and the inputs it answers
  (declaring the input if the draft does not have it yet; `--unset` drops an
  assignment; `options remove` drops the choice), with the rules
  publish enforces - no secret targets, no choice driving another choice -
  refused on the spot. `drafts get` lists each input's choices. And
  `stack-profiles apply --dry-run` prints every input with the value it
  would deploy with and where that value comes from (`--set`, `choice
  model_size=32b`, `default`), names the required inputs still unset, never
  echoes a secret, and creates nothing - no cluster needed.
- **`ankra stack-profiles get` shows what each choice of an input sets.** A
  profile can now offer an input whose choices answer other inputs — a
  **Model size** of `8b` or `32b` that also moves the model id, the context
  length and the model-store size. Below the parameters table, `get` prints
  a `Choices for <input> (--set <input>=<value>)` block per such input,
  listing every choice, its label and reasoning, and the `sets name=value`
  lines it applies, so you can read what `--set model_size=32b` will do
  before you `apply`. The platform resolves the choice server-side, so
  `apply --set model_size=32b` with no other bindings deploys the whole
  set; a `--set` of your own on one of those inputs still wins.
- **`ankra org domain get|set` reads and writes the organisation's own Ankra
  root domain.** Every Ankra-generated hostname — the organisation's delegated
  DNS zone, its clusters' domains, and the preview hostnames built from them —
  nests under a root domain that defaults to `ankra.cc`. Registering your own
  was portal-only; it is now scriptable against the same backend setting the
  portal writes (AI > Settings > Workspaces, "Custom Ankra domain").
  `set --default` returns the organisation to the platform default. When the
  switch is refused because zones or records still live under the old root,
  the error lists exactly which cluster domains and which DNS records are
  blocking it, and the command that removes each.
- **`ankra org dns zones` lists every cluster domain in the organisation.**
  The inventory a root-domain switch has to clear: cluster, zone fqdn, and
  state. Previously the only way to discover a cluster's domain was
  `ankra cluster domain <cluster>`, which created one where none existed.
- **`--sort <column>` and `--order asc|desc` on the main list commands**
  (`cluster list`, `cluster addons list`, `org list`, `credentials list`,
  `tokens list`) let you order the output by any rendered column — e.g.
  `ankra cluster list --sort created --order desc` puts the newest clusters
  first. Sorting happens on the raw values, so timestamp columns order
  chronologically rather than by their "2 days ago" display text, and the
  same order applies to `-o json|yaml`. Without `--sort` the server order is
  unchanged, and an unknown column exits with the usage code and lists the
  valid ones. (`helm registries list` keeps its existing server-side
  `--sort-by`/`--sort-order` flags.)
- **`ankra stack-profiles` now carries the profile's whole life, not just the
  read half.** The portal could create a profile from a deployed stack, edit
  its catalogue metadata, snapshot new versions, roll the current-version
  pointer, diff versions, list the fleet's deployments, share it with other
  organisations, review community suggestions, run throwaway demos, manage
  its logo, and delete it — the CLI could only list, get, export, import, and
  apply. New subcommands close the gap: `create`, `update`, `delete`,
  `save-version`, `set-current-version`, `version`, `diff`, `deployments`,
  `share list|add|remove`, `suggestions list|get|approve|reject|withdraw`,
  `demo list|launch|detail|logs|stop`, and `logo get|set|clear`. The
  `drafts` family gains `validate`, `rebase`, and `submit-suggestion`.
  `delete` prompts for confirmation unless `--yes` is passed; every new
  command supports `-o json|yaml`, and `stack-profile`/`stackprofiles`/
  `stackprofile` now resolve as aliases of the family.
- **`ankra application ai-config get|set|clear`** reads, replaces, and resets
  the per-application AI lane configuration (pull request review, demo URL,
  and the rest) that the portal's application Settings page manages — `set`
  takes the JSON document `get -o json` prints, and `clear` returns the
  application to the organisation's defaults after a confirmation.
- **`ankra application publish-addon` and `published-addon`** publish the
  application's generated manifests to the organisation catalogue as a
  manifest add-on and read the published state back.
- **`--include-dns` on every `ankra cluster <provider> create`** (UpCloud,
  DigitalOcean, Hetzner, OVH, Proxmox, Morpheus) completes the create flag
  trio beside `--external-cloud-provider` and `--include-networking`. The
  platform gives a new cluster its own subdomain under `ankra.cc` and installs
  external-dns unless asked not to, and the CLI had no way to ask: every
  CLI-created cluster took the delegated subzone whether or not it wanted one.
  It stays on by default, matching the portal wizards and the server, and is
  independent of the other two — `--include-dns=false` keeps the ingress
  stack.
- **`ankra cluster scaleway|proxmox|morpheus bastion`** now exists. The
  platform mounts the bastion health and diagnose endpoints for all seven
  providers that carry node groups, but the CLI registered the group on only
  four, so `ankra cluster proxmox bastion status` did not exist even though
  the endpoint behind it answered. Each group carries what that provider can
  actually do: `status` everywhere, `diagnose` on Proxmox and Morpheus, and
  neither `resize` (the CLI has no bastion instance-type call for these three)
  nor `diagnose` on Scaleway, whose managed Public Gateway is probed by the
  health loop but has no SSH job lane.
- **`ankra cluster upcloud create --cni`** selects the container network
  interface for k3s clusters (`flannel`, `calico`, or `cilium`; the platform
  default applies when omitted), closing a gap where the CNI was only
  choosable from the portal wizard and the API.

### Fixed

- **`ankra cluster domain <cluster>` no longer creates a DNS zone when you
  are only looking.** The command's help called it an idempotent lookup, but
  it was backed by a POST: checking a cluster's domain enabled one on a
  cluster that did not have it — and every zone under the old root blocks an
  organisation root-domain switch, so the read-looking command added blockers
  to the very migration an operator was trying to run. The plain command is
  now a read (a cluster without a zone reports state `none`), and creating a
  zone is the explicit `--enable`. Scripts that relied on the bare command to
  enable a domain must add `--enable`.
- **`ankra cluster apply` no longer drops a manifest's or addon's `group`
  label.** The platform's IaC export writes an organizational `group` on
  manifests and addons, but `apply` never read the key and the request it
  built had nowhere to put it. Applying an exported ImportCluster therefore
  flattened every group in the stack — silently, with `apply` and `validate`
  both reporting success, and permanently, because apply also prunes what the
  file no longer declares. Groups now survive the clone-edit-apply round trip,
  and a `group:` that is not a quoted string is rejected instead of quietly
  becoming no group at all.

- **`ankra cluster upcloud create` no longer pins a network range that the
  platform refuses.** `--network-ip-range` defaulted to the literal
  `10.0.0.0/16` and the CLI always sent it, so on any UpCloud account that
  already holds a `10.0.x` private network the create came back
  `400 ... Network address 10.0.0.0/16 overlaps with an existing private
  network`. The guide's terminal equivalent of the create wizard broke exactly
  as printed. The flag now defaults to unset and an unset range is left out of
  the request, so Ankra derives a range that is free in the account — what the
  API, AI and portal lanes already do. Pass `--network-ip-range` only to pin a
  specific range.

- **`ankra cluster apply` no longer drops an addon's values or a stack's
  variables on the way to the platform.** Two keys of the ImportCluster
  dialect were never read: `configuration.values_base64` on an addon, and the
  stack-level `variables` map. Both are what the platform's own IaC export
  emits, so applying an exported file — the ordinary clone-edit-apply loop —
  installed every addon with chart defaults and left the stack with no
  variables at all, while `apply` and `validate` both reported success. The
  add-ons then ran unconfigured and the manifests reached the cluster with
  `${VAR}` still in them, failing typed fields outright. Apply now sends both,
  and a `configuration:` block it cannot turn into values is an error naming
  what it did find, rather than a silent fall back to chart defaults — both
  when the block uses a key this CLI does not read and when it gives a key
  the CLI does read the wrong type, such as a nested map where
  `values_base64` takes a string. A key it cannot read sitting *beside* one it
  can is a warning on stderr naming the ignored key rather than an error, so a
  file from a newer Ankra still applies.
  Because apply also prunes what the file does not declare, a config-only
  apply used to mis-configure and wipe in the same run.

- **`ankra cluster apply` can now read a manifest's `manifest_base64`, so an
  exported ImportCluster applies as exported.** The platform's IaC export
  writes a stack's manifests as `manifest_base64`, but apply only ever read
  the inline `manifest` or a `from_file` path and rejected anything else with
  "a manifest must set either 'manifest' (inline YAML) or 'from_file'" — so
  the ordinary clone-edit-apply loop failed on every exported file until each
  manifest was hand-decoded back into YAML. Unlike the addon values drop above
  this at least failed loudly, and nothing was ever mis-deployed. Apply now
  accepts the encoded form and passes the same string through to the platform,
  refusing content that is not base64 or does not decode to valid YAML.
  `manifest` and `from_file` keep precedence where a file sets more than one.

- **`ankra cluster logs --follow=false` now asks the platform for a bounded
  read instead of guessing when the backlog ended.** The route only ever
  followed, so the CLI had to infer the end of the tail from a two-second gap
  with no new line — which cost every scripted `--follow=false` run those two
  seconds, and cut the tail short whenever a busy pod happened to pause.
  The request now carries `follow=false`: the cluster snapshots the selected
  tail and closes the stream itself, and the CLI exits on that. Against a
  platform that predates the parameter nothing changes — the old idle-gap
  drain still ends the read. The stream's terminating frames ("stream
  complete", "stream idle timeout") also stop being printed as though they
  were log lines, in both follow and non-follow mode.

- **`ankra application add` no longer inspects the wrong repository when it is
  run from inside a Git hook.** `git commit` exports `GIT_DIR`,
  `GIT_INDEX_FILE` and friends into every hook it runs, and those variables
  outrank the directory the CLI addresses, so the command run from a
  pre-commit hook — or from `git rebase --exec`, or a CI step wrapped in one —
  read the hook's repository instead of the path it was given: the wrong
  owner/name, the wrong branch, and no error for a path that is not in a
  repository at all. Git invocations now drop the inherited
  repository-binding variables while keeping the user's SSH, credential and
  proxy settings.

### Changed

- **`ankra cluster <provider> bastion resize` no longer claims the node is
  resized before the cloud has touched it.** The command reported
  "resized to '<type>'" as soon as the platform had recorded the new instance
  type, while the provider's update job — the power-off, the resize, the
  power-on — had not started. The platform now hands back the operation
  carrying that job, so the confirmation says the instance type was *set*,
  names the operation, warns that SSH and NAT drop while the node cycles, and
  points at `ankra cluster operations list <operation_id>` for the part that is
  still running. A write that scheduled no cloud work (a stopped cluster, whose
  new type applies on start, or a resize already in flight) says so instead of
  printing an id there is nothing to poll for. `-o json|yaml` gains the
  matching `operation_id` field.

## v0.12.0 — 2026-08-18

Closes the loop on node-group cloud-init. A node group could already carry a
user-data document, but only over the raw API, and nothing could show what it
did on first boot; `--user-data-file` attaches the document and
`nodes cloud-init-log` reads the output back, so a provisioning script is no
longer debugged blind. Also makes `cluster reconcile` report the operations it
triggered instead of printing an empty success line on every cluster kind.

### Added

- **`ankra cluster <provider> nodes cloud-init-log` reads a node's first-boot
  output.** Node groups can carry a cloud-init user-data document, but its
  execution was invisible: there was no node-log surface, and the bastion SSH
  key belongs to the platform, so a failed provisioning script could only be
  inferred from symptoms. The new subcommand fetches `cloud-init status` and
  the tail of `/var/log/cloud-init-output.log` over the platform's bastion
  lane as a tracked read-only operation, for Hetzner, OVH, UpCloud,
  DigitalOcean, and Scaleway clusters.

- **`ankra cluster node-group add --user-data-file` attaches a cloud-init
  document to a new node group.** The platform applies it verbatim at first
  boot on every instance the group ever creates, replacements included (OVH
  clusters only). A file flag rather than a string flag, because the document
  is multi-KB YAML; documents over the platform's 65535-byte cap are refused
  before the request is sent.

- **`ankra cluster <provider> bastion status` and `bastion diagnose`.** The
  bastion is the one host the cluster agent sits behind, so when it goes down
  the agent goes quiet with it and the CLI had nothing to say about why —
  the platform's own verdict and its SSH diagnosis were reachable only from
  the assistant. `bastion status` prints the recorded verdict (reachable or
  not, which hop a failed probe stopped at, how many probes have failed in a
  row, and when it was last checked) without touching the host, so it answers
  even while the bastion is unreachable. `bastion diagnose` dispatches the
  provider's read-only diagnose job — sshd configuration, failed-login volume,
  disk, failed units, journal errors, listening ports, pending security
  updates — and blocks for its report, handing back an operation id to poll
  with `cluster operations list` if the job outruns the platform's two-minute
  wait. Both accept `-o json|yaml`. Providers whose gateway carries no
  diagnose job say so in `status` rather than offering a command that would
  only refuse.

- **`ankra demo deploy` selects and tunes individual components.** A monorepo
  demo is deployed as one pod per component, but the CLI was still
  single-workload: one global `--image-tag` and `--container-port` for the
  whole demo, and `demo list`/`demo detail` printed the raw JSON document the
  components were buried in. `--component NAME` (repeatable) narrows a launch
  to the components you name, `--component-tag`, `--component-port` and
  `--component-path` tune one component each, and `--entry-component` names
  the component that owns the demo host's root path instead of leaving it to
  the backend's heuristic. Omitting them all still deploys every recorded
  component, and the demo output now reports components rather than raw JSON.

### Fixed

- **`ankra cluster reconcile` now reports what actually happened.** It parsed
  a response shape the API never returns and printed an empty
  "Reconciliation request completed:" with exit 0 on every cluster kind,
  which read as "reconcile does not work for this cluster". It now reports
  "Reconciliation triggered: N operation(s) created", or the explicit zero
  case when stored state is already in sync.

- **`ankra application add` declares the image registry at create time.** The
  registry an application publishes to could only be declared after it
  existed, with `application registry set` — but the setup job generates the
  build workflow from the declaration the application is *created* with, so
  an application onboarded from the CLI got a workflow that logs in to the
  organisation's own Ankra registry and had to be regenerated by hand.
  `--registry-url` (with `--registry-credential`, `--registry-api-url`,
  `--registry-pull-secret`, `--registry-username-secret`,
  `--registry-password-secret` and `--registry-manage-actions-secrets`,
  mirroring `application registry set`) declares it up front, and the created
  application reports the registry it landed on. Without `--registry-url` the
  application still publishes to the organisation's Ankra registry project,
  exactly as before; the other registry flags need it and are refused on
  their own.

- **`ankra cluster validate --cluster` accepts a name again.** It sent the
  name verbatim as the `cluster_id` query parameter and the API rejected it
  with a 422 `uuid_parsing` error, while every other cluster command takes a
  name or an ID. It now resolves names through the clusters list; a 36-char
  UUID still passes through untouched.

## v0.11.0 — 2026-08-18

Promotes v0.11.0-rc1 through rc4 — stack profiles addressable by name,
organisation DNS records, application registry declaration, managed-cluster
discover and import, and the cluster domain command — and adds everything
since: a non-following logs mode, per-step operation results, alerting as
code, stack-profile drafts, and forced reclaim of leaked cloud resources.

### Added

- **`ankra cluster logs --follow=false` prints the backlog and exits.** The
  logs command always followed, so piping it into `grep` or a script hung
  until you killed it. `--follow` (`-f`) now defaults to true, keeping the
  familiar `kubectl logs -f` behaviour, and `--follow=false` returns as soon
  as the current backlog has drained.
- **`ankra cluster operations steps --results` shows what each step actually
  did.** The steps table only ever carried a status and an error excerpt; the
  scheduler's per-step result payload - the resources a teardown deleted,
  skipped, or failed to reclaim, or the full error body behind a truncated
  excerpt - was recorded by the platform but reachable only through the API.
  `--results` fetches it and prints each step's result under the table, and
  `-o json|yaml` gains a `step_results` list in step order so scripts can join
  it without a second call. Steps that never finished are listed as having no
  result rather than silently omitted.
- **`ankra alerts destinations` and `ankra alerts routes` bring alerting as
  code to the terminal.** Where alerts go and which notifications reach them
  was portal-only until now. `destinations list|get|create|update|delete`
  manage the webhook and chat-channel receivers (Slack, Microsoft Teams,
  Discord, PagerDuty, or any custom URL; channel-based Slack and Teams
  destinations take `--channel-id`, and `destinations channels` lists the
  channels the Ankra bot can post to), `destinations test` and `test-url`
  fire a sample notification and exit non-zero when the receiver rejects it,
  and `routes list|create|update|delete|test` decide which notifications
  reach which destination by kind, severity, cluster, and source, in priority
  order with include/exclude modes. Updates send only the flags you pass, so
  the rest of a destination or route stays untouched. Everything uses the
  same personal access token as the rest of the CLI and honours `-o json` and
  `-o yaml`, so the whole alerting setup can be scripted, diffed, and applied
  from CI alongside your clusters.
- **`ankra stack-profiles drafts` edits and publishes profile versions from
  the terminal.** Open a draft on an existing profile (or seed one from a
  deployed stack), see every parameter with `get`, and give each one the
  guidance the launch form shows under its field with
  `annotate --parameter <name> --description "..."` — this is how a profile
  author instructs the person filling in variables and secrets. `publish`
  cuts the version, `list` and `delete` manage open drafts. Drafts were
  browser-and-MCP-only until now; the platform gained bearer-token twins for
  the whole draft family in the same change.
- **`--force` on cloud cluster deprovision and stop now reclaims leaked cloud
  resources, on every provider that leaks them.** `ankra cluster deprovision
  --force` and the provider `stop|deprovision --force` commands (UpCloud,
  Hetzner, OVH, DigitalOcean, plus Scaleway stop) also delete the cluster's
  CSI-provisioned storage volumes and load balancers, which these providers
  never reclaim on their own and which keep billing after a plain stop or
  terminate. The backend deletes exactly the volumes recorded for the
  cluster - never another cluster's disks. Without `--force`, behaviour is
  unchanged, with one exception: a plain Hetzner stop or deprovision now
  keeps the cluster's volumes (previously it always deleted them, so a
  stopped Hetzner cluster restarted with empty storage); pass `--force` to
  get the old reclaim-everything behaviour.
- **`ankra application demo` speaks multi-component.** A monorepo demo runs
  every component as its own pod, and the CLI could neither steer that nor
  show it. `demo deploy` gains `--component` (repeatable) to narrow a launch
  to the components you name, `--component-tag`, `--component-port` and
  `--component-path` to tune one component each as `NAME=VALUE`, and
  `--entry-component` to say which one owns the demo host's root path
  instead of leaving it to the entry heuristic. Because the selection and
  the overrides ride the same request field, an override may only name a
  component `--component` selects, and the CLI says so rather than letting
  the launch silently narrow to that one component. `demo list` and
  `demo detail` now print a summary — every demo's components, which one is
  the entry, and each component's port, ingress path, image tag and pod —
  instead of a raw JSON document; `-o json` and `-o yaml` still emit the
  untouched payload, including the resource inventory and events the
  summary leaves out. `demo logs` gains `--component` to read the pod
  belonging to one component of a multi-component demo (and `--pod` to name
  one directly), so reading the API's logs no longer means finding its pod
  by hand.

### Fixed

- **`ankra application demo logs --tail` is no longer ignored.** The flag
  was sent as `tail`, but the endpoint reads `tail_lines`, so every demo log
  fetch silently returned the backend's default tail no matter what you
  asked for.

## v0.11.0-rc4 — 2026-08-17

Release candidate, superseding v0.11.0-rc3. Install it with `ankra config
beta enable && ankra upgrade`, or download a binary from the release page;
`ankra config beta disable` returns you to the stable channel.

### Added

- **`ankra cluster managed discover` and `ankra cluster managed import` adopt
  clusters that already run at the provider.** Discovery lists every managed
  Kubernetes cluster a credential can see (DOKS, UKS, GKE, OVH MKS, AKS, EKS,
  Kapsule) with its provider cluster id, location, version, status, node
  count, and whether it is already imported. Import adopts one by provider
  cluster id: the backend fetches the kubeconfig through the provider API and
  installs the agent automatically, so there is nothing to run against the
  cluster. Both were portal-and-API-only until now.
- **`ankra cluster domain` shows the cluster's generated public domain,
  enabling it if needed.** The call is idempotent, so it doubles as a lookup:
  a cluster that already has its ankra.cc zone reports the existing domain, a
  cluster without one gets the zone queued and reports it as `pending` until
  it publishes. Previously the day-2 opt-in was a raw API call.

## v0.11.0-rc3 — 2026-08-16

Release candidate, superseding v0.11.0-rc2 — rc2 predates `ankra org dns`
entirely. Install it with `ankra config beta enable && ankra upgrade`, or
download a binary from the release page; `ankra config beta disable` returns
you to the stable channel.

### Added

- **`ankra org dns` manages records in the organisation's own delegated
  zone.** Every organisation gets a zone of its own (`ankra org dns zone`), and
  until now the only way to point a memorable name at an add-on's generated
  hostname was the API. `list` shows every record with its reconciliation
  state, `add <name> <type> <content>` creates a CNAME, A, or TXT record under
  the zone — the zone fqdn is appended for you — `update` re-points one at a
  new target, and `delete` removes it. Records reconcile asynchronously, so a
  new or edited record reads `pending` until it is published to the
  authoritative nameservers and then turns `active`; a record that could not
  be published reports why in the `ERROR` column rather than sitting silently
  wrong.

- **`ankra application registry` points an existing application at a container
  registry you operate.** An application publishes into the organisation's own
  Ankra registry project unless it declares otherwise, and that declaration
  could previously only be made when the application was created — so an
  organisation whose builds push to their own Harbor had no way to correct an
  already-onboarded application, and Ankra kept reporting its images as never
  published. `registry get` shows the effective registry, the host and project
  it resolves to, and the image repository each component is expected to
  publish to, so you can compare them against where your builds actually push.
  `registry set --url oci://<host>/<project> --credential <name>` declares one;
  `registry clear` returns the application to the organisation's own registry.
  Setting a registry without `--credential` is allowed but warns, because
  without a credential Ankra can describe where the images live and neither
  read nor pull them.

### Fixed

- **`ankra org dns --ttl` no longer offers a range the nameservers will not
  serve.** The help advertised `30..604800`, but a record's ttl is capped at
  one day upstream, and anything longer used to be quietly reduced to that —
  so a record you set to a week reported a week back to you while resolvers
  were told one day. The accepted range is now `30..86400` in both the help
  and the platform, and a longer value is refused outright instead of being
  silently rewritten.

## v0.11.0-rc2 — 2026-08-16

Release candidate, superseding v0.11.0-rc1 — **do not use rc1**, the headline
change in it did not work.

### Fixed

- **Profile-name resolution actually resolves now.** The lookup asked the
  profiles endpoint for a page of 200, but it caps `page_size` at 100 and
  rejects anything larger. Because the lookup falls back to the reference you
  typed whenever listing fails — so that a working profile id never breaks on
  account of an unrelated endpoint — every name silently fell through to the
  API and produced exactly the `uuid_parsing` error the feature exists to
  remove. `ankra stack-profiles get llm-d` works in rc2; in rc1 it still
  failed.

## v0.11.0-rc1 — 2026-08-16

Release candidate. Install it with `ankra config beta enable && ankra upgrade`,
or download a binary from the release page; `ankra config beta disable` returns
you to the stable channel.

### Fixed

- **`ankra stack-profiles get`, `export-iac` and `apply` accept a profile
  name.** `stack-profiles list` prints a NAME column, so a name is the obvious
  thing to pass to the next command — but doing so leaked the API's raw
  validation dump (`uuid_parsing`, `expected an optional prefix of urn:uuid:`),
  which says nothing about what to do instead. The commands now resolve a name
  to its id, report an unknown name by pointing at `stack-profiles list`, and
  list the candidates when a name is ambiguous. A profile id is still used
  directly and costs no extra lookup, and if the profile listing is
  unavailable the reference is passed through unchanged so a working id never
  fails on account of it.

- **`--version` accepts the `v1` form the CLI itself prints.** Every Ankra
  surface labels profile versions `v1` — the LATEST column in
  `stack-profiles list`, and "Latest version: v1" in `stack-profiles get` —
  but the flag was an integer, so copying that value back gave
  `strconv.ParseInt: parsing "v1": invalid syntax`. Both `1` and `v1` now
  work, and anything unparseable fails with a message naming the accepted
  forms. Omitting the flag still means "the profile's current version".

### Security

- **The binaries are built with Go 1.26.6.** The toolchain was pinned to Go
  1.26.5, against which `govulncheck` reports four reachable standard-library
  vulnerabilities — including `encoding/asn1` recursion depth (GO-2026-5972)
  and Punycode label handling in `net/http` (GO-2026-5026) — all of them
  reachable from the CLI's own HTTP client and login paths, and all fixed in
  Go 1.26.6.

## v0.10.1 — 2026-08-13

### Fixed

- **The OVH availability-zone flags render their type in `--help` again.**
  Cobra takes the first backquoted text in a flag's usage string as the
  value placeholder, so the pointer to the discovery command turned into the
  placeholder itself and `--availability-zones` displayed as
  `--availability-zones ankra cluster ovh regions --with-zones` instead of
  `--availability-zones strings`. The flags themselves were never affected.

## v0.10.0 — 2026-08-13

Promotes v0.10.0-rc1 and rc2, and adds everything since: OVH availability
zones, a playground cluster per organisation, demo inspection and repair,
power schedules, GitOps repointing, and OpenRouter key management.

### Added

- **OVH availability zones.** An OVH cluster in a 3-AZ region such as
  `EU-WEST-PAR` or `EU-SOUTH-MIL` used to put every node, control plane
  included, in whichever single zone OVH picked. `ankra cluster ovh create`
  now takes `--availability-zones eu-west-par-a,eu-west-par-b,eu-west-par-c`
  to spread a cluster (control planes and etcd per role, workers per node
  group), and `node-group add` takes `--availability-zone` to pin one group
  to a single zone, which is what a workload on zonal storage needs. Zone
  names are region-scoped, so `ankra cluster ovh regions --with-zones` lists
  each region's type and its zones rather than leaving you to guess the
  spelling. Spreading requires at least 3 control planes, since fewer cannot
  hold etcd quorum through the loss of a zone.
- **`ankra cluster playground` creates a throwaway cluster for an
  organisation**, for trying the platform out without provisioning
  infrastructure of your own.
- **Demo inspection and repair.** `ankra application demo` gains detail,
  logs, config and fix subcommands, so a demo that will not come up can be
  diagnosed and corrected from the terminal.
- **`ankra cluster power-schedules`** manages scheduled stop and start for a
  cluster, so non-production estate can be parked outside working hours.
- **`--allow-repoint` on cluster apply** permits changing a cluster's GitOps
  source deliberately, instead of the change being refused or applied by
  accident. Repointing prunes what the new source does not define, so it
  stays opt-in.
- **`ankra ai openrouter set-key` / `remove-key`** manage the OpenRouter API
  key without going through the browser.

### Fixed

- **`ankra cluster apply` no longer implies it waited for the deploys.** It
  reported success once the definition was accepted, which read as "the
  workloads are up" when they had not started rolling out yet.
- **An add-on's configuration block survives an upgrade.** Upgrading a chart
  version dropped the configuration attached to the add-on.

## v0.10.0-rc2 — 2026-08-11

Second release candidate for v0.10.0. Two new command families: GitOps
source visibility (`cluster gitops status`) and chart introspection
(`charts template` and `charts values`), rendering a chart's manifests and
default values server-side from the same package Ankra deploys.

### Added

- **`ankra cluster gitops status` shows which GitOps repository a cluster
  actually syncs from.** Previously this was only visible in the browser: the
  new command prints the repository (owner/name and web URL), branch, stored
  Git credential, and provider, plus the last synced commit and time, the
  sync status and phase, and any pending commit or sync error. Supports
  `-o json|yaml` for scripting.
- **`ankra charts template` renders a chart's manifests without deploying
  anything.** The `helm template` equivalent for the Ankra catalog: the
  chart version is rendered server-side (no cluster connection) and printed
  to stdout as a `---`-separated multi-doc YAML stream with a `# Source:`
  header per document, ready to pipe into `kubectl diff` or kubeconform.
  `-f values.yaml` overrides the chart's defaults, `--release-name` and
  `--namespace` control the render context, and values problems (bad YAML,
  schema violations, template errors) fail with the exact Helm error a
  deploy would have produced — so broken values are caught before they
  reach a cluster. Chart NOTES.txt output goes to stderr, keeping stdout
  parseable.
- **`ankra charts values` prints a chart version's default values.** The
  `helm show values` equivalent: the decoded YAML lands on stdout, ready to
  redirect to a file, edit, and feed back to `ankra charts template -f`
  (`-o raw` prints the base64-encoded form). For both new commands the
  repository is resolved automatically when the chart name is unambiguous;
  `--repository` pins it.

## v0.10.0-rc1 — 2026-08-11

First release candidate for v0.10.0. Multi-key and whole-Secret encryption
for `cluster encrypt`, complete paginated listings (`--all`, no more silent
truncation), a dedicated StorageClass listing, and structured-output
consistency fixes.

### Added

- **`ankra cluster encrypt` can encrypt several keys in one run.** `--key`
  is now repeatable on both `encrypt manifest` and `encrypt addon`, in file
  mode and cluster mode alike, so encrypting a Secret with a dozen entries
  no longer takes a dozen invocations: all keys go through a single SOPS
  pass and a single write (one commit in cluster mode), every key is
  verified to be real ENC[...] ciphertext, and each one is recorded in
  `encrypted_paths`. `encrypt manifest` additionally gains `--all-data`,
  which selects every key under a Secret's `data` and `stringData`
  automatically - values that are already encrypted are skipped with a
  notice, and pointing it at anything other than a Secret fails naming the
  actual kind.
- **`ankra helm registries list --all` fetches every page.** The listing is
  paginated server-side (20 per page), and the CLI only ever showed the
  requested page — an organisation with 121 registries saw 20 rows with no
  sign that more existed. `--all` walks every page client-side and prints
  the complete set (mutually exclusive with `--page`).
- **`ankra helm registries get` can page through a registry's charts.** The
  chart listing attached to a registry only ever returned the first page;
  the new `--page`/`--page-size` flags select which chart page the response
  (and the `Charts: N (showing M on this page)` footer) reflects.
- **`ankra cluster get storageclasses` lists StorageClasses directly.**
  StorageClass was previously only reachable through `cluster get resources
  StorageClass --group storage.k8s.io`, and nothing told you the `--group`
  value it needed.

### Fixed

- **`ankra cluster manifests list` no longer silently truncates.** The
  client decoded the response's pagination envelope but never sent paging
  parameters, so clusters with more manifests than the backend's first page
  quietly lost the rest. The listing now walks every page.
- **`ankra cluster get <kind> -o json` stays parseable when nothing is
  found.** An empty listing printed `No <kind> found.` to stdout even in
  JSON/YAML mode, breaking `jq` pipelines; empty results now render as the
  structured envelope, and the human message is table-mode only.
- **`ankra cluster get pods -o json` emits one consistent shape.** Clusters
  that fit in one page got the full response envelope while clusters over
  100 pods got a bare pod array, so scripts saw a different document
  depending on cluster size. JSON output is now always the envelope, with
  every page's pods merged and the pagination describing the merged result.
- **`ankra cluster get resources <Kind>` now explains `--group`.** When a
  lookup finds nothing, the kind is outside the core API group, and
  `--group` was left unset, the empty message adds a hint naming the flag
  (e.g. `--group storage.k8s.io` for StorageClass), instead of an
  indistinguishable `No StorageClass found.`
- **Structured chart and registry listings carry complete metadata.**
  `ankra charts list -o json` pagination now includes `total_count`, and
  `ankra helm registries list -o json` rows now include the `kind` field
  the table always showed.

## v0.9.1 — 2026-08-10

A patch release. `ankra helm registries create` gains YAML spec support and
flag-based creation with a client-side URL scheme guard (closing the path to
the API's bare 500 on malformed specs), the never-functional MFA and
organisation RBAC command families are removed in favour of a browser
hand-off, and the AI model help examples name the current Expert model.

### Added

- **`ankra helm registries create` accepts YAML spec files and pure flag
  invocations.** `-f` now sniffs the file content, so the same command
  takes a JSON or a YAML spec, and a registry can be created without a
  file at all: `--name` plus `--url` (with optional `--credential-name`
  and repeatable `--exclude-charts`) build the spec for you. Exactly one
  of `-f` or `--name`/`--url` must be given. Flat specs with a URL scheme
  other than `oci://`, `http://`, or `https://` are rejected client-side
  as a usage error instead of being posted as an HTTP registry (the API
  answers such specs with an unhelpful 500), and a successful create now
  prints a reminder that `ankra helm registries sync <name>` triggers
  indexing immediately.

### Added

- **`ankra ai openrouter set|remove` makes OpenRouter bring-your-own-key
  scriptable.** `set` stores the organisation's OpenRouter API key — pass it
  with `--api-key`, pipe it on stdin, or omit both and a masked interactive
  prompt asks for it; the key itself is never echoed. `remove` deletes the
  stored key (confirming first, `--yes` to skip) and, when OpenRouter was
  the active provider, the organisation falls back to the Ankra-managed
  default. `ankra ai provider openrouter` activates a stored key and
  `ankra ai status` now shows the OpenRouter block alongside Anthropic and
  the legacy OpenAI-compatible endpoint.

### Removed

- **The MFA management and organisation RBAC commands are gone — none of
  them ever worked.** Every `ankra profile auth` API command (`status`,
  `totp start/confirm/remove`, `recovery-codes regenerate`, `passkeys
  list/remove`) and the whole `ankra org cluster-groups` /
  `ankra org assign` / `ankra org assignments` / `ankra org unassign` /
  `ankra org roles create` family called `/api/v1` routes the backend only
  serves to browser sessions, so every invocation since their introduction
  in v0.6.0 has failed with an API error. Two-factor settings and passkeys
  are browser flows by design (passkey enrollment needs a WebAuthn ceremony
  a terminal cannot run), so the CLI now ships a single `ankra profile auth
  open` command that opens Profile Authentication in the browser.
  `ankra profile auth passkeys open` still works as a deprecated alias and
  will be removed in v0.10.0. `ankra org roles` (listing the assignable
  roles) and all other `ankra org` commands are unaffected.

### Fixed

- **`ankra ai models create|update` help now names the current Expert
  model.** The `--model-id` examples still said `claude-opus-4-8` after the
  platform's Expert tier moved to Claude Opus 5, so the help text suggested
  a superseded model id. Because the hosted CLI reference is regenerated
  from this help text on every release, the stale example also kept
  reverting the corrected model id in `reference/cli/ai.mdx`.

## v0.9.0 — 2026-08-07

The stable v0.9.0 release promotes the v0.9.0 release candidates out of
prerelease. It adds two self-managed cluster families (Proxmox VE and HPE
Morpheus), brings the managed Kubernetes family to parity, gives every
provisioned provider per-node restart and bastion resize, introduces the
`agents` and `application` command families, lets agent-mode chat writes be
confirmed from the terminal, and fixes a long list of commands that were
decoding response shapes the API never sent.

### Added

- **Proxmox VE clusters are managed from the CLI.** The new `ankra cluster
  proxmox` family covers create, deprovision, stop/start, worker and
  node-group scaling, labels and taints, autoscaling, control-plane changes,
  node inspection and restart, SSH keys, Kubernetes upgrades, and discovery of Proxmox
  nodes, storages, bridges, and templates, plus `ankra credentials proxmox`
  for credential management.

- **HPE Morpheus clusters are managed from the CLI.** The new `ankra
  cluster morpheus` family mirrors the Proxmox surface (node restart excepted — the
  platform has no Morpheus restart lane) — full lifecycle,
  node groups, control plane, SSH keys, and upgrades — plus discovery of
  Morpheus groups, clouds, plans, layouts, and networks, and `ankra
  credentials morpheus` for credential management.

- **The managed Kubernetes family reaches parity.** `ankra cluster managed
  stop|start` drives provider-native stop/start where the provider supports
  it (AKS today), the new `ankra cluster managed node-pool update` command
  changes node counts and autoscaling settings in place, node pools take
  autoscaling bounds at create and add (`--autoscaling`,
  `--autoscaling-min`, `--autoscaling-max`), and Scaleway Kapsule joins the
  provider list (`--provider kapsule`, with `--private-network-id`).

- **Hetzner clusters can be stopped and started.** `ankra cluster hetzner
  stop <cluster_id>` releases the cluster's compute while preserving its
  saved topology, and `ankra cluster hetzner start <cluster_id>` re-provisions
  it (optionally `--scope control_plane`), matching the other self-managed
  providers.

- **Scaleway clusters now support lifecycle commands.** Use
  `ankra cluster scaleway stop <cluster_id>` to release compute while
  preserving the cluster definition, then `ankra cluster scaleway start
  <cluster_id>` to re-provision it (optionally `--scope control_plane`).

- **Scaleway clusters now have the same `nodes` commands as every other
  Ankra-provisioned provider.** `ankra cluster scaleway nodes list`, `nodes
  get`, and `nodes restart` were the only provider node surface missing from
  the CLI, even though the platform has served the Scaleway node routes and
  the `scaleway_restart_server` restart lane all along — so a Scaleway node
  could be restarted from the portal or the AI chat, but not from the
  terminal. `nodes restart` schedules a native reboot (falling back to a
  power cycle) as a tracked operation for any node the cluster reports,
  including the bastion/gateway. HPE Morpheus still has no `nodes restart`,
  matching the platform, which has no Morpheus restart lane.

- **`ankra cluster <provider> nodes restart` restarts a single node.** For
  Hetzner, OVH, UpCloud, and DigitalOcean clusters you can now restart any
  provisioned node - a control plane node, a worker, or the bastion/gateway -
  as a tracked operation. The platform schedules a native reboot (falling
  back to a power cycle); the node must be in the `up` state with no restart
  already in flight. Find the node ID with `nodes list`.

- **`ankra cluster <provider> bastion resize` changes the bastion instance
  type.** A new `bastion` command family (Hetzner, OVH, UpCloud,
  DigitalOcean) resizes the cluster's bastion/gateway node, following the
  same async accept/wait contract as node-group instance-type upgrades:
  submit-and-return by default, or block with `--wait`.

- **`ankra cluster <provider> nodes list` now shows provider status.** The
  node table gained a `PROVIDER_STATUS` column carrying the cloud provider's
  live status/power state (for example OVH `ACTIVE`/`SHUTOFF`) as last
  recorded by the provider read job, so a crashed or externally-stopped VM is
  visible before you act on it. Structured output (`-o json|yaml`) carries
  `provider_status` and `provider_power_state`.

- **See and stop what your AI agents are doing with `ankra agents`.** The
  new command family lists the organisation's dispatched AI agent runs
  (`ankra agents runs`, filterable by agent and status), shows one run in
  full (`ankra agents run <run_id>`), reads the run's session transcript —
  what the agent actually said and did — (`ankra agents transcript
  <run_id>`), and cancels a live run (`ankra agents cancel <run_id>`,
  organisation admins only): the platform interrupts the in-flight turn
  within seconds without pausing the agent itself. All four support
  `-o json|yaml` for scripting.

- **Agent-mode chat writes can now be approved from the terminal.** In agent
  mode every mutating tool halts the turn and emits an `action_proposal`
  frame, and the write only runs once that proposal is confirmed. The CLI
  parsed the chat stream but silently dropped that frame and had no way to
  answer it, so `ankra chat --mode agent "restart node worker-1"` appeared to
  do nothing — the proposal existed server-side but was invisible and
  unreachable. The stream now renders every proposal (tool, description, risk,
  whether it is reversible, parameters, expiry, and the action id), an
  interactive session prompts to run or discard each one, and
  `ankra chat actions confirm|reject|list` drives the same decision from a
  script. A confirmation refused because the cluster drifted since the
  proposal reports what happened and prints the ready-to-run `--force`
  invocation; a superseded action does not offer force, because forcing one
  cannot work.

- **Application management is available from the CLI.** `ankra application
  add .` detects a local GitHub checkout and starts application setup, while
  the application subcommands expose lifecycle, deployment, workflow,
  repository, security, publishing, and demo operations through the bearer
  API. `-o json|yaml` provides scriptable output.

- **A kube-gateway access denial now suggests the command that fixes it.**
  When `ankra cluster kubeconfig add` or `ankra cluster kube-token` is
  rejected with a 403 because the caller has no access grant on the cluster,
  the error now explains that an organisation admin can grant access and
  shows the exact invocation (`ankra cluster access grant <email>
  --cluster <name> --role view`) instead of leaving only the raw 403
  response to decipher.

- **Stack manifests and addons can carry an AGENTS.md.** Every manifest and
  addon entry in a stack spec now accepts `agents_md` (inline markdown) or
  `agents_md_from_file` (a repo-relative path, mirroring how the stack-level
  `description_from_file` works), so operational learnings live next to the
  resource they describe. The platform stores the content as a sibling file
  in the GitOps repo (`add-ons/<addon>/AGENTS.md`,
  `manifests/<name>.AGENTS.md`); omitting the field preserves what is
  stored, an explicit empty string clears it, and editing it never triggers
  a redeploy. `ankra cluster clone` transfers the referenced AGENTS.md files
  alongside the other stack files, and `ankra cluster addons upgrade` /
  `manifests upgrade` keep them intact.

- **`ankra cluster info` now shows the cluster's provider network
  identifiers.** For Ankra-provisioned clusters (DigitalOcean first) the
  details include a Network section with the VPC UUID, IP range, NAT
  gateway id, egress IP, and bastion droplet id/IPs — machine-readable via
  `-o json` — so operators no longer have to dig through per-node
  relationships to find the VPC a cluster lives in.

- **Secrets can be set and encrypted in a single commit.** `ankra cluster
  encrypt manifest` (cluster mode) now accepts repeatable `--set` edits that
  are applied in-memory before encryption, so the new value and its SOPS
  encryption land in one partial-stack PATCH — the plaintext value never
  reaches git history. Previously the documented flow (`manifests upgrade
  --set` followed by `encrypt manifest`) committed the plaintext secret
  first, leaving it recoverable from the repository history.

- **`ankra cluster manifests upgrade --from-file` accepts SOPS-encrypted
  files.** When the file carries SOPS metadata, the keys holding `ENC[...]`
  ciphertext are detected and recorded as `encrypted_paths` automatically
  (merged with the manifest's existing paths), and the new repeatable
  `--encrypted-path` flag declares keys explicitly. Previously such uploads
  dropped the encryption metadata and the backend rejected them with a
  generic 500.

- **`ankra helm registries list` supports pagination, search, and sorting.**
  The command used to fetch only the server's first page (20 registries) and
  gave no hint that more existed. It now accepts `--page` and `--page-size`
  (up to 100 per page), `--search` for a case-insensitive name filter, and
  `--sort-by` (`name`, `url`, `created_at`, `updated_at`, `chart_count`,
  `last_indexed_at`, `is_global`) with `--sort-order asc|desc`, and every
  listing ends with a `Page X of Y (total N)` footer so truncation is always
  visible.

- **Read-only API calls now retry transient platform errors.** Bodyless
  `GET`/`HEAD` requests that fail with a transport-level timeout (for
  example `http2: timeout awaiting response headers`), a connection
  setup/reset error, a mid-exchange disconnect, an HTTP/2 GOAWAY, or a
  502/503/504 gateway status are retried up to two more times with a
  short backoff (1s, then 2s), with a warning on stderr per retry. A
  seconds-long platform blip no longer hard-fails scripts and CI
  pipelines on their first read (2026-07-14: a brief platform stall
  failed a production rollout on `listing clusters`). Writes are never
  retried.

- **The lifecycle systemtest covers managed clusters.** `systemtest/
  lifecycle_systemtest.sh` now exercises both cluster families: the
  self-managed provider lifecycle and, per managed provider, the managed
  lifecycle (create, node-pool add/scale/update, stop/start where
  supported, upgrade, delete).

### Changed

- **`ankra cluster deprovision --force` now says when it is ignored.** Only
  the Hetzner deprovision endpoint honors `force`; for every other cluster
  type the CLI now prints a warning to stderr instead of implying a forced
  teardown the backend never performs. The generic deprovision request also
  no longer sends the `auto_delete`/`force` query parameters the backend
  discards.

- **The README now points at the hosted CLI reference instead of duplicating
  it.** The full command reference — every command, flag, and default — lives
  at [docs.ankra.ai/reference/cli](https://docs.ankra.ai/reference/cli),
  regenerated from the CLI source on every release, so it can never drift from
  the shipped binary the way the hand-maintained README reference could. The
  README keeps installation, quick-start, and development documentation, plus
  a per-command link table into the reference.

### Deprecated

- **`ankra cluster deprovision --auto-delete` is deprecated — it never did
  anything.** The backend parses and discards the `auto_delete` parameter, so
  the flag silently suggested a record deletion that never happened. The flag
  is now hidden and prints a deprecation warning pointing at
  `ankra delete cluster` for the record deletion; it will be removed in
  v0.10.0 (see `DEPRECATIONS.md`).

### Fixed

- **`ankra cluster encrypt -f` and `ankra cluster clone` no longer strip
  fields and comments from your cluster YAML.** Both commands used to rewrite
  the file by re-serialising an internal struct, which silently deleted
  anything the struct didn't model — `deploy_wave` on stacks and
  `spec.prometheus_metrics` were lost outright, and comments, anchors, and key
  ordering were destroyed — corrupting a file that is often the GitOps source
  of truth. The commands now edit the parsed YAML document in place, touching
  only the nodes they change: encrypting adds the key to `encrypted_paths` and
  nothing else, and cloning grafts the source's stack entries verbatim
  (comments and all) into the target file.

- **Seven commands that decoded the wrong response shape now show real
  data.** `cluster manifests list` always printed "No manifests found",
  `org members` always printed "No members found", `chat health` printed an
  empty status with a score of 0, and `cluster stacks history` rendered
  blank rows — in every case the CLI was reading JSON keys the API never
  sends. Each now decodes the actual response: manifests show their stack
  and creation time, members show role and invite status, health shows the
  scored report with issues and AI insights, and stack history lists every
  version of each stack member. Validation errors from manifest/addon
  upgrades and `encrypt` (which arrive nested per resource) are unpacked
  instead of printing empty brackets, and OVH/UpCloud/DigitalOcean
  deprovision report the created operation id instead of resource counts
  the API never returned.

- **`ankra cluster addons uninstall` can now actually uninstall.** The
  uninstall endpoint takes an addon resource UUID, but the addon listing
  carries no ids, so every uninstall attempt sent an empty id and failed
  with a 404. The CLI now resolves the resource id through the stack
  history before deleting. Addons that are not part of an Ankra-managed
  stack are rejected with a clear message instead of a server error, and
  `addons get`/`addons list` no longer show an always-empty ID column and
  repository (the API sends the registry URL under a different key).

- **`cluster stacks` and `cluster addons list` are no longer capped at 25
  entries.** The API pages both listings at 25 by default and the CLI never
  asked for more, silently hiding everything past the first page. Both now
  walk every page.

- **Every `ankra cluster managed` command now reaches the API instead of
  failing with a 404.** The client built managed-cluster URLs under
  `/api/v1/org/clusters/managed`, a path the backend never serves, so all
  nine subcommands — create, deprovision, stop, start, upgrade, and the four
  node-pool operations — failed with "not found" no matter what arguments
  they were given. The client now calls the backend's token-authenticated
  `/api/v1/clusters/managed` routes, which accept the CLI's bearer token and
  serve every one of those operations.

- **`ankra cluster kubeconfig add my-cluster` now works.** `kubeconfig add`
  and `kubeconfig remove` accept the cluster as a positional argument, the
  same way `ankra cluster select my-cluster` does. Previously the positional
  was silently ignored, so `ankra cluster kubeconfig add production --use`
  confusingly failed with "no cluster specified" even though the cluster was
  right there in the command. `--cluster` still works; giving both spellings
  with different values is rejected as a usage error (exit code 2), and a
  second stray positional argument now errors instead of being dropped.

- **`ankra helm registries create` validates the spec file before sending
  it.** The API requires the registry nested under exactly one of
  `helm_oci_registry` or `helm_http_registry` and answers any other shape
  with an unexplained server error (`No registry provided!`). The CLI now
  catches malformed files up front with an example of the expected shape
  (exit code 2), and accepts a flat `{"name": ..., "url": ...}` file by
  nesting it automatically — inferring an OCI registry from an `oci://`
  URL.

- **`ankra chat` now surfaces server errors instead of printing a blank
  `Error:` line and exiting 0.** The backend sends its message inside the
  error frame's `data` member (rate limits, spend caps, busy conversations);
  the CLI now reads it from there, prints it to stderr, and one-shot chat
  exits non-zero so scripts can detect the failure.

- **`ankra migrate` converts an existing Docker deployment into Ankra
  resources.** `ankra migrate convert` reads a docker-compose file, a bare
  Dockerfile, or the running Docker daemon and writes an ImportCluster
  manifest plus the Kubernetes manifests its stack refers to - compose
  `depends_on` becomes stack `parents`, credentials land in Secrets, named
  volumes become claims, healthchecks become readiness probes - with a
  warning for everything it could not carry over and the fix for each.
  Conversion is done by modules: `docker` is built in, and anyone can add a
  format by putting an executable named `ankra-module-<name>` on PATH that
  answers `describe`, `detect`, and `convert` over JSON (see
  `ankra migrate modules --help` and `examples/modules/`). None of it needs
  a login.


- **Chat progress is visible again.** Status frames became structured
  objects on newer backends and the CLI silently dropped them; it now
  renders the intent (and mechanism) as the familiar `[...]` progress line.

- **Ctrl-D leaves interactive chat cleanly** like `exit`, instead of failing
  with `reading input: EOF` and exit code 1.

- **A stalled chat stream no longer hangs the CLI forever.** A watchdog
  ends the stream with a clear `stream idle timeout` error when the
  connection goes silent for 3 minutes (the backend heartbeats every few
  seconds while working).

- **`ankra login` now runs the same insecure-HTTP guard as every other
  command.** The login flow sends the PKCE verifier and receives the minted
  token; a plaintext `http://` base URL to a non-loopback host is refused
  (loopback development and `ANKRA_ALLOW_INSECURE_HTTP=1` still work).

- **Error messages that echo API response bodies now redact likely secret
  material everywhere.** Several error paths (stack patch 422 echoes,
  variable requests, manifest and add-on configuration reads, support
  uploads) rendered raw response bodies to the terminal; they now pass
  through the same redaction the other error paths already used.

- **`ankra tokens create` now gives MCP-specific guidance for scoped tokens.**
  The previous examples named permission scopes the platform rejects.
  The help text now shows `mcp:read` and `mcp:write`, and successful scoped
  token creation prints the MCP endpoint instead of suggesting a REST
  `ANKRA_API_TOKEN` configuration that would be refused.

## v0.8.0 — 2026-07-14

The stable v0.8.0 release promotes v0.8.0-rc0: agent-token output and
agent-status accuracy fixes, plus drift field-path visibility in
`ankra cluster operations`.

### Added

- **`ankra cluster operations` now shows which fields drifted.** Single
  execution views (`operations list <id>`, `operations steps <id>`) fetch
  the step results and render each drifting resource with its drift type
  and the exact field paths the agent compared (for example
  `/spec/template/spec/hostNetwork`), instead of only step metadata and
  timings. Structured output (`-o json|yaml`) carries the same data as
  `drift_resources` on each step. Enrichment is best-effort: on platforms
  without the execution result endpoint the commands work as before and
  print a note to stderr.

### Fixed

- **`ankra cluster agent token` no longer prints an empty token.** The
  platform's token endpoints return the agent install command (and, on newer
  platforms, `token` and `cluster_id` fields), while the CLI decoded a
  `token`/`expires_at` shape that no longer existed and silently rendered an
  empty string. The CLI now decodes all returned fields, extracts the
  `ank_cai_…` token from the helm command when the platform only returns
  `command`, and prints the token together with the full install/update
  command. Structured output (`-o json|yaml`) now carries `token`,
  `cluster_id`, and `command`; the never-populated `expires_at` field is
  gone.
- **`ankra cluster agent status` no longer reports a stale agent as
  `connected`.** The status was derived from `checked_in_at` merely being
  present, so an agent that had been rejected or offline for hours (even
  days) still displayed `Status: connected`. The CLI now uses the platform's
  `is_online` verdict when present (30-second check-in threshold, the same
  one that flips clusters offline) and otherwise falls back to a two-minute
  check-in recency test; a stale check-in renders as
  `not connected (stale check-in)`.

## v0.7.0 — 2026-07-10

The stable v0.7.0 release consolidates the v0.7.0 release candidates: the
new ticket-relay browser login (required by the platform, which now answers
the old localhost-callback flow with 426 Upgrade Required), managed
Kubernetes support for all six providers, and Homebrew installation.

### Changed

- **`ankra login` no longer opens a local network port.** The old flow
  started a localhost callback server and had the browser redirect the OAuth
  code to it. The CLI now starts a platform login ticket, drives the whole
  flow in the browser (including sign-in approval and any MFA challenge),
  and polls `/api/v1/cli/login/poll` with the PKCE code verifier — which
  never leaves the machine — to collect the parked token. The platform has
  dropped support for the localhost-callback flow and refuses pre-v0.7.0
  CLIs with `426 Upgrade Required`, so this release is required to log in.

### Added

- **Homebrew installation.** `brew install ankraio/tap/ankra` installs the CLI
  from the new [ankraio/homebrew-tap](https://github.com/ankraio/homebrew-tap)
  vendor tap. The release workflow now renders the formula from
  `packaging/homebrew/ankra.rb.tmpl` and pushes version and checksum bumps to
  the tap on every stable tag; pre-release tags never reach brew. A
  Homebrew-managed binary refuses `ankra upgrade` (self-update) and defers to
  `brew upgrade ankra`, so brew stays the single owner of the file.
- **`ankra cluster managed` now supports all six managed Kubernetes
  providers.** The `--provider` flag previously accepted only `doks` and
  `uks`, even though the backend and portal already managed GKE, OVHcloud
  MKS, AKS, and EKS at the same endpoints. `create`, `delete`, `node-pool
  add|scale|delete`, and `upgrade` now accept `doks`, `uks`, `gke`,
  `ovh_mks`, `aks`, and `eks` (the `ovh-mks` and `mks` aliases normalise to
  `ovh_mks`; input is lower-cased and trimmed). Provider-specific
  control-plane options, node-pool autoscaling bounds, and cluster
  discovery/import remain portal/API only.

## v0.7.0-rc3 — 2026-07-10

### Added

- **`ankra cluster managed` now supports all six managed Kubernetes
  providers.** The `--provider` flag previously accepted only `doks` and
  `uks`, even though the backend and portal already managed GKE, OVHcloud
  MKS, AKS, and EKS at the same endpoints. `create`, `delete`, `node-pool
  add|scale|delete`, and `upgrade` now accept `doks`, `uks`, `gke`,
  `ovh_mks`, `aks`, and `eks` (the `ovh-mks` and `mks` aliases normalise to
  `ovh_mks`; input is lower-cased and trimmed). Provider-specific
  control-plane options, node-pool autoscaling bounds, and cluster
  discovery/import remain portal/API only.

## v0.6.0 — 2026-07-10

The stable v0.6.0 release consolidates v0.6.0-rc0 and adds organisation
cluster groups and scoped role assignments on top: agent rules and hooks that
make Ankra the default Kubernetes workflow, stack deploy waves, node-group
autoscaling, and the first slice of platform RBAC.

### Security

- **Builds now use Go 1.26.5**, picking up the fix for
  [GO-2026-5856](https://pkg.go.dev/vuln/GO-2026-5856) (`crypto/tls`:
  Encrypted Client Hello privacy leak), which govulncheck flagged as reachable
  from the CLI's TLS paths — the login callback server, streaming log/chat
  reads and writes, and every API round-trip. `golang.org/x/sys` is bumped to
  v0.44.0 for [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024)
  (Windows-only; not called by the CLI). `govulncheck ./...` is clean again.

### Added

- **Organisation cluster groups.** `ankra org cluster-groups
  list|create|add-cluster|set-selector|preview` manages named sets of
  clusters, either static (pinned members) or dynamic (a label selector
  evaluated against cluster labels), for use as role-assignment scopes.
  `preview` shows the clusters a group currently resolves to.
- **Scoped role assignments.** `ankra org assign <member-email>` grants a
  member a role at organisation, cluster, or cluster-group scope;
  `ankra org assignments <member-email>` lists what a member holds and
  `ankra org unassign <assignment-id>` revokes it. `ankra org roles create`
  defines custom roles that may bundle Kubernetes access levels provisioned
  across the assignment's scope (ADR 0007 extension).
- **`ankra cluster list` gains an Environment column.**
- **`ankra skills install` makes Ankra the agent's default for Kubernetes
  work.** Skills alone only load when the conversation happens to match their
  description, so install now also writes an always-applied rule telling
  Cursor/Claude Code that clusters here are Ankra-managed: route changes
  through the GitOps repo or `ankra cluster apply`, inspect freely, never
  mutate with raw kubectl/helm. Cursor gets a local plugin rule
  (`~/.cursor/plugins/local/ankra`) or a project `.cursor/rules/ankra.mdc`;
  Claude Code gets a marker-managed block in `CLAUDE.md` that reinstalls
  refresh and uninstalls remove without touching your own content. Skip with
  `--no-rules`; a full `ankra skills uninstall` cleans everything up.
- **`ankra skills install --with-hooks` enforces the workflow.** Installs an
  agent hook (Cursor `beforeShellExecution`, Claude Code `PreToolUse`) that
  runs the new `ankra skills guard` plumbing command on every shell command:
  cluster-mutating kubectl/helm invocations (`apply`, `delete`, `helm
  upgrade`, ...) pause for user confirmation with a redirect to the Ankra
  workflow, while read-only inspection, `--dry-run`, and everything else pass
  through untouched. The guard fails open and merges into existing
  hooks.json/settings.json without disturbing other entries.
- **Skill descriptions now trigger on plain Kubernetes intent.** The embedded
  skills previously activated only when the user said "Ankra"; their
  descriptions now match what users actually ask ("deploy an app", "install a
  Helm chart", "set up monitoring", "store secrets in Git"), so agents reach
  for the Ankra workflow without being told. The `ankra-platform-principles`
  skill doubles as the gateway: it applies to any Kubernetes task in an
  environment with the `ankra` CLI or an Ankra GitOps repo, with an explicit
  escape hatch for clusters that are not Ankra-managed.
- **Stack deploy waves.** Stacks in a cluster YAML accept an optional
  `deploy_wave` (integer >= 0): stacks in wave N deploy only after every
  stack in a lower wave finished, and teardown unwinds in reverse order.
  Stacks without a wave keep the current unordered behaviour. The wave is
  validated by `ankra cluster apply`, preserved by partial patches
  (`ankra cluster addons upgrade`, `ankra cluster manifests upgrade`), and
  shown as a new "Wave" column in `ankra cluster stacks list`.
- **`ankra cluster node-group autoscaling get|set`.** Read and write a node
  group's Cluster Autoscaler settings on Hetzner, OVH, and UpCloud clusters:
  `set --enabled --min <n> --max <n>` keeps the group's node count within
  [min, max] based on pod demand (first enable installs the autoscaler),
  `--enabled=false` turns it off. Both honour `-o json|yaml`, and `set`
  supports the standard `--wait`/`--timeout` async-write flags.
- **Wider organisation roles ahead of platform RBAC.** `ankra org invite
  --role` now accepts `owner`, `admin`, `operator`, `member`, `viewer`, and
  `read-only`, validated client-side with a clear usage error; the new
  `ankra org roles` lists them with descriptions. Until the RBAC assignments
  API ships, `owner`/`operator` alias onto `admin`/`member` on the wire.
- **`ankra tokens create --scopes`.** Optionally pin an API token to a
  permission allowlist (e.g. `--scopes clusters.read,stacks.deploy`);
  omitting it keeps today's behaviour of the user's full authority.
- **Exit code 7 for RBAC permission denials.** When the platform denies a
  request because the caller's role lacks a permission (403
  `permission_denied`), the CLI now names the missing permission, points at
  an organisation admin, and exits 7 — distinct from exit 6, which means
  re-authenticate. Reads, async writes, and stack patches all classify;
  other 403s keep their existing handling.

## v0.5.1 — 2026-07-07

### Added

- **`ankra cluster digitalocean`** — create, deprovision, stop/start, scale, upgrade, node groups,
  regions/sizes discovery, and credential management (alias: `ankra cluster do`,
  `ankra credentials digitalocean`).
- **`ankra cluster managed`** — create, deprovision, upgrade, and node-pool operations for
  DigitalOcean Kubernetes (`doks`) and UpCloud Managed Kubernetes (`uks`).
- Provider-agnostic cluster commands (`scale`, `upgrade`, `node-group`, `ssh-keys`, `deprovision`)
  now detect `digitalocean` clusters automatically.
- `systemtest/lifecycle_systemtest.sh` now exercises Kubernetes distribution as
  an independent axis (`ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm"`), running
  one cluster per provider/distribution pair, and generates a unique `/16` per
  DigitalOcean cluster to avoid VPC range collisions across parallel workers.

### Fixed

- **`ankra cluster addons upgrade` / `manifests upgrade` / `encrypt ... --cluster` /
  `stacks variables set|delete` timing out on large clusters.** These commands
  end in a partial-stack PATCH that the backend serves synchronously (DB
  transaction plus a full GitOps commit/push when the cluster has a linked
  repo), which can legitimately take longer than the previous 60-second
  command context. The context deadline is now 5 minutes, matching the HTTP
  client's existing slow-write timeout.

## v0.6.0-rc0 — 2026-07-07

### Added

- **`ankra skills install` makes Ankra the agent's default for Kubernetes
  work.** Skills alone only load when the conversation happens to match their
  description, so install now also writes an always-applied rule telling
  Cursor/Claude Code that clusters here are Ankra-managed: route changes
  through the GitOps repo or `ankra cluster apply`, inspect freely, never
  mutate with raw kubectl/helm. Cursor gets a local plugin rule
  (`~/.cursor/plugins/local/ankra`) or a project `.cursor/rules/ankra.mdc`;
  Claude Code gets a marker-managed block in `CLAUDE.md` that reinstalls
  refresh and uninstalls remove without touching your own content. Skip with
  `--no-rules`; a full `ankra skills uninstall` cleans everything up.
- **`ankra skills install --with-hooks` enforces the workflow.** Installs an
  agent hook (Cursor `beforeShellExecution`, Claude Code `PreToolUse`) that
  runs the new `ankra skills guard` plumbing command on every shell command:
  cluster-mutating kubectl/helm invocations (`apply`, `delete`, `helm
  upgrade`, ...) pause for user confirmation with a redirect to the Ankra
  workflow, while read-only inspection, `--dry-run`, and everything else pass
  through untouched. The guard fails open and merges into existing
  hooks.json/settings.json without disturbing other entries.
- **Skill descriptions now trigger on plain Kubernetes intent.** The embedded
  skills previously activated only when the user said "Ankra"; their
  descriptions now match what users actually ask ("deploy an app", "install a
  Helm chart", "set up monitoring", "store secrets in Git"), so agents reach
  for the Ankra workflow without being told. The `ankra-platform-principles`
  skill doubles as the gateway: it applies to any Kubernetes task in an
  environment with the `ankra` CLI or an Ankra GitOps repo, with an explicit
  escape hatch for clusters that are not Ankra-managed.
- **Stack deploy waves.** Stacks in a cluster YAML accept an optional
  `deploy_wave` (integer >= 0): stacks in wave N deploy only after every
  stack in a lower wave finished, and teardown unwinds in reverse order.
  Stacks without a wave keep the current unordered behaviour. The wave is
  validated by `ankra cluster apply`, preserved by partial patches
  (`ankra cluster addons upgrade`, `ankra cluster manifests upgrade`), and
  shown as a new "Wave" column in `ankra cluster stacks list`.
- **`ankra cluster node-group autoscaling get|set`.** Read and write a node
  group's Cluster Autoscaler settings on Hetzner, OVH, and UpCloud clusters:
  `set --enabled --min <n> --max <n>` keeps the group's node count within
  [min, max] based on pod demand (first enable installs the autoscaler),
  `--enabled=false` turns it off. Both honour `-o json|yaml`, and `set`
  supports the standard `--wait`/`--timeout` async-write flags.
- **Wider organisation roles ahead of platform RBAC.** `ankra org invite
  --role` now accepts `owner`, `admin`, `operator`, `member`, `viewer`, and
  `read-only`, validated client-side with a clear usage error; the new
  `ankra org roles` lists them with descriptions. Until the RBAC assignments
  API ships, `owner`/`operator` alias onto `admin`/`member` on the wire.
- **`ankra tokens create --scopes`.** Optionally pin an API token to a
  permission allowlist (e.g. `--scopes clusters.read,stacks.deploy`);
  omitting it keeps today's behaviour of the user's full authority.
- **Exit code 7 for RBAC permission denials.** When the platform denies a
  request because the caller's role lacks a permission (403
  `permission_denied`), the CLI now names the missing permission, points at
  an organisation admin, and exits 7 — distinct from exit 6, which means
  re-authenticate. Reads, async writes, and stack patches all classify;
  other 403s keep their existing handling.

## v0.5.0 — 2026-07-05

### Added

- **kubeadm cluster support in `ankra cluster upgrade`.** The provider-agnostic
  upgrade now covers kubeadm-distribution clusters alongside k3s. Nodes
  upgrade one at a time (control plane first): each node is cordoned, drained
  respecting PodDisruptionBudgets, upgraded, and gated on being Ready at the
  target version, with an etcd snapshot taken before the control plane. A new
  `--force` flag proceeds when a drain is blocked by a PodDisruptionBudget
  (the default aborts safely), and the upgrade now prints the operation ID
  with a hint to follow progress via `ankra cluster operations list`.
- **`ankra cluster kubeadm-versions`** lists the upstream Kubernetes versions
  the platform can provision or upgrade kubeadm clusters to, as a sibling of
  `ankra cluster k3s-versions`.
- **etcd topology flags for kubeadm creates.** `ankra cluster
  hetzner|ovh|upcloud create` gain `--etcd-topology` (`stacked` on the control
  planes, or `external` on dedicated VMs), `--etcd-node-count` (3 or 5), and
  `--etcd-server-type` for sizing the dedicated etcd nodes.
- **`ankra stack-profiles list --category`** filters profiles server-side by
  category (e.g. `monitoring`).
- **Generated CLI reference.** `tools/gendocs` renders the full command tree
  as Mintlify MDX pages, and a release-tag workflow opens a sync PR against
  the public docs so the reference never drifts from the shipped CLI.

### Fixed

- **Kubeconfig exec entries pin the cluster's owning organisation.** Entries
  written by `ankra cluster kubeconfig add` now embed `--org
  <organisation-id>` in the `kube-token` exec args, so `kubectl` keeps
  working after you switch your selected organisation — previously the token
  mint failed with "Cluster not found" whenever the selection differed from
  the cluster's owner. Cluster IDs are resolved to their owning organisation
  (and real cluster name) via the backend; entries written before this
  release need a one-time re-add to pick up the pin.
- **`ankra stack-profiles export-iac` exports the current version by
  default.** The `--version` default was a hard-coded `1`, silently exporting
  a stale first version once a profile advanced; it now resolves the
  profile's current published version and errors clearly when a profile has
  no published versions.
- **Deprecated `ankra cluster <provider> upgrade` help no longer overstates
  parity.** These forms always run the safe non-forced rollout; the help now
  says so and points at `ankra cluster upgrade` for `--force` and operation
  tracking.

## v0.4.2 — 2026-07-03

### Fixed

- **`ankra login` now declares two-factor capability to the platform.** The
  token exchange sends `supports_mfa: true`, letting the platform tell CLIs
  that can complete the Ankra-native two-factor step-up apart from legacy
  releases (v0.3.0 and older) that silently saved an empty token and reported
  "Login successful!". Once the platform enforces the check, outdated CLIs
  receive an explicit upgrade error instead of a broken login.

### Changed

- **Differentiated exit codes** - the CLI now exits with a stable, documented
  code instead of always `1`: `0` success, `1` API/runtime error, `2`
  usage/flag error (unknown command/flag, bad arguments, missing required
  flags), `3` targeted resource not found, `4` confirmation declined, `5`
  `--wait`/`--timeout` expiry on asynchronous writes (internal request
  deadlines still exit `1`), `6` authentication failure (missing/expired/
  rejected credentials, 401/403). Scripts can now branch on the failure class
  (re-authenticate on 6, treat 3 as idempotent success) without parsing error
  text.
- **Declined confirmations exit `4` everywhere** - including `helm registries
  delete` and `helm credentials delete`, which previously printed
  "Cancelled." to stdout and exited `0`, indistinguishable from a successful
  delete.
- **Errors always reach stderr and set a non-zero exit code.** Every command
  handler was converted from cobra's `Run` to `RunE`. This fixes a class of
  bugs where failures printed an error to *stdout* and exited `0`, invisible
  to scripts and CI: all of `charts` and `chat`, `cluster manifests list`,
  `cluster select`/`clear`, credential list commands, and others. Error text
  no longer pollutes stdout for `-o json|yaml` consumers; it is printed by
  cobra to stderr as `Error: ...`.
- **`ankra delete cluster`** - declining the confirmation prompt now exits `4`
  (previously printed "Aborted." and exited `0`); deleting a cluster that does
  not exist now exits `3` (previously exited `0`); the refusal hint for cloud
  clusters now points at `ankra cluster deprovision` instead of the
  provider-namespaced form deprecated in v0.4.0; and the underlying API error
  is included in the failure message instead of being swallowed.

### Added

- **Deprecation forwarding machinery** (internal) - `deprecateAndForward`
  registers a hidden forwarder at an old command path that re-dispatches to
  the replacement with argument rewriting, emitting cobra's human-facing
  notice plus a machine-readable `ANKRA_DEPRECATED=<old>=><new>
  removal=<version>` stderr marker for scripts and agents. No forwarders are
  wired yet; this lands the mechanism for upcoming command-tree work.

## v0.4.1 — 2026-07-03

### Fixed

- **`ankra login` no longer reports "Login successful!" while saving an empty
  token.** When the platform withholds the API token — for example when the
  account requires a two-factor step-up that the running CLI version does not
  understand — older CLIs silently wrote an empty `token:` to `~/.ankra.yaml`
  and declared success, leaving every subsequent command failing with
  "not logged in". The login flow now refuses to persist credentials without a
  token and explains what happened: an incomplete two-factor step-up says to
  run `ankra login` again, and a token-less exchange points at
  `ankra upgrade`. Existing saved credentials are left untouched either way.

## v0.4.0 - 2026-06-30

The stable v0.4.0 release consolidates the v0.4.0 release candidates into a
larger CLI control-plane update: provider-agnostic cloud cluster management,
cluster access administration, stack-profile apply/get, global per-command
cluster targeting, self-service MFA tooling, and more resilient login and
GitOps write paths.

### Added

- **`ankra profile auth ...`** - manage your own two-factor authentication from
  the CLI. `status` shows enrolled authenticators, passkeys/security keys and
  remaining recovery codes; `totp start|confirm|remove` sets up or removes an
  authenticator app; `recovery-codes regenerate` creates a fresh one-time code
  set; and `passkeys list|remove|open` lists/removes passkeys or opens Profile
  Authentication in the browser for WebAuthn setup.
- **`ankra skills --editor claude-code`** - install the curated Ankra Agent
  Skills into Claude Code's `~/.claude/skills` directory, or
  `<project>/.claude/skills` when combined with `--project`.
- **`ankra cluster access list | grant | revoke`** - manage per-user access to
  a cluster's Kubernetes API through the Ankra kube gateway, including
  namespace-scoped grants and RBAC reconcile status.
- **Provider-agnostic cloud cluster lifecycle commands** - `ankra cluster
  upgrade`, `scale`, `node-group`, `k3s-versions`, and `deprovision` now detect
  Hetzner, OVH, or UpCloud automatically, so users no longer need to pick a
  provider namespace for common lifecycle work.
- **Cloud create parity across Hetzner, OVH, and UpCloud** - cloud-provider and
  networking stacks can be installed directly by default and committed to GitOps
  when repository flags are supplied.
- **OVH operational commands** - stop/start clusters, print access info, manage
  SSH keys, set node-group labels/taints, and inspect control-plane or node
  details through the public API.
- **`ankra stack-profiles get` and `ankra stack-profiles apply`** - inspect
  published stack-profile versions and instantiate a profile as a draft or
  deploy it directly, with `--set`, `--set-file`, and `--set-env` parameter
  binding.
- **Organisation slug resolution** - organisation slugs are shown in org output,
  and `ankra org switch` plus global `--org` resolve by ID, slug, or name.
- **Global `--cluster <name|id>` for cluster-scoped commands** - target a
  cluster for a single command without changing the saved selection.
- **`ankra cluster ssh-keys get | set | resync <cluster_id>`** - manage SSH keys
  across Hetzner, OVH, and UpCloud from one command group, including provider
  reference repair with `resync`.

### Changed

- **`ankra login` now completes Ankra-native two-factor authentication.** Second
  factors are managed by Ankra (not Auth0): when your account has a passkey,
  security key, or authenticator app enrolled, the token exchange withholds the
  API token and returns a one-time challenge URL. The CLI opens that URL in your
  browser, you complete the second step (passkey, authenticator code, or recovery
  code), and the CLI polls until the step-up succeeds and the token is released.
  No flags change; accounts without a second factor log in exactly as before.
- **`ankra login` is more reliable on dual-stack IPv4/IPv6 machines.** The
  browser redirect now uses the same `127.0.0.1` loopback address the callback
  server listens on, and the callback wait matches the backend's 10-minute
  login-state expiry.
- **`--config <file>` now fully isolates per-invocation state.** Extensionless
  config files are parsed as YAML, and active-cluster selection is keyed to the
  explicit config path so parallel workers do not clobber each other.
- **`ankra support create` now shows the AI review before submitting.** Flagged
  requests and possible duplicates are shown before confirmation; `--force`
  still skips the prompt.

### Fixed

- **Partial-stack writes tolerate slow synchronous Git commits.** Commands that
  PATCH a stack (`manifests upgrade`, `addons update`, `cluster encrypt`, and
  `stack-variables set`) are bounded by an overall 5-minute deadline instead of
  the shared client's 30-second response-header timeout.
- **`ankra cluster encrypt` preserves leading-dot keys such as
  `.dockerconfigjson`.** Dotted-path normalisation no longer corrupts literal
  Kubernetes secret keys that begin with a dot.

### Deprecated

- The provider-specific `ankra cluster {hetzner,ovh,upcloud} upgrade`, `scale`,
  `node-group`, and `deprovision` commands are deprecated in favour of the
  provider-agnostic verbs above and are scheduled for removal in v0.5.0.
- `ankra cluster ovh ssh-keys get | set <cluster_id>` is deprecated in favour
  of `ankra cluster ssh-keys get | set <cluster_id>` and is scheduled for
  removal in v0.6.0.

## v0.4.0-rc4 - 2026-06-23

### Fixed

- **Partial-stack writes no longer fail with `http2: timeout awaiting response
  headers` when the server commits to git synchronously.** `ankra cluster
  manifests upgrade`, `ankra cluster addons update`, `ankra cluster encrypt`,
  and `ankra cluster stack-variables set` all issue
  `PATCH /stacks/{stack_name}`, which performs a synchronous git commit+push on
  the request path and can legitimately take longer than the shared HTTP
  client's 30s response-header timeout to start responding. These partial-stack
  writes now use a dedicated client that drops the response-header timeout and
  is bounded by an overall 5-minute deadline, so a slow-but-progressing server
  completes the write instead of erroring out while still making progress.

## v0.4.0-rc3 - 2026-06-23

### Added

- **Global `--cluster <name|id>` on every `ankra cluster ...` subcommand** -
  target a cluster per command without first running `ankra cluster select`.
  The flag is inherited by all cluster subcommands (`stacks`, `operations`,
  `addons`, `manifests`, `get`/`logs`/`resources`, `helm`, `agent`,
  `reconcile`, `provision`, `deprovision`, `roll-to`, `info`, ...) and takes
  precedence over the persisted selection; it also accepts either a cluster
  name or ID. `ankra chat health` and `ankra openclaw skill | handoff` gained
  the same `--cluster` override. Commands that already accepted a positional
  cluster name still do - an explicit argument wins over `--cluster`, which in
  turn wins over the saved selection.

- **`ankra cluster ssh-keys get | set | resync <cluster_id>`** - cloud-agnostic
  SSH key management that detects the provider (Hetzner, OVH, UpCloud)
  automatically from the cluster. `get` lists attached and available SSH key
  credentials, `set` replaces the attached set (use `--clear` to remove all user
  keys; the Ankra-managed key is always retained) and applies the change to
  running nodes, and `resync` repairs a stale provider-side SSH key reference
  (for example when the key was deleted and re-created in the provider console)
  that blocks new node creation, re-applying the authorised keys to running
  nodes.

### Fixed

- **`ankra login` now completes reliably on dual-stack (IPv4/IPv6) machines.**
  The browser callback server binds the IPv4 loopback (`127.0.0.1`) but the
  redirect URI advertised to the backend used `localhost`, which resolves to
  both `127.0.0.1` and `::1`. A browser that connected to the IPv6 address
  reached nothing, so after authenticating (including MFA) the final
  `http://localhost:<port>/callback` redirect failed and login never finished.
  The redirect URI now uses the `127.0.0.1` literal (RFC 8252 §8.3), matching
  the listener. The CLI also waits up to 10 minutes for the callback (was 5) to
  align with the backend's login-state expiry, so a slow MFA round-trip no
  longer tears the callback server down early.

### Deprecated

- **`ankra cluster ovh ssh-keys get | set <cluster_id>`** - replaced by the
  cloud-agnostic `ankra cluster ssh-keys get | set <cluster_id>`. The provider is
  detected automatically from the cluster.

## v0.4.0-rc2 - 2026-06-19

Builds on v0.4.0-rc1 (all of its provider-parity work is included) and adds
stack-profile inspection and one-step apply, plus organisation slug resolution.

### Added

- **`ankra stack-profiles get <profile-id>`** - show a stack profile's metadata,
  its published versions, and the parameters a version exposes. Pick a specific
  version with `--version` (defaults to the profile's current version) and use
  `-o json|yaml` for structured output.
- **`ankra stack-profiles apply <profile-id>`** - instantiate a stack profile
  onto a cluster. By default it creates a reviewable **draft** (nothing is
  deployed until you pass `--deploy` or deploy it from the dashboard); it targets
  the selected cluster unless `--cluster <name|id>` is given. Choose the profile
  `--version`, name the new stack with `--stack-name`, and bind parameters with
  `--set name=value` - or, for secrets, `--set-file name=path` / `--set-env
  name=ENV_VAR` so values never reach your shell history or process list.
- **Organisation slugs** - the organisation `slug` is now shown in
  `ankra org list`, `ankra org current`, and `ankra org create`, and both
  `ankra org switch <organisation>` and the global `--org` flag resolve a
  reference by ID, slug, or name (case-insensitive), with actionable errors on
  ambiguous or unknown references.

See the **v0.4.0-rc1** notes below for the cloud-agnostic `cluster
upgrade | scale | node-group` verbs, the cloud-provider/ingress parity across
OVH, UpCloud and Hetzner, and the deprecation of the provider-specific
`cluster {hetzner,ovh,upcloud}` commands.

## v0.4.0-rc1 - 2026-06-18

### Added

- **`ankra cluster access list | grant | revoke`** - manage who can reach a
  cluster's Kubernetes API through the Ankra kube gateway (the access used by
  `ankra cluster kubeconfig` and `ankra cluster kube-token`). A grant maps an
  organisation member (by email) to a Kubernetes role (`view`, `edit`, `admin`,
  `cluster-admin`), cluster-wide or limited to one namespace with
  `--namespace`. `list` shows each grant's RBAC reconcile status (pending,
  applied, failed, cluster offline); `revoke` accepts a grant ID or an email
  (revoking every grant that member has on the cluster). Managing access
  requires organisation admin rights.

- **`ankra cluster upgrade <cluster_id> <target_version>`**, **`ankra cluster
  scale <cluster_id> <worker_count>`**, and **`ankra cluster node-group
  <list|add|scale|upgrade|delete>`** - cloud-agnostic verbs that detect the
  provider (Hetzner, OVH, UpCloud) automatically from the cluster, so you no
  longer pick a provider namespace. They replace the provider-specific
  `ankra cluster {hetzner,ovh,upcloud} ...` forms (see Deprecated).
- **`ankra cluster k3s-versions`** - list the k3s (Kubernetes) versions
  available for `ankra cluster upgrade`, with the stable channel highlighted.
- **`ankra cluster deprovision <cluster_id>`** now accepts a cluster ID or a
  name (previously name-only) and routes cloud clusters to the provider-specific
  teardown so cloud resources are released.

- **`ankra cluster ovh create`** now accepts **`--external-cloud-provider`**
  (OpenStack CCM + Cinder CSI), **`--include-networking`** (Traefik +
  cert-manager), and **`--gitops-credential-name`** / **`--gitops-repository`** /
  **`--gitops-branch`**. The cloud provider and networking install by default
  (reconciled directly, no GitOps required) and are committed to Git when the
  GitOps flags are set. `--include-networking` requires `--external-cloud-provider`
  (the ingress LoadBalancer is provisioned by the cloud controller manager), so
  `--external-cloud-provider=false` also disables networking; pass
  `--include-networking=false` to keep the cloud provider without ingress.
- **`ankra cluster upcloud create`** now matches OVH: **`--external-cloud-provider`**
  (UpCloud CCM + CSI) and the new **`--include-networking`** flag (Traefik +
  cert-manager) both default to **on** and no longer require GitOps - the
  cloud-provider/networking stacks are reconciled directly, and are additionally
  committed to Git when **`--gitops-credential-name`** and **`--gitops-repository`**
  are set. `--include-networking` requires `--external-cloud-provider` (the ingress
  LoadBalancer is provisioned by the cloud controller manager), so
  `--external-cloud-provider=false` also disables networking; pass
  `--include-networking=false` to keep the cloud provider without ingress.
- **`ankra cluster hetzner create`** reaches the same parity: new
  **`--external-cloud-provider`** (Hetzner CCM + CSI), **`--include-networking`**
  (Traefik + cert-manager), and **`--gitops-credential-name`** /
  **`--gitops-repository`** / **`--gitops-branch`** flags. The cloud-provider and
  networking stacks now install by default without GitOps (reconciled directly),
  and are committed to Git when the GitOps flags are set. `--include-networking`
  requires `--external-cloud-provider`, so `--external-cloud-provider=false` also
  disables networking; pass `--include-networking=false` to keep the cloud provider
  without ingress.
- **`ankra cluster ovh stop <cluster_id>`** and **`ankra cluster ovh start
  <cluster_id> [--scope all|control_plane]`** - stop an OVH cluster's compute
  while keeping its configuration, then start it again later (optionally bringing
  up only the control plane first).
- **`ankra cluster ovh access-info <cluster_id>`** - print the gateway (bastion)
  and control plane IPs along with ready-to-use `ssh -J` jump and Kubernetes API
  port-forward commands.
- **`ankra cluster ovh ssh-keys get <cluster_id>`** and **`ankra cluster ovh
  ssh-keys set <cluster_id> --ssh-key-credential-ids <id>,...`** - view and
  replace the SSH key credentials attached to an OVH cluster (changes apply on
  the next reconciliation).
- **`ankra cluster ovh node-group add`** now accepts **`--labels k=v,...`** and
  **`--taints k=v:Effect,...`** so a new node group can be created with its
  Kubernetes labels and taints in one step.
- **`ankra cluster ovh control-plane ...`** and **`ankra cluster ovh nodes
  ...`** now reach the public API: the control-plane and node-inspection
  endpoints are exposed on `/api/v1/clusters/ovh/...` (previously only
  available to the web UI), so these commands work against a token-authenticated
  CLI session.

### Changed

- **`--config <file>` now fully isolates per-invocation state.** A config file
  with an unfamiliar or missing extension (for example `--config /run/ankra/worker1`)
  is now parsed as YAML - the only format the CLI writes - instead of reading as
  empty and silently dropping the saved token and base URL. The active-cluster
  selection (`ankra cluster select`) is also keyed to the explicit `--config`
  path (stored alongside it as `<config>.selected.json`) rather than `$HOME`, so
  parallel runs against different config files no longer clobber each other's
  selection. **Migration:** if you previously ran with `--config` and relied on
  the `$HOME`-keyed selection, re-run `ankra cluster select` once to re-establish it.
- **`ankra support create` now shows the AI review before submitting.** Instead
  of a one-shot create that returned a terse "ticket flagged in review; retry
  with --force" on rejection, the command first calls the review endpoint and
  prints what it found: the specific reasons a request was flagged, clarifying
  questions that would speed up triage, and any existing ticket that may already
  track the same problem. When the review flags the request or finds a possible
  duplicate, you're asked to confirm interactively (`Submit this request anyway?
  [y/N]`); `--force` still skips the prompt and submits, and `-o json|yaml`
  callers get a `--force`-guidance error instead of a prompt. A clean request is
  submitted with no extra step.

### Deprecated

- The provider-specific **`ankra cluster {hetzner,ovh,upcloud} upgrade`**,
  **`scale`**, **`node-group <list|add|scale|upgrade|delete>`**, and
  **`deprovision`** commands are deprecated in favour of the cloud-agnostic
  `ankra cluster upgrade` / `scale` / `node-group` / `deprovision` verbs, which
  detect the provider automatically. The old commands still work and now print a
  runtime warning pointing at the replacement; they are scheduled for removal in
  v0.5.0. See `DEPRECATIONS.md`.

## v0.3.0 - 2026-06-11

First stable release of the v0.3.0 line. It consolidates everything shipped in
the **v0.3.0-rc0 → rc3** release candidates and adds direct kubeconfig, metrics,
support and stack-profile tooling on top, so you can drive an Ankra cluster
end-to-end from the terminal. Install it with the standard one-liner or
`ankra upgrade`; the beta channel is no longer required for the v0.3.0 features.

### Security

- **`ankra cluster encrypt manifest | addon` no longer produces files that only
  look encrypted.** SOPS' `encrypted_regex` matches YAML key names during tree
  traversal, not dotted paths, so `--key data.password` previously matched
  nothing: the file gained full `sops:` metadata (age recipient, mac) while the
  secret value stayed plaintext base64, and `encrypted_paths` was still updated.
  A dotted `--key` is now normalised to its last segment (`data.password` →
  `password`) with a notice, and after every encryption the CLI verifies the
  target key's value is real `ENC[...]` ciphertext - hard-failing before any
  file write or stack PATCH when SOPS encrypted nothing. The `--help` examples
  and the `ankra-sops-secrets` skill no longer steer users into the dotted-path
  form.

### Added

- **`ankra cluster kubeconfig add | remove | list`** and **`ankra cluster
  kube-token`** - wire `kubectl` straight to an Ankra cluster. `kube-token`
  prints a short-lived Kubernetes `ExecCredential` for use as a credential
  plugin, and `kubeconfig add` writes an `ankra-*` context (exec-based, or
  `--embed-token`) into your kubeconfig with atomic `0600` writes that preserve
  any foreign entries and use collision-safe context naming.
- **`ankra cluster metrics query | query-range`** - proxy a PromQL query (instant
  or range) to the cluster's Prometheus metrics source, with `table | json |
  yaml` output for ad-hoc inspection and CI.
- **`ankra support create | list | get | comment | attach | close`** - open and
  track Ankra support requests from the CLI, including image/screenshot
  attachments. Each request goes through a mandatory AI review; use `--force` to
  submit a request the reviewer flags.
- **`ankra stack-profiles list | export-iac | import`** - manage reusable,
  organisation-level stack profiles as `ClusterInfrastructureAsCode` YAML
  (export a profile version, import one from a file).
- **`ankra cluster draft`** - stage every stack in an `ImportCluster` as a
  reviewable draft instead of applying it; nothing is deployed by the command
  itself.
- **`ankra cluster validate`** - the offline `apply --dry-run` checks plus
  server-side chart-existence, plaintext-secret, and parent-reference
  validation; CI-friendly exit codes and `--strict-secrets`.
- **Self-update & beta channel** - `ankra upgrade` downloads, SHA-256-verifies
  and atomically swaps the binary, with `--version` pinning for upgrade,
  downgrade and rollback (`--allow-unverified` for releases that predate
  published checksums), and an `ankra config beta enable|disable|status`
  pre-release channel with semver-aware precedence (a stable release outranks
  its release candidates).
- **Offline dependency-tree and referenced-file validation** in
  `ankra cluster apply`, and **`--dry-run`** for `apply` / `delete cluster`
  (fully offline, no token, CI-friendly).
- **`--watch` and `-o json|yaml`** for `ankra cluster operations` list and
  steps.
- **Shared `-o json|yaml` output across commands**, and unexpected platform
  errors now print a hint to file the bug with `ankra support create`.
- **OVH command parity with the web UI**:
  - `ovh regions --credential-id <id>` - list the OVH Cloud regions a
    credential's project can actually deploy in.
  - `ovh stop <cluster_id>` and `ovh start <cluster_id>
    [--scope all|control_plane]` - stop a cluster's compute while keeping its
    configuration, then start it again later.
  - `ovh access-info <cluster_id>` - gateway (bastion) and control plane IPs
    with ready-to-use `ssh -J` jump and Kubernetes API port-forward commands.
  - `ovh ssh-keys get|set` - view and replace the SSH key credentials attached
    to a cluster (changes apply on the next reconciliation).
  - `ovh node-group add --labels k=v,... --taints k=v:Effect,...`, plus
    `node-group labels` / `node-group taints` to update existing groups (an
    empty value clears them; taint effect defaults to `NoSchedule`).
  - `ovh control-plane ...` and `ovh nodes ...` now reach the public API
    (`/api/v1/clusters/ovh/...`), so they work against a token-authenticated
    CLI session.

### Changed

- **`cluster apply` and the cloud `node-group` mutations (Hetzner, OVH,
  UpCloud)** submit async by default (`202 Accepted`); `--wait` blocks until
  the platform finishes and prints the full result (including the agent install
  command on first import), bounded by `--timeout` (default 10m).
- **`ankra cluster apply`** understands the `prometheus_metrics` spec field.

### Fixed

- **`ankra credentials get`** resolves a name to an ID (trying the v2
  platform-credential lookup before the legacy table).
- **`ankra org members` / `org current`** honour `--org` and validate the saved
  selection instead of sending a stale value.
- An unknown `--cluster` name fails clearly instead of forwarding a non-UUID
  value and producing an opaque server-side error.

### Details and examples

#### Stage changes as drafts with `ankra cluster draft`

`ankra cluster draft -f cluster.yaml` stages every stack in an ImportCluster YAML as a reviewable draft instead of applying it. The local checks run first (the same as `ankra cluster apply --dry-run`), then each stack is saved as a resource draft you can review, edit, and deploy from the Ankra stack builder - nothing is deployed by the command itself.

If the cluster does not exist yet it is imported first (live), since drafts can only be attached to an existing cluster. Stacks that already match the cluster's desired state are reported as `no changes` rather than creating an empty draft. The command exits non-zero if any stack fails validation.

```bash
ankra cluster draft -f cluster.yaml
```

#### Server-side validation with `ankra cluster validate`

`ankra cluster validate -f cluster.yaml` runs the same offline checks as `ankra cluster apply --dry-run` (structure, referenced-file YAML, parent/dependency tree) and then sends the spec to the Ankra API for the checks that need server-side data - checks the offline path cannot perform:

- **chart existence** in the Helm registries connected to your organisation,
- **plaintext secret detection** for Kubernetes `Secret` manifests and addon values that are not SOPS-encrypted,
- **parent references** resolved against an existing cluster's deployed resources (with `--cluster <id>`).

Nothing is applied. Warnings (e.g. plaintext secrets) are printed but do not fail the command; pass `--strict-secrets` to treat plaintext secrets as errors. The command exits non-zero when validation finds errors, so it drops straight into CI.

```bash
ankra cluster validate -f cluster.yaml
ankra cluster validate -f cluster.yaml --strict-secrets
ankra cluster validate -f cluster.yaml --cluster <cluster_id>
```

#### Self-update with `ankra upgrade`

`ankra upgrade` downloads and installs the latest Ankra CLI release, replacing
the running binary in place. It resolves the latest release tag from GitHub
(or installs a pinned `--version v0.2.5`), downloads the matching
`ankra-cli-<os>-<arch>` asset, verifies it against the published SHA-256
checksum, and atomically swaps the executable. The command needs no API token.

Pin an exact release with `--version` (with or without the leading `v`) to
upgrade *or* downgrade - a pinned version installs whether it is newer, older
or the same as the running binary, so it doubles as a rollback. Only an
unpinned `ankra upgrade` keeps the "already up to date" / "installed version is
newer" safety checks; pinning is treated as explicit intent and asks for a
single confirmation (`Upgrade` / `Downgrade` / `Reinstall`).

If a release does not publish a checksum, the upgrade fails closed rather than
installing an unverified binary; pass `--allow-unverified` to override that for
older releases that predate published checksums.

```bash
ankra upgrade                       # upgrade to the latest release
ankra upgrade --check               # report whether a newer release is available
ankra upgrade --version v0.2.5      # install an exact release (upgrade)
ankra upgrade --version 0.1.9 --yes # downgrade/roll back, no confirmation prompt
ankra upgrade --version v0.1.0 --allow-unverified  # release without a checksum
```

If the installed binary lives in a directory the current user cannot write
(for example `/usr/local/bin`), the command prints a clear message pointing to
`sudo ankra upgrade` or the install script.

#### Beta (pre-release) update channel

`ankra config beta enable` opts the CLI into pre-release versions. When the
beta channel is enabled, `ankra upgrade` resolves the newest release
*including* release candidates (for example `v0.3.0-rc.1`); when disabled (the
default) only stable `x.x.x` releases are installed. The preference is stored
in `~/.ankra/settings.json`, separately from credentials.

```bash
ankra config beta enable     # opt into pre-releases
ankra config beta status     # show the current channel
ankra config beta disable    # back to stable only (default)
ankra upgrade --beta         # one-off: include pre-releases for this run
```

Version comparison now follows semantic-versioning precedence, so a stable
release outranks its release candidates (`v0.3.0` > `v0.3.0-rc.2` > `v0.3.0-rc.1`).

#### Offline dependency-tree validation in `ankra cluster apply`

`ankra cluster apply` now validates the parent (`parents:`) graph of the
assembled `ImportCluster` document before it is sent to the API, in addition to
the existing structural and `from_file` checks. The validation enforces that
resource names are unique per kind across the whole document (parents resolve by
`kind`+`name` with no stack qualifier, so a duplicate is ambiguous), that every
parent reference uses a valid `kind` (`manifest` or `addon`), names a resource
declared somewhere in the document (cross-stack references allowed), and that
the resulting graph is acyclic. This catches dependency errors locally that the
backend would otherwise only reject at apply time (HTTP 422).

It runs for both real applies and `--dry-run`, so you can lint a `cluster.yaml`
end-to-end without a token or network:

```bash
ankra cluster apply -f cluster.yaml --dry-run
# Invalid ImportCluster in "cluster.yaml":
#   dependency cycle detected: addon "a" -> addon "b" -> addon "a"
```

#### Referenced-file YAML validation in `ankra cluster apply`

Every file reference in the document is now resolved and validated, regardless
of whether its content is ultimately used. Manifest content (`manifest` inline
or `from_file`, including multi-document files) and addon values
(`configuration.values` inline or `configuration.from_file`) are parsed to
confirm valid YAML; `stack.description_from_file` is resolved and read for
existence even when an inline `description` is also set (previously the file
reference was silently skipped in that case). Errors name the resolved file and
the problem:

```bash
ankra cluster apply -f cluster.yaml --dry-run
# Invalid ImportCluster in "cluster.yaml":
#   stack "logging": manifest "broken": the file referenced by 'from_file' ("/abs/path/broken.yaml") is not valid YAML: ...
```

#### `--dry-run` for `ankra cluster apply` and `ankra delete cluster`

`ankra cluster apply --dry-run` runs the structural, referenced-file, and
dependency-tree validation above and then exits without contacting the API.
`ankra delete cluster --dry-run` reports the cluster it would delete without
calling the API. Both dry-run modes are fully offline and no longer require a
token, so they can run in pre-merge CI without credentials. (Dry-run modes that
still query live cluster state, such as `cluster addons upgrade --dry-run`,
continue to require authentication.)

#### Watch and machine-readable output for `ankra cluster operations`

`ankra cluster operations list` gains `--watch`/`-w` to continuously poll and
refresh until every execution reaches a terminal state, with a configurable
`--interval` (default `5s`, floored at `1s`). Both `operations list` and
`operations steps` gain `-o json|yaml` for machine-readable output in CI.
`--watch` cannot be combined with `-o` (structured output is rendered once).

```bash
ankra cluster operations list --watch --interval 10s
ankra cluster operations steps <execution_id> -o json
```

## v0.2.4 - May 2026

### New Features

#### Variables CRUD at Organisation, Cluster, and Stack Scopes

`ankra org variables` and `ankra cluster variables` are new top-level command
groups for managing template variables that get substituted into stack
manifests and addon values at deploy time. Stack-scoped variables are managed
via `ankra cluster stacks variables`. All three scopes have the same UX:

```bash
# Organisation (available to every cluster)
ankra org variables list
ankra org variables set DB_HOST db.example.com --description "Primary DB"
ankra org variables get DB_HOST
ankra org variables delete DB_HOST

# Cluster (shadows org variables on that cluster)
ankra cluster variables list --cluster prod
ankra cluster variables set DB_HOST db.prod.example.com

# Stack (most specific; shadows cluster + org variables on that stack)
ankra cluster stacks variables list demo-web-app
ankra cluster stacks variables set demo-web-app FEATURE_FLAG enabled
```

`set` is an upsert: it creates the variable, or updates it if a variable with
the same name already exists. The value can also be read from stdin with `-`
(useful for piping secrets from a vault or `pass`). All `list`/`get` commands
support `-o json|yaml` for scripting. `delete` prompts for confirmation
(`--yes` to skip).

Org and cluster variables are exposed on new bearer-token endpoints
(`/api/v1/org/variables` and `/api/v1/org/clusters/imported/{id}/variables`)
that wrap the existing usecases; stack variables travel through the same
partial-stack PATCH used by `manifests upgrade` / `addons upgrade`.

#### Encrypt and Decrypt Live Cluster Resources with SOPS

`ankra cluster encrypt` and `ankra cluster decrypt` can now operate directly on
manifests and addons stored on a live cluster, without needing a local
`cluster.yaml`. They mirror the partial-stack PATCH flow used by
`manifests upgrade` / `addons upgrade`: fetch the current content, call the
SOPS API to encrypt/decrypt, and (for encrypt) push the result back with
`encrypted_paths` updated.

```bash
# Encrypt a key in a live manifest on the selected cluster
ankra cluster encrypt manifest db-secret --key data.password

# Encrypt a key in a live addon's values, with an explicit cluster + stack
ankra cluster encrypt addon --name grafana --key adminPassword \
  --cluster prod --stack monitoring

# Print decrypted content from a live cluster
ankra cluster decrypt manifest db-secret
ankra cluster decrypt addon --name grafana --cluster prod
```

The existing `-f <cluster.yaml>` file mode is unchanged and remains for GitOps
workflows where the source of truth lives on disk. The two modes are mutually
exclusive; cluster mode is the default when no `-f` is given. A new
`decrypt addon` subcommand brings the addon variant to parity with the manifest
variant.

#### Install Ankra Agent Skills

`ankra skills` installs the curated Ankra Agent Skills (for Cursor, Claude Code, and OpenClaw)
into a skills directory. The skills are embedded in the CLI binary, so installation works
offline and is versioned with the release.

```bash
ankra skills list                  # list available skills, marking installed ones
ankra skills install               # install all into ~/.cursor/skills (personal)
ankra skills install --editor claude-code  # install all into ~/.claude/skills
ankra skills install --project .   # install into ./.cursor/skills (project)
ankra skills install --editor claude-code --project .  # install into ./.claude/skills
ankra skills install ankra-gitops  # install only named skills
ankra skills uninstall             # remove all Ankra skills
```

Use `--force` to overwrite existing skills and `--source <dir>` to install from a local
skills directory instead of the embedded copy. This is separate from `ankra openclaw skill`,
which generates a per-cluster SKILL.md.

#### Manage Dependency Parents from the CLI

`ankra cluster addons upgrade` and `ankra cluster manifests upgrade` now accept
`--add-parent`, `--remove-parent`, and `--set-parent` flags to edit a resource's
dependency parents (which control deployment ordering inside a stack) without
re-applying the whole `cluster.yaml`. Parents are given as
`name=<name>,kind=<manifest|addon>` (kind defaults to `manifest`).

```bash
# Make an addon wait for a namespace manifest
ankra cluster addons upgrade infisical \
  --add-parent name=infisical-ns,kind=manifest \
  --cluster website-demo

# Replace all parents at once
ankra cluster manifests upgrade web \
  --set-parent name=infisical-ns,kind=manifest \
  --set-parent name=infisical,kind=addon \
  --cluster website-demo

# Remove a parent (removing the last one clears the link)
ankra cluster manifests upgrade web \
  --remove-parent name=infisical-ns,kind=manifest \
  --cluster website-demo
```

`--set-parent` replaces the list wholesale and is mutually exclusive with
`--add-parent` / `--remove-parent`.

#### Read and Delete Manifests and Addon Values

Two new read commands print the current stored content, ready to pipe to a file
or edit and re-apply:

```bash
ankra cluster addons values website > values.yaml
ankra cluster manifests get web > web.yaml
```

Both support `-o raw` to emit the base64-encoded form. A new
`ankra cluster manifests delete <name>` command disconnects a manifest from its
stack (removing its resources from the cluster); the owning stack is resolved
automatically and a confirmation prompt protects the operation (skip with
`--yes`, preview with `--dry-run`).

#### Patch a Manifest In-Place with `--set`

`ankra cluster manifests upgrade` now accepts helm-style `--set`, `--set-string`,
and `--set-file` flags to mutate a single path inside a manifest's Kubernetes
YAML, instead of only replacing the whole file. This makes it easy to bump, for
example, a Deployment image tag from CI.

```bash
# Bump a Deployment's image tag in place
ankra cluster manifests upgrade web \
  --set 'spec.template.spec.containers[name=app].image=nginx:1.27' \
  --cluster website-demo

# Pick a document when the manifest holds several
ankra cluster manifests upgrade web \
  --target-kind Deployment --target-name web \
  --set 'spec.replicas=3' \
  --cluster website-demo
```

`--set*` MUTATE the existing manifest and are mutually exclusive with
`--from-file` / `--manifest -`, which REPLACE it. When a manifest contains more
than one document, use `--target-kind` / `--target-name` to choose which one to
edit.

#### Address List Items by Field with `--set` Selectors

Both `manifests upgrade` and `addons upgrade` `--set` paths can now address a
list item by a stable field instead of a fragile numeric index. For example,
`containers[name=app].image` targets the container named `app`, and
`env[name=LOG_LEVEL].value` targets that environment entry. A selector that
matches nothing fails with a clear error rather than silently creating an entry.
Numeric indexes (`containers[0]`) continue to work.

#### Run Commands Against a Specific Organisation (`--org`)

A new global `--org` flag (or the `ANKRA_ORG` environment variable) runs a
single command against any organisation you belong to, without changing your
selected organisation. The value accepts an organisation name or ID.

```bash
# Run against another organisation by name, just for this command
ankra --org "Acme Corp" cluster list

# Or by ID
ankra --org 22222222-2222-2222-2222-222222222222 get pods my-cluster

# Scope a whole shell session via the environment
export ANKRA_ORG="Acme Corp"
ankra cluster list
```

The override is per-request: it does not call `ankra org switch` and leaves your
persistently selected organisation untouched. You must be an active member of the
requested organisation, otherwise the API returns a permission error.

#### Control Plane Management

Inspect and change the control plane of a stopped cluster, without going through
the dashboard.

```bash
# Show the current configuration
ankra cluster hetzner control-plane get <cluster_id>

# Switch between 1 and 3 controllers (etcd quorum: only 1 or 3 is allowed)
ankra cluster hetzner control-plane set-count <cluster_id> 3

# Change the controller instance type
ankra cluster hetzner control-plane set-instance-type <cluster_id> cx33
```

The same commands are available for OVH (`ankra cluster ovh control-plane …`)
and UpCloud (`ankra cluster upcloud control-plane …`). The cluster must be
stopped; changes apply the next time you start the cluster.

#### Cluster Nodes Listing

List every server Ankra manages for the cluster (control plane, workers, and
bastion or gateway), or drill into one for full spec and metadata. Soft-deleted
entries from a stopped cluster are listed too, so the saved topology is visible
before re-provisioning.

```bash
ankra cluster hetzner nodes list <cluster_id>
ankra cluster hetzner nodes list <cluster_id> --json
ankra cluster hetzner nodes get <cluster_id> <node_id>
ankra cluster hetzner nodes get <cluster_id> <node_id> --json
```

Available for all providers (`hetzner`, `ovh`, `upcloud`).

#### Surgical Addon and Manifest Upgrades

Two new subcommands for in-place updates against the existing partial-stack endpoint - no more hand-editing the full `ImportCluster.yaml`.

##### Bump an addon's chart version

```bash
ankra cluster addons upgrade ankra-website \
  --chart-version 1.0.146 \
  --cluster website-demo
```

##### Tweak a single Helm values field with `--set` (helm-style)

```bash
ankra cluster addons upgrade website \
  --set image.tag=1.0.146 \
  --cluster website-demo
```

`--set` accepts comma-separated dotted paths with array indexing (`ingress.hosts[0].host=demo.ankra.io`).

> `--set` vs `--set-string`: `--set image.tag=1.0.146` keeps the value a string because `1.0.146` is not a valid number. `--set image.tag=2.0` would coerce to the float `2.0`, which Helm renders as `2`. When the value is a valid number/bool but you want it to stay a string, use `--set-string image.tag=2.0`. `--set-file key=path` reads file contents as the value (useful for certs or configmap blobs).

##### Replace the whole values document

```bash
ankra cluster addons upgrade website \
  --values-from-file ./values.yaml \
  --cluster website-demo
```

`--set*` and `--values-from-file` are mutually exclusive: `--set*` mutates the existing document while `--values-from-file` replaces it.

##### Update a manifest

```bash
ankra cluster manifests upgrade demo-namespace \
  --from-file manifests/demo-namespace.yaml \
  --cluster website-demo
```

##### Common options

- `--cluster <name|id>` - defaults to the selected cluster.
- `--stack <name>` - addons only, required when the same addon name exists in multiple stacks. Manifest names are globally unique on a cluster, so `manifests upgrade` has no `--stack` flag.
- `--registry-name`, `--registry-url`, `--registry-credential-name` - atomically retag the addon's registry.
- `--namespace` - destructive for addons (Helm reinstall); requires `--yes` or interactive confirmation.
- `--dry-run` - print the before/after YAML; no API write.
- `-o json|yaml` - machine-readable output for CI scripts.

All upgrades go through the same partial-stack endpoint as the UI, so they are atomic, locked, and produce a single git commit per invocation when gitops is enabled.

### API Endpoints

- `GET /api/v1/clusters/{provider}/{id}/control-plane` - read controller count, instance type and editability
- `PUT /api/v1/clusters/{provider}/{id}/control-plane` - change controller count (1 or 3)
- `PUT /api/v1/clusters/{provider}/{id}/control-plane/instance-type` - change controller instance type
- `GET /api/v1/clusters/{provider}/{id}/nodes` - list all managed servers for the cluster
- `GET /api/v1/clusters/{provider}/{id}/nodes/{node_id}` - full spec and metadata for a node

### Deprecations

- `ankra chat` currently uses the bearer-token streaming endpoints
  `/api/v1/chat/general` and `/api/v1/org/clusters/{cluster_id}/kubernetes/chat`.
  These are now deprecated and will be removed in a future release; the platform
  now responds with `Deprecation: true` and a `Sunset` header on these routes.
  When the warning prints, upgrade `ankra-cli` to the next release once a
  resumable session-based replacement has shipped on the platform.

## v0.1.129 - April 2026

### New Features

#### Node Group Management

Full CRUD for node groups on Hetzner, OVH, and UpCloud clusters. Each node group has its own instance type, node count, Kubernetes labels, and taints.

##### List Node Groups

```bash
ankra cluster hetzner node-group list <cluster_id>
```

Example output:

```
default              type=cx33     count=2  labels=0  taints=0
gpu-workers          type=ccx33    count=3  labels=1  taints=1
```

##### Add a Node Group

```bash
ankra cluster hetzner node-group add <cluster_id> \
  --name gpu-workers \
  --instance-type ccx33 \
  --count 3
```

##### Scale a Node Group

```bash
ankra cluster hetzner node-group scale <cluster_id> default 4
```

Node groups can be scaled to 0 (removes all servers but keeps the group definition).

##### Upgrade Instance Type

```bash
ankra cluster hetzner node-group upgrade <cluster_id> default cx43
```

Instance type upgrades are irreversible - Hetzner disk enlargement cannot be undone. To use a smaller type, create a new node group and delete the old one.

##### Delete a Node Group

```bash
ankra cluster hetzner node-group delete <cluster_id> gpu-workers
```

##### OVH and UpCloud

The same commands are available for OVH and UpCloud clusters:

```bash
# OVH
ankra cluster ovh node-group list <cluster_id>
ankra cluster ovh node-group add <cluster_id> --name workers --instance-type b2-15 --count 2
ankra cluster ovh node-group scale <cluster_id> workers 4
ankra cluster ovh node-group upgrade <cluster_id> workers b2-30
ankra cluster ovh node-group delete <cluster_id> workers

# UpCloud
ankra cluster upcloud node-group list <cluster_id>
ankra cluster upcloud node-group add <cluster_id> --name workers --instance-type 4xCPU-8GB --count 2
ankra cluster upcloud node-group scale <cluster_id> workers 4
ankra cluster upcloud node-group upgrade <cluster_id> workers 8xCPU-16GB
ankra cluster upcloud node-group delete <cluster_id> workers
```

#### Node Groups at Cluster Creation

The `node_groups` field is now supported in the cluster create API for all providers. When provided, it replaces `worker_count` and `worker_server_type`:

```json
{
  "node_groups": [
    {"name": "default", "instance_type": "cx33", "count": 2},
    {"name": "gpu", "instance_type": "ccx33", "count": 1, "labels": {"gpu": "true"}, "taints": [{"key": "gpu", "value": "true", "effect": "NoSchedule"}]}
  ]
}
```

### Improvements

- **Server naming**: Servers are now named `{cluster}-{group_name}-{index}` instead of `{cluster}-worker-{index}` for better identification.
- **No online requirement**: Node group operations no longer require the cluster to be online.
- **Safe instance type changes**: Servers are powered off, verified off, resized, then powered back on. If the resize fails, the server is powered back on automatically.
- **Graceful K8s cleanup**: K8s uninstall during node deletion is now best-effort - unreachable nodes (powered off, deleted) no longer block the delete operation.

### API Endpoints

- `GET /api/v1/clusters/hetzner/{id}/node-groups` - list node groups
- `POST /api/v1/clusters/hetzner/{id}/node-groups` - add a node group
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/scale` - scale a node group
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/instance-type` - upgrade instance type
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/labels` - update labels
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/taints` - update taints
- `DELETE /api/v1/clusters/hetzner/{id}/node-groups/{name}` - delete a node group

Same endpoints available for OVH (`/clusters/ovh/...`) and UpCloud (`/clusters/upcloud/...`).

---

## v0.1.128 - April 2026

### New Features

#### Hetzner: Multiple SSH Key Support

Hetzner cluster creation now supports attaching multiple SSH key credentials with the `--ssh-key-credential-ids` flag. Pass a comma-separated list of credential IDs to deploy multiple keys to all servers.

```bash
ankra cluster hetzner create \
  --name my-cluster \
  --credential-id <hetzner_credential_id> \
  --ssh-key-credential-ids <key_id_1>,<key_id_2>,<key_id_3> \
  --location fsn1 \
  --control-plane-count 1 \
  --worker-count 2
```

The existing `--ssh-key-credential-id` flag continues to work for single-key usage.

#### UpCloud Cloud Cluster Management

Full lifecycle management for UpCloud clusters, including provisioning, deprovisioning, scaling, and Kubernetes version upgrades. UpCloud clusters use managed SDN Routers and NAT Gateways for private networking.

##### Create a Cluster

```bash
ankra cluster upcloud create \
  --name my-cluster \
  --credential-id <upcloud_credential_id> \
  --ssh-key-credential-id <ssh_key_credential_id> \
  --zone fi-hel1 \
  --control-plane-count 1 \
  --control-plane-plan 2xCPU-4GB \
  --worker-count 2 \
  --worker-plan 2xCPU-4GB
```

##### Deprovision a Cluster

Deprovision now uses the DAG-based operation system. Resources are deleted in the correct dependency order via the scheduler, and the cluster is only removed once all resources are cleaned up.

```bash
ankra cluster upcloud deprovision <cluster_id>
```

Example output:

```
UpCloud cluster deprovision initiated!
  Cluster ID: abc123
  Operation ID: op-456
  Resources queued for deletion: 11
```

##### Check Worker Count

```bash
ankra cluster upcloud workers <cluster_id>
```

##### Scale Workers

```bash
ankra cluster upcloud scale <cluster_id> 4
```

##### Check Kubernetes Version

```bash
ankra cluster upcloud k8s-version <cluster_id>
```

##### Upgrade Kubernetes Version

```bash
ankra cluster upcloud upgrade <cluster_id> v1.31.2+k3s1
```

#### UpCloud API Credentials

Manage UpCloud API credentials for cluster provisioning. UpCloud uses a single API token for authentication.

##### List UpCloud Credentials

```bash
ankra credentials upcloud list
```

##### Create an UpCloud Credential

```bash
ankra credentials upcloud create --name my-upcloud-cred --api-token <token>
```

##### List SSH Key Credentials

```bash
ankra credentials upcloud ssh-key list
```

##### Create an SSH Key Credential

```bash
ankra credentials upcloud ssh-key create --name my-key --generate
ankra credentials upcloud ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."
```

### Improvements

- **DAG-based deprovision**: Cluster deletion now creates a tracked operation with individual delete jobs, visible in the Operations UI. The cluster is only marked as deleted once all resources are successfully destroyed.
- **Parallel server deletion**: Multiple server delete jobs run concurrently in the DAG, reducing deprovision time.
- **Best-effort agent uninstall**: The Ankra agent uninstall step no longer blocks deprovision if SSH or Helm is unavailable.

### API Endpoints

- `POST /api/v1/clusters/upcloud` - create an UpCloud cluster
- `DELETE /api/v1/clusters/upcloud/{id}` - deprovision a cluster (returns operation ID)
- `GET /api/v1/clusters/upcloud/{id}/worker-count` - get worker count
- `POST /api/v1/clusters/upcloud/{id}/scale-workers` - scale workers
- `GET /api/v1/clusters/upcloud/{id}/k8s-version` - get Kubernetes version
- `POST /api/v1/clusters/upcloud/{id}/upgrade-k8s-version` - upgrade Kubernetes version
- `GET /api/v1/credentials/upcloud` - list UpCloud credentials
- `POST /api/v1/credentials/upcloud` - create an UpCloud credential
- `GET /api/v1/credentials/upcloud/ssh-keys` - list SSH key credentials
- `POST /api/v1/credentials/upcloud/ssh-key` - create an SSH key credential

---

## v0.1.127

### New Features

#### OVH Cloud Cluster Management

Full lifecycle management for OVH Cloud clusters, including provisioning, deprovisioning, scaling, and Kubernetes version upgrades.

##### Create a Cluster

```bash
ankra cluster ovh create \
  --name my-cluster \
  --credential-id <ovh_credential_id> \
  --ssh-key-credential-id <ssh_key_credential_id> \
  --region GRA7 \
  --control-plane-count 1 \
  --control-plane-flavor-id b2-15 \
  --worker-count 2 \
  --worker-flavor-id b2-15
```

##### Deprovision a Cluster

```bash
ankra cluster ovh deprovision <cluster_id>
```

##### Check Worker Count

```bash
ankra cluster ovh workers <cluster_id>
```

Example output:

```
Worker Count: 2
```

##### Scale Workers

```bash
ankra cluster ovh scale <cluster_id> 4
```

Example output:

```
Scaling workers.
  Previous count: 2
  New count:      4
```

##### Check Kubernetes Version

```bash
ankra cluster ovh k8s-version <cluster_id>
```

Example output:

```
Kubernetes Version: v1.31.2+k3s1
  Distribution: k3s
```

##### Upgrade Kubernetes Version

```bash
ankra cluster ovh upgrade <cluster_id> v1.35.1+k3s1
```

Example output:

```
Kubernetes version upgrade initiated.
  Previous version: v1.31.2+k3s1
  New version:      v1.35.1+k3s1
  Nodes affected:   3
```

#### OVH API Credentials

Manage OVH Cloud API credentials for cluster provisioning.

##### List OVH Credentials

```bash
ankra credentials ovh list
```

##### Create an OVH Credential

```bash
ankra credentials ovh create --name my-ovh-cred --project-id <project_id>
```

Prompts securely for application key, application secret, and consumer key. Credentials are validated against the OVH API on creation.

##### List SSH Key Credentials

```bash
ankra credentials ovh ssh-key list
```

##### Create an SSH Key Credential

```bash
ankra credentials ovh ssh-key create --name my-key --generate
```

Use `--generate` to create a new keypair, or omit it to provide your own public key.

### API Endpoints

- `POST /api/v1/clusters/ovh` - create an OVH cluster
- `DELETE /api/v1/clusters/ovh/{id}` - deprovision a cluster
- `GET /api/v1/clusters/ovh/{id}/worker-count` - get worker count
- `POST /api/v1/clusters/ovh/{id}/scale-workers` - scale workers
- `GET /api/v1/clusters/ovh/{id}/k8s-version` - get Kubernetes version
- `POST /api/v1/clusters/ovh/{id}/upgrade-k8s-version` - upgrade Kubernetes version
- `GET /api/v1/credentials/ovh` - list OVH credentials
- `POST /api/v1/credentials/ovh` - create an OVH credential
- `GET /api/v1/credentials/ovh/ssh-keys` - list SSH key credentials
- `POST /api/v1/credentials/ovh/ssh-key` - create an SSH key credential

---

## v0.1.126

### New Features

#### Hetzner Worker Scaling

Scale worker nodes on a Hetzner cluster up or down (1-10 nodes):

```bash
ankra cluster hetzner scale <cluster_id> <count>
```

Example:

```bash
ankra cluster hetzner scale abc123 5
```

Example output:

```
Scaling workers.
  Previous count: 3
  New count:      5
```

#### Hetzner Kubernetes Version Upgrade

Upgrade the Kubernetes (k3s) version across all nodes in a Hetzner cluster:

```bash
ankra cluster hetzner upgrade <cluster_id> <target_version>
```

Example:

```bash
ankra cluster hetzner upgrade abc123 v1.30.0+k3s1
```

Example output:

```
Kubernetes version upgrade initiated.
  Previous version: v1.29.1+k3s1
  New version:      v1.30.0+k3s1
  Nodes affected:   4
```

### API Endpoints

- `POST /api/v1/clusters/hetzner/{id}/scale-workers` - scale workers
- `GET /api/v1/clusters/hetzner/{id}/k8s-version` - fetch current k8s version
- `POST /api/v1/clusters/hetzner/{id}/upgrade-k8s-version` - trigger k8s version upgrade

---

## v0.1.125

### New Features

#### Kubernetes Version Query

Check the current Kubernetes version running on a Hetzner cluster:

```bash
ankra cluster hetzner k8s-version <cluster_id>
```

Example output:

```
Kubernetes Version: v1.29.1+k3s1
  Distribution: k3s
```

#### Kubernetes Version Upgrade

Upgrade the Kubernetes (k3s) version across all nodes in a Hetzner cluster:

```bash
ankra cluster hetzner upgrade <cluster_id> <target_version>
```

Example:

```bash
ankra cluster hetzner upgrade abc123 v1.30.0+k3s1
```

Example output:

```
Kubernetes version upgrade initiated.
  Previous version: v1.29.1+k3s1
  New version:      v1.30.0+k3s1
  Nodes affected:   4
```

### API Endpoints

- `GET /api/v1/clusters/hetzner/{id}/k8s-version` - fetch current k8s version
- `POST /api/v1/clusters/hetzner/{id}/upgrade-k8s-version` - trigger k8s version upgrade

---

## v0.1.124

### New Features

#### Hetzner Cluster Management

Full lifecycle management for Hetzner clusters, including provisioning, deprovisioning, and scaling.

##### Create a Cluster

```bash
ankra cluster hetzner create \
  --name my-cluster \
  --credential-id <cred_id> \
  --ssh-key-credential-id <ssh_key_id> \
  --location fsn1 \
  --worker-count 3 \
  --worker-server-type cx33 \
  --control-plane-count 1 \
  --distribution k3s
```

##### Deprovision a Cluster

```bash
ankra cluster hetzner deprovision <cluster_id>
```

Example output:

```
Hetzner cluster deprovisioned successfully!
  Cluster ID: abc123
  Deleted servers: 4
  Deleted networks: 1
  Deleted SSH keys: 1
```

##### Check Worker Count

```bash
ankra cluster hetzner workers <cluster_id>
```

Example output:

```
Worker Count: 3
  Min: 1
  Max: 10
```

##### Scale Workers

```bash
ankra cluster hetzner scale <cluster_id> <worker_count>
```

Example:

```bash
ankra cluster hetzner scale abc123 5
```

Example output:

```
Scaling up from 3 to 5 workers.
```

#### Hetzner Credentials Management

Manage Hetzner API credentials and SSH keys.

##### List Hetzner Credentials

```bash
ankra credentials hetzner list
```

##### Create a Hetzner Credential

```bash
ankra credentials hetzner create --name my-hetzner-key
```

You will be prompted securely for the API token.

##### List SSH Key Credentials

```bash
ankra credentials hetzner ssh-key list
```

##### Create an SSH Key Credential

```bash
# Generate a new keypair
ankra credentials hetzner ssh-key create --name my-key --generate

# Or provide an existing public key
ankra credentials hetzner ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."
```

#### Stack Cloning Between Clusters

Clone stacks from one cluster to another as a draft for review before deployment.

```bash
# Clone a stack to another cluster
ankra cluster stacks clone my-stack --to target-cluster

# Clone with a new name
ankra cluster stacks clone my-stack --to target-cluster --name new-stack-name

# Clone without addon configurations
ankra cluster stacks clone my-stack --to target-cluster --include-config=false
```

Example output:

```
Cloning stack 'my-stack' to cluster 'target-cluster'...

Stack cloned successfully!
  Draft ID:    draft-456
  Stack Name:  my-stack
  Addons:      3
  Manifests:   2

The stack has been created as a draft. Open the Ankra dashboard to review and deploy.
```

---

## v0.1.123

### SOPS Encryption Commands

New commands for encrypting and decrypting manifest and addon configuration files using SOPS.

#### Breaking Change

- **Removed**: `ankra cluster sops <secret>` command has been removed

#### New Commands

##### Encrypt Manifest

Encrypt a specific key in a manifest file referenced by the cluster configuration.

```bash
ankra cluster encrypt manifest <manifest_name> --key <key_name> -f <cluster.yaml>
```

Example:
```bash
ankra cluster encrypt manifest trinity-database-secret --key TRINITY_DB_PASSWORD -f cluster.yaml
```

This will:
1. Find the manifest in the cluster YAML
2. Read the referenced manifest file
3. Encrypt the specified key using your organisation's SOPS key
4. Update the manifest file with encrypted values
5. Add the key to `encrypted_paths` in the cluster YAML

##### Encrypt Addon

Encrypt a specific key in an addon's values file.

```bash
ankra cluster encrypt addon --name <addon_name> --key <key_name> -f <cluster.yaml>
```

Example:
```bash
ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml
```

##### Decrypt Manifest

Decrypt and display the contents of a manifest file.

```bash
ankra cluster decrypt manifest <manifest_name> -f <cluster.yaml>
```

Example:
```bash
ankra cluster decrypt manifest trinity-database-secret -f cluster.yaml
```

#### Features

- **Add keys to existing encrypted files**: You can add new encrypted keys to files that are already SOPS-encrypted (as long as they were encrypted with your organisation's key)
- **Clear error messages**: If you try to encrypt a file that was encrypted by a different organisation, you'll get a helpful error message explaining the issue

---

## v0.1.122 and earlier - initial releases

Originally published as "Ankra CLI v1.0.0"; the tags actually shipped for this
initial line were `v0.1.115` through `v0.1.122`.

### Highlights

This release introduces the **Ankra CLI** - a powerful command-line interface for managing your Kubernetes infrastructure. Authenticate with SSO, chat with AI about your clusters, browse Helm charts, manage credentials, and control stacks - all from your terminal.

---

### New Features

#### SSO Authentication

Securely authenticate with the Ankra platform using browser-based SSO login with PKCE.

```bash
# Login to Ankra (opens browser for SSO)
ankra login

# Logout and clear credentials
ankra logout
```

Your credentials are securely stored in `~/.ankra.yaml` and automatically used for all subsequent commands.

---

#### AI-Powered Chat

Get instant help troubleshooting your infrastructure with AI-powered chat. Ask questions about your clusters, get recommendations, and analyze health issues.

##### Interactive Chat Mode

```bash
# Start an interactive chat session
ankra chat

# Chat with cluster context for better answers
ankra chat --cluster my-production-cluster
```

##### One-Shot Questions

```bash
# Ask a single question
ankra chat "Why are my pods in CrashLoopBackOff?"

# Ask with cluster context
ankra chat --cluster staging "How do I scale my deployment?"
```

##### Cluster Health Analysis

```bash
# Get AI-analyzed cluster health for the selected cluster
ankra chat health

# Include detailed AI analysis
ankra chat health --ai
```

##### Chat History Management

```bash
# List previous conversations
ankra chat history

# Show a specific conversation
ankra chat show <conversation_id>

# Delete a conversation
ankra chat delete <conversation_id>
```

---

#### Helm Charts

Browse and search the Helm chart catalog directly from your terminal.

##### List Available Charts

```bash
# List all available charts
ankra charts list

# Paginate through charts
ankra charts list --page 2 --page-size 50

# Show only subscribed charts
ankra charts list --subscribed
```

##### Search Charts

```bash
# Search for charts by name
ankra charts search nginx

# Search for monitoring solutions
ankra charts search prometheus
```

##### Chart Information

```bash
# Get detailed info about a chart
ankra charts info nginx

# Specify a repository
ankra charts info grafana --repository https://grafana.github.io/helm-charts
```

**Example Output:**

```
Chart: nginx

  Repository: bitnami (https://charts.bitnami.com/bitnami)

  Available Versions (10):
    - 15.1.2
    - 15.1.1
    - 15.1.0
    ...

  Available Profiles:
    - default: Standard nginx deployment
    - high-availability: Multi-replica HA setup
```

---

#### Credentials Management

Manage cloud provider and Git credentials for your clusters.

##### List Credentials

```bash
# List all credentials
ankra credentials list

# Filter by provider
ankra credentials list --provider github
```

##### View Credential Details

```bash
# Get details of a specific credential
ankra credentials get <credential_id>
```

##### Validate & Delete

```bash
# Check if a credential name is available
ankra credentials validate my-new-credential

# Delete a credential
ankra credentials delete <credential_id>
```

**Aliases:** `ankra creds`, `ankra cred`, `ankra credential`

---

#### Stack Management

Create, manage, and track infrastructure stacks on your clusters.

##### List & View Stacks

```bash
# First, select a cluster
ankra cluster select

# List all stacks on the active cluster
ankra cluster stacks list

# View details of a specific stack
ankra cluster stacks list my-monitoring-stack
```

**Example Output:**

```
Stack Details:
  Name:        my-monitoring-stack
  Description: Production monitoring
  State:       up
  Manifests:   3
  Addons:      2

  Manifests:
    ✓ prometheus-config
      ├─ kind: ConfigMap
      ├─ namespace: monitoring
      ├─ state: up
      └─ parents: none

  Addons:
    ✓ grafana
      ├─ chart: grafana:6.50.7
      ├─ namespace: monitoring
      ├─ state: up
      └─ parents: none
```

##### Create & Delete Stacks

```bash
# Create a new stack
ankra cluster stacks create my-new-stack --description "Application stack"

# Delete a stack
ankra cluster stacks delete old-stack
```

##### Rename & History

```bash
# Rename a stack
ankra cluster stacks rename old-name new-name

# View change history for a stack
ankra cluster stacks history my-stack
```

---

#### Cluster Clone

Clone stacks from an existing cluster to a new cluster configuration. Supports both local files and remote URLs.

```bash
# Clone all stacks from one cluster to another
ankra cluster clone source-cluster.yaml new-cluster.yaml

# Clone from a remote URL
ankra cluster clone https://github.com/org/repo/raw/main/cluster.yaml new-cluster.yaml

# Clone only specific stacks
ankra cluster clone cluster.yaml new-cluster.yaml --stack "monitoring" --stack "networking"

# Replace all stacks in the target cluster
ankra cluster clone cluster.yaml new-cluster.yaml --clean

# Force merge even with naming conflicts
ankra cluster clone cluster.yaml new-cluster.yaml --force

# Copy missing files from skipped stacks
ankra cluster clone cluster.yaml new-cluster.yaml --copy-missing
```

---

#### API Tokens

Manage API tokens for programmatic access.

```bash
# List all API tokens
ankra tokens list

# Create a new token
ankra tokens create my-ci-token

# Create token with expiration
ankra tokens create my-temp-token --expires "2024-12-31T00:00:00Z"

# Revoke a token
ankra tokens revoke <token_id>

# Delete a revoked token
ankra tokens delete <token_id>
```

---

#### Cluster Operations

```bash
# List all clusters
ankra cluster list

# Get cluster details
ankra cluster get my-cluster

# Select a cluster for subsequent commands
ankra cluster select

# Trigger reconciliation
ankra cluster reconcile my-cluster
```

---

### Bug Fixes

#### `ankra cluster clone` - Registry Linkage Fix

Fixed an issue where `ankra cluster clone` did not correctly format the linkage to existing registries when cloning stacks or entire clusters. Addon configurations that reference container registries (`registry_name`, `registry_url`, `registry_credential_name`) are now properly preserved and formatted in the cloned configuration.

**Before:** Registry references in cloned addons could be malformed or missing, causing deployment failures when the cloned cluster tried to pull images from private registries.

**After:** All registry linkage fields are correctly preserved and formatted, ensuring seamless deployments with private container registries.

---

#### `ankra chat` - API Request & Response Format Fix

Fixed issues where the chat command had incompatible field names with the backend API:

1. **Request fields:** The CLI was sending `message` and `history` fields, but the backend expects `query` and `conversation_history`.

2. **Response parsing:** The CLI was looking for `content` field in streaming events, but the backend sends content in the `data` field.

3. **Status message formatting:** Status messages (like "Processing...") were being concatenated inline with content, making output hard to read.

**Before:** Chat would fail with 422 validation errors, show empty responses, or display status messages inline with content:
```
Assistant: Processing...I'll generate a report...
```

**After:** The CLI now correctly sends `query` and `conversation_history` fields, properly parses the `data` field from streaming events, and formats status messages on separate lines:
```
Assistant: [Processing...]
I'll generate a report...
```

---

### Getting Started

```bash
# 1. Install the CLI (download from releases)

# 2. Login with SSO
ankra login

# 3. List your clusters
ankra cluster list

# 4. Select a cluster to work with
ankra cluster select

# 5. Start chatting with AI about your infrastructure
ankra chat "What's the status of my deployments?"
```

---

### Configuration

The CLI stores configuration in `~/.ankra.yaml`:

- **token**: Your API authentication token
- **base-url**: The Ankra platform URL (defaults to https://platform.ankra.app)

You can also use environment variables:

- `ANKRA_API_TOKEN`: Override the stored token
- `ANKRA_BASE_URL`: Override the base URL

---

**Full documentation:** https://docs.ankra.app/cli
