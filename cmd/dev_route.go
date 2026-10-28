package cmd

// Which invocations of a shimmed tool run in the checkout's Ankra Workspace
// and which stay on this machine. A Go port of claude-tools
// remote-exec/shims/route (ankra-b5c3as.3, .9, .25), rule for rule: the
// classification is a contract with the resource guards, which ask it through
// ANKRA_EXEC_EXPLAIN. The one departure is gofmt reading stdin, which the
// workspace cannot see (devGofmtReadsStdin).

import "strings"

// The workspace families a shimmed tool belongs to. A family is not a
// workspace kind: devWorkspaceKind maps it onto one the repository has.
const (
	devFamilyGo   = "go"
	devFamilyNode = "node"
	devFamilyE2E  = "e2e"
)

// devShimTools are the tools `ankra dev install` shims, in install order.
var devShimTools = []string{"go", "gofmt", "golangci-lint", "pnpm", "npm", "npx"}

// devE2EFetchDirectories are what a Playwright run leaves behind, copied
// back whatever the exit code.
var devE2EFetchDirectories = []string{"test-results", "playwright-report", "e2e-report", "blob-report"}

// devE2EPrelude installs the browsers of the repository's own Playwright
// version (a no-op when cached) before the run.
const devE2EPrelude = `npx --no-install playwright install chromium webkit >/dev/null 2>&1 || true; exec "$@"`

// devDecision is where one invocation runs.
type devDecision struct {
	// IsRouted says it runs in the workspace; everything else is unset when
	// it does not.
	IsRouted bool
	// Family picks the workspace kind.
	Family string
	// IsApplying says the invocation rewrites the tree: its changes come
	// back as a patch (ankra exec --apply).
	IsApplying bool
	// FetchDirectories are copied back after the run (ankra exec --fetch).
	FetchDirectories []string
	// Argv is the command the workspace runs.
	Argv []string
}

// explain is the ANKRA_EXEC_EXPLAIN answer, in the bash shim's words.
func (decision devDecision) explain() string {
	switch {
	case !decision.IsRouted:
		return "local"
	case decision.Family == devFamilyE2E:
		return "route:playwright"
	default:
		return "route"
	}
}

// isDevShimTool reports whether tool is one the shims cover.
func isDevShimTool(tool string) bool {
	for _, known := range devShimTools {
		if known == tool {
			return true
		}
	}
	return false
}

// classifyDevInvocation decides where `tool arguments...` runs, the mode
// aside.
func classifyDevInvocation(tool string, arguments []string) devDecision {
	if !isDevShimTool(tool) {
		return devDecision{}
	}
	if devPlaywrightRun(tool, arguments) {
		argv := append([]string{"sh", "-c", devE2EPrelude, "devbox-e2e", tool}, arguments...)
		return devDecision{IsRouted: true, Family: devFamilyE2E,
			FetchDirectories: append([]string{}, devE2EFetchDirectories...), Argv: argv}
	}
	decision := devDecision{Argv: append([]string{tool}, arguments...)}
	switch tool {
	case "go":
		decision.Family = devFamilyGo
		decision.IsRouted = devRoutableGo(arguments)
		decision.IsApplying = devGoWrites(arguments)
	case "gofmt":
		decision.Family = devFamilyGo
		decision.IsRouted = !devGofmtReadsStdin(arguments)
		decision.IsApplying = devGofmtWrites(arguments)
	case "golangci-lint":
		decision.Family = devFamilyGo
		decision.IsRouted = devRoutableGolangci(arguments)
		decision.IsApplying = devGolangciWrites(arguments)
	case "pnpm":
		decision.Family = devFamilyNode
		decision.IsRouted = devRoutablePnpm(arguments)
	case "npm":
		decision.Family = devFamilyNode
		decision.IsRouted = devRoutableNpm(arguments)
	case "npx":
		decision.Family = devFamilyNode
		decision.IsRouted = devRoutableNpx(arguments)
	}
	if !decision.IsRouted {
		return devDecision{}
	}
	return decision
}

// devArgument answers the argument at index, "" past the end (bash ${N:-}).
func devArgument(arguments []string, index int) string {
	if index < len(arguments) {
		return arguments[index]
	}
	return ""
}

// devHasWord reports whether the invocation carries word as an argument of
// its own, the way the bash shim asks: case " $* " in *" word "*.
func devHasWord(arguments []string, words ...string) bool {
	joined := " " + strings.Join(arguments, " ") + " "
	for _, word := range words {
		if strings.Contains(joined, " "+word+" ") {
			return true
		}
	}
	return false
}

// devGoWrites reports whether a go invocation rewrites the tree: generate,
// fix, fmt, get, tool, and mod tidy|vendor|edit.
func devGoWrites(arguments []string) bool {
	subcommand, second := "", ""
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		if subcommand == "" {
			subcommand = argument
		} else if second == "" {
			second = argument
			break
		}
	}
	switch subcommand {
	case "generate", "fix", "fmt", "get", "tool":
		return true
	case "mod":
		return second == "tidy" || second == "vendor" || second == "edit"
	}
	return false
}

// devGoOutputFlag reports whether a flag after the subcommand names a file
// the invocation writes outside what a run brings back, or runs a program of
// this machine: those stay here.
func devGoOutputFlag(argument string) bool {
	switch argument {
	case "-c", "-o", "-coverprofile", "-trace", "-exec":
		return true
	}
	for _, prefix := range []string{"-o=", "-coverprofile=", "-cpuprofile", "-memprofile", "-blockprofile",
		"-mutexprofile", "-trace=", "-outputdir", "-exec="} {
		if strings.HasPrefix(argument, prefix) {
			return true
		}
	}
	return false
}

