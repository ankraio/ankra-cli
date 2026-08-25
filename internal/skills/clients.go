package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agent clients disagree about almost everything except markdown: where
// skills live, which file is always in context, whether slash commands
// exist, and whether the process can read the local filesystem at all. A
// Client captures those differences as data so install/uninstall stay one
// code path, and adding the next assistant is a table entry rather than a
// new branch.

// Layout is where one client keeps its artefacts, relative to the scope root
// (the home directory for a personal install, the project directory for a
// project install). An empty field means the client has no such artefact and
// install silently skips it.
type Layout struct {
	// Skills is the directory the skill folders are copied into.
	Skills string
	// Instructions is the always-loaded markdown file that carries the
	// managed Ankra block: the routing rule plus the index of installed
	// skills. Clients that steer with a rule mechanism instead (Cursor)
	// leave it empty.
	Instructions string
	// Commands is the directory that holds the workflow command files
	// (slash commands / prompt files / workflows).
	Commands string
	// Hooks is the config file the 'ankra skills guard' hook is merged into.
	Hooks string
}

// Client is one assistant the skills can be installed for.
type Client struct {
	// ID is the canonical --client value.
	ID string
	// Aliases are the other spellings accepted for ID.
	Aliases []string
	// DisplayName is how the client is named in output.
	DisplayName string
	// Personal is the layout for a home-directory install.
	Personal Layout
	// Project is the layout for a repository-scoped install.
	Project Layout
	// RuleFormat selects how the always-applied routing rule is written:
	// "cursor" for Cursor's plugin/.mdc mechanism, "block" for a managed
	// block inside Layout.Instructions, "" for clients that take neither.
	RuleFormat string
	// CommandFormat selects the workflow command file format: "markdown",
	// "copilot" (.prompt.md with a mode header), "toml" (Gemini CLI), or ""
	// when the client has no command mechanism.
	CommandFormat string
	// HookFormat selects the hook event dialect: "cursor", "claude", or ""
	// when the client exposes no shell hook.
	HookFormat string
	// LoadsSkillsNatively is true when the client discovers SKILL.md files
	// in Layout.Skills by itself. When false the managed index in
	// Layout.Instructions is what points the agent at them.
	LoadsSkillsNatively bool
	// Packaged is true for clients that cannot read the local filesystem, so
	// install writes uploadable .zip bundles instead of loose directories.
	Packaged bool
	// Detect are paths (relative to the scope root) whose presence means the
	// client is in use here; used by --client auto.
	DetectPersonal []string
	DetectProject  []string
	// Note is printed after a successful install when the client needs one
	// more manual step than "restart the app".
	Note string
}

// ankraLibrary is the client-neutral home for skill bodies. Assistants that
// have no skills directory of their own still read files, so the skills go
// somewhere stable and the managed index points at them by path.
const ankraLibrary = ".ankra/skills"

// clientRegistry is the full set of supported clients, in the order they are
// listed and installed.
var clientRegistry = []Client{
	{
		ID:          "claude-code",
		Aliases:     []string{"claude", "claudecode", "cc"},
		DisplayName: "Claude Code",
		Personal: Layout{
			Skills:       ".claude/skills",
			Instructions: ".claude/CLAUDE.md",
			Commands:     ".claude/commands",
			Hooks:        ".claude/settings.json",
		},
		Project: Layout{
			Skills:       ".claude/skills",
			Instructions: "CLAUDE.md",
			Commands:     ".claude/commands",
			Hooks:        ".claude/settings.json",
		},
		RuleFormat:          "block",
		CommandFormat:       "markdown",
		HookFormat:          "claude",
		LoadsSkillsNatively: true,
		DetectPersonal:      []string{".claude"},
		DetectProject:       []string{".claude", "CLAUDE.md"},
	},
	{
		ID:          "claude-app",
		Aliases:     []string{"claude-desktop", "claude-ai", "claudeai", "desktop"},
		DisplayName: "Claude app (claude.ai / desktop)",
		Personal:    Layout{Skills: ".ankra/claude-app"},
		Project:     Layout{Skills: ".ankra/claude-app"},
		Packaged:    true,
		DetectPersonal: []string{
			"Library/Application Support/Claude",
			".config/Claude",
			"AppData/Roaming/Claude",
		},
		Note: "Upload each .zip at claude.ai → Settings → Capabilities → Skills → Upload skill " +
			"(the Claude app cannot read your filesystem, so the bundles are the delivery).",
	},
	{
		ID:          "cursor",
		Aliases:     []string{"cursor-ide"},
		DisplayName: "Cursor",
		Personal: Layout{
			Skills:   ".cursor/skills",
			Commands: ".cursor/commands",
			Hooks:    ".cursor/hooks.json",
		},
		Project: Layout{
			Skills:   ".cursor/skills",
			Commands: ".cursor/commands",
			Hooks:    ".cursor/hooks.json",
		},
		RuleFormat:          "cursor",
		CommandFormat:       "markdown",
		HookFormat:          "cursor",
		LoadsSkillsNatively: true,
		DetectPersonal:      []string{".cursor"},
		DetectProject:       []string{".cursor"},
	},
	{
		ID:          "codex",
		Aliases:     []string{"openai-codex", "codex-cli"},
		DisplayName: "Codex CLI",
		Personal: Layout{
			Skills:       ".codex/skills",
			Instructions: ".codex/AGENTS.md",
			Commands:     ".codex/prompts",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: "AGENTS.md",
		},
		RuleFormat:     "block",
		CommandFormat:  "markdown",
		DetectPersonal: []string{".codex"},
		DetectProject:  []string{"AGENTS.md"},
	},
	{
		ID:          "copilot",
		Aliases:     []string{"github-copilot", "vscode"},
		DisplayName: "GitHub Copilot",
		Personal: Layout{
			Skills:       ankraLibrary,
			Instructions: ".ankra/AGENTS.md",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: ".github/copilot-instructions.md",
			Commands:     ".github/prompts",
		},
		RuleFormat:     "block",
		CommandFormat:  "copilot",
		DetectPersonal: []string{".vscode", "Library/Application Support/Code/User", ".config/Code/User"},
		DetectProject:  []string{".github"},
		Note: "Copilot reads .github/copilot-instructions.md per repository — install with " +
			"--project <repo> for it to take effect, and commit the result.",
	},
	{
		ID:          "windsurf",
		Aliases:     []string{"codeium"},
		DisplayName: "Windsurf",
		Personal: Layout{
			Skills:       ankraLibrary,
			Instructions: ".codeium/windsurf/memories/global_rules.md",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: ".windsurf/rules/ankra.md",
			Commands:     ".windsurf/workflows",
		},
		RuleFormat:     "block",
		CommandFormat:  "markdown",
		DetectPersonal: []string{".codeium/windsurf"},
		DetectProject:  []string{".windsurf"},
	},
	{
		ID:          "gemini",
		Aliases:     []string{"gemini-cli", "google"},
		DisplayName: "Gemini CLI",
		Personal: Layout{
			Skills:       ".gemini/skills",
			Instructions: ".gemini/GEMINI.md",
			Commands:     ".gemini/commands/ankra",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: "GEMINI.md",
			Commands:     ".gemini/commands/ankra",
		},
		RuleFormat:     "block",
		CommandFormat:  "toml",
		DetectPersonal: []string{".gemini"},
		DetectProject:  []string{".gemini", "GEMINI.md"},
	},
	{
		ID:          "opencode",
		Aliases:     []string{"open-code", "sst"},
		DisplayName: "OpenCode",
		Personal: Layout{
			Skills:       ".config/opencode/skills",
			Instructions: ".config/opencode/AGENTS.md",
			Commands:     ".config/opencode/command",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: "AGENTS.md",
			Commands:     ".opencode/command",
		},
		RuleFormat:     "block",
		CommandFormat:  "markdown",
		DetectPersonal: []string{".config/opencode"},
		DetectProject:  []string{".opencode"},
	},
	{
		ID:          "cline",
		Aliases:     []string{"roo", "roo-code"},
		DisplayName: "Cline",
		Personal: Layout{
			Skills:       ankraLibrary,
			Instructions: "Documents/Cline/Rules/ankra.md",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: ".clinerules/ankra.md",
			Commands:     ".clinerules/workflows",
		},
		RuleFormat:     "block",
		CommandFormat:  "markdown",
		DetectPersonal: []string{"Documents/Cline"},
		DetectProject:  []string{".clinerules"},
	},
	{
		ID:          "zed",
		Aliases:     []string{"zed-editor"},
		DisplayName: "Zed",
		Personal: Layout{
			Skills:       ankraLibrary,
			Instructions: ".config/zed/AGENTS.md",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: "AGENTS.md",
		},
		RuleFormat:     "block",
		DetectPersonal: []string{".config/zed"},
		DetectProject:  []string{".rules"},
	},
	{
		ID:          "openclaw",
		Aliases:     []string{"claw"},
		DisplayName: "OpenClaw",
		Personal: Layout{
			Skills:       ".openclaw/skills",
			Instructions: ".openclaw/AGENTS.md",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: "AGENTS.md",
		},
		RuleFormat:          "block",
		LoadsSkillsNatively: true,
		DetectPersonal:      []string{".openclaw"},
	},
	{
		ID:          "agents",
		Aliases:     []string{"agents-md", "agentsmd", "generic", "amp", "aider", "other"},
		DisplayName: "AGENTS.md (any other assistant)",
		Personal: Layout{
			Skills:       ankraLibrary,
			Instructions: ".ankra/AGENTS.md",
		},
		Project: Layout{
			Skills:       ankraLibrary,
			Instructions: "AGENTS.md",
		},
		RuleFormat: "block",
		Note: "AGENTS.md is read by Amp, Jules, Aider, Factory and most newer agents. " +
			"Point any other assistant at the skills directory named above.",
	},
}