// devRoutableGo: go test, vet and build route, and so do the writers, unless
// the invocation changes directory (-C) or writes an output file.
func devRoutableGo(arguments []string) bool {
	subcommand := ""
	for _, argument := range arguments {
		if argument == "-C" || strings.HasPrefix(argument, "-C=") {
			return false
		}
		if subcommand == "" {
			if !strings.HasPrefix(argument, "-") {
				subcommand = argument
			}
			continue
		}
		if devGoOutputFlag(argument) {
			return false
		}
	}
	switch subcommand {
	case "test", "vet", "build":
		return true
	}
	return devGoWrites(arguments)
}

// devRoutablePnpm: the heavy read-only scripts (typecheck, test, lint,
// format-check, knip), a vitest run and tsc --noEmit. Anything that fixes,
// writes, watches or serves stays here, and so does every other script.
func devRoutablePnpm(arguments []string) bool {
	first, second := devArgument(arguments, 0), devArgument(arguments, 1)
	if first == "run" {
		first, second = second, devArgument(arguments, 2)
	}
	if first == "" || strings.HasPrefix(first, "-") {
		return false
	}
	for _, fragment := range []string{"fix", "write", "e2e", "playwright", "watch", "dev", "storybook"} {
		if strings.Contains(first, fragment) {
			return false
		}
	}
	switch first {
	case "typecheck", "test", "test:ci", "test:unit", "test:shard", "lint", "format-check", "knip":
		return true
	case "vitest":
		return devHasWord(arguments, "run", "--run")
	case "exec":
		switch second {
		case "vitest":
			return devHasWord(arguments, "run", "--run")
		case "tsc":
			return devHasWord(arguments, "--noEmit")
		}
		return false
	case "tsc":
		return devHasWord(arguments, "--noEmit")
	}
	for _, prefix := range []string{"typecheck:", "test:shard:", "lint:"} {
		if strings.HasPrefix(first, prefix) {
			return true
		}
	}
	return false
}

// devRoutableNpm: the same script names as pnpm (npm run typecheck, npm
// test, npm t).
func devRoutableNpm(arguments []string) bool {
	if first := devArgument(arguments, 0); first == "t" || first == "test" {
		return true
	}
	return devRoutablePnpm(arguments)
}

// devRoutableNpx: tsc --noEmit and a vitest run; every other npx stays here.
func devRoutableNpx(arguments []string) bool {
	switch devArgument(arguments, 0) {
	case "tsc":
		return devHasWord(arguments, "--noEmit")
	case "vitest":
		return devHasWord(arguments, "run", "--run")
	}
	return false
}

// devPlaywrightRun reports a headless Playwright test run: pnpm|npm [run]
// test:e2e*, e2e*, [exec] playwright, and npx playwright test. The
// interactive modes (--ui, --debug, --headed, codegen, show-report, a *:ui
// script) and a browser install stay here.
func devPlaywrightRun(tool string, arguments []string) bool {
	if devHasWord(arguments, "--ui", "--debug", "--headed", "codegen", "show-report", "install") {
		return false
	}
	for _, argument := range arguments {
		if strings.HasSuffix(argument, ":ui") {
			return false
		}
	}
	first, second := devArgument(arguments, 0), devArgument(arguments, 1)
	switch tool {
	case "pnpm", "npm":
		if first == "run" {
			first, second = second, devArgument(arguments, 2)
		}
		switch {
		case first == "test:e2e", first == "e2e", strings.HasPrefix(first, "test:e2e:"),
			strings.HasPrefix(first, "e2e:"):
			return true
		case first == "playwright":
			return true
		case first == "exec":
			return second == "playwright"
		}
	case "npx":
		return first == "playwright" && second == "test"
	}
	return false
}

// devGofmtWrites reports gofmt -w, which rewrites files.
func devGofmtWrites(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "-w" {
			return true
		}
	}
	return false
}

// devGofmtReadsStdin reports a gofmt invocation that names no file or
// directory and so formats standard input (an editor's format-on-save). The
// workspace never sees this machine's stdin, so routing it would answer
// nothing with exit 0; it stays here. The bash shim routed every gofmt.
func devGofmtReadsStdin(arguments []string) bool {
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--":
			return index+1 >= len(arguments)
		case argument == "-r" || argument == "--r" || argument == "-cpuprofile" || argument == "--cpuprofile":
			// The flag's value is the next argument, not a path.
			index++
		case strings.HasPrefix(argument, "-"):
			// A flag, or "-", which names standard input too.
		default:
			return false
		}
	}
	return true
}

// devRoutableGolangci: golangci-lint run and fmt.
func devRoutableGolangci(arguments []string) bool {
	first := devArgument(arguments, 0)
	return first == "run" || first == "fmt"
}

// devGolangciWrites reports golangci-lint fmt and run --fix, which rewrite
// files.
func devGolangciWrites(arguments []string) bool {
	if devArgument(arguments, 0) == "fmt" {
		return true
	}
	joined := " " + strings.Join(arguments, " ") + " "
	return strings.Contains(joined, " --fix ") || strings.Contains(joined, " --fix=")
}