// Clients returns every supported client, in registry order.
func Clients() []Client {
	out := make([]Client, len(clientRegistry))
	copy(out, clientRegistry)
	return out
}

// ClientIDs returns the canonical --client values, in registry order.
func ClientIDs() []string {
	ids := make([]string, 0, len(clientRegistry))
	for _, client := range clientRegistry {
		ids = append(ids, client.ID)
	}
	return ids
}

// LookupClient resolves an ID or alias, case- and space-insensitively.
func LookupClient(name string) (Client, error) {
	wanted := strings.ToLower(strings.TrimSpace(name))
	if wanted == "" {
		return Client{}, fmt.Errorf("no client given")
	}
	for _, client := range clientRegistry {
		if client.ID == wanted {
			return client, nil
		}
		for _, alias := range client.Aliases {
			if alias == wanted {
				return client, nil
			}
		}
	}
	return Client{}, fmt.Errorf("unsupported client %q (known: %s, plus \"all\" and \"auto\")",
		name, strings.Join(ClientIDs(), ", "))
}

// ResolveClients expands the --client values into a de-duplicated client
// list, in registry order. "all" selects every client; "auto" selects the
// clients whose configuration is present under root. An empty request
// defaults to auto, falling back to Claude Code and Cursor when nothing is
// detected, so a first run on a bare machine still installs something useful.
func ResolveClients(requested []string, root string, scope string) ([]Client, error) {
	if len(requested) == 0 {
		requested = []string{"auto"}
	}
	selected := make(map[string]bool)
	for _, name := range requested {
		for _, part := range strings.Split(name, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			switch part {
			case "":
				continue
			case "all":
				for _, client := range clientRegistry {
					selected[client.ID] = true
				}
			case "auto", "detect":
				for _, client := range DetectClients(root, scope) {
					selected[client.ID] = true
				}
			default:
				client, err := LookupClient(part)
				if err != nil {
					return nil, err
				}
				selected[client.ID] = true
			}
		}
	}
	if len(selected) == 0 {
		selected["claude-code"] = true
		selected["cursor"] = true
	}
	out := make([]Client, 0, len(selected))
	for _, client := range clientRegistry {
		if selected[client.ID] {
			out = append(out, client)
		}
	}
	return out, nil
}

// DetectClients reports which clients are configured under root for the given
// scope, judged by the presence of their own configuration paths.
func DetectClients(root string, scope string) []Client {
	found := make([]Client, 0, len(clientRegistry))
	for _, client := range clientRegistry {
		markers := client.DetectPersonal
		if scope == ScopeProject {
			markers = client.DetectProject
		}
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(marker))); err == nil {
				found = append(found, client)
				break
			}
		}
	}
	return found
}

// Scope names for a personal (home directory) or project install.
const (
	ScopePersonal = "personal"
	ScopeProject  = "project"
)

// Layout returns the client's layout for a scope.
func (c Client) Layout(scope string) Layout {
	if scope == ScopeProject {
		return c.Project
	}
	return c.Personal
}

// Target is one resolved installation: a client, a scope, and the absolute
// paths everything is written to.
type Target struct {
	Client Client
	Scope  string
	// Root is the scope root: the home directory or the project directory.
	Root string
	// SkillsDirectory is where the skill folders (or .zip bundles) land.
	SkillsDirectory string
	// InstructionsPath is the markdown file carrying the managed block; empty
	// when the client uses a rule mechanism instead.
	InstructionsPath string
	// CommandsDirectory is where workflow command files land; empty when the
	// client has no command mechanism.
	CommandsDirectory string
	// HooksPath is the hook config file; empty when the client has no hooks.
	HooksPath string
}

// ResolveTarget turns a client and scope into absolute paths under root.
func ResolveTarget(client Client, scope, root string) (Target, error) {
	if root == "" {
		return Target{}, fmt.Errorf("no root directory for a %s install", scope)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Target{}, err
	}
	layout := client.Layout(scope)
	if layout.Skills == "" {
		return Target{}, fmt.Errorf("%s has no %s install location", client.DisplayName, scope)
	}
	join := func(relative string) string {
		if relative == "" {
			return ""
		}
		return filepath.Join(absoluteRoot, filepath.FromSlash(relative))
	}
	return Target{
		Client:            client,
		Scope:             scope,
		Root:              absoluteRoot,
		SkillsDirectory:   join(layout.Skills),
		InstructionsPath:  join(layout.Instructions),
		CommandsDirectory: join(layout.Commands),
		HooksPath:         join(layout.Hooks),
	}, nil
}

// LoadsNatively reports whether this target's client discovers the SKILL.md
// files at SkillsDirectory by itself. A client that does so for its own
// directory still needs the index when the scope puts the skills in the
// client-neutral library instead.
func (t Target) LoadsNatively() bool {
	return t.Client.LoadsSkillsNatively &&
		t.Client.Layout(t.Scope).Skills != ankraLibrary
}

// SupportsHooks reports whether the guard hook can be installed for a target.
func (t Target) SupportsHooks() bool {
	return t.Client.HookFormat != "" && t.HooksPath != ""
}

// SupportsCommands reports whether workflow command files can be written.
func (t Target) SupportsCommands() bool {
	return t.Client.CommandFormat != "" && t.CommandsDirectory != ""
}

// DisplayPath shortens an absolute path for output, collapsing the user's
// home directory to ~ so install output stays readable.
func DisplayPath(path string) string {
	if path == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + string(os.PathSeparator) + path[len(home)+1:]
	}
	return path
}

// SortClientsByID orders clients alphabetically; used where output should not
// depend on registry order.
func SortClientsByID(clients []Client) {
	sort.Slice(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })
}
