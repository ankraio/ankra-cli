package cmd

import (
	"strings"
	"testing"
)

// The classification cases of claude-tools remote-exec/test.sh section 11,
// which the bash shim answers through ANKRA_EXEC_EXPLAIN, plus what each
// routed case carries (family, --apply, --fetch).
func TestClassifyDevInvocationMatchesTheBashShim(t *testing.T) {
	cases := []struct {
		name        string
		command     string
		wantExplain string
		wantFamily  string
		wantApply   bool
	}{
		{"go test ./... routes", "go test ./...", "route", devFamilyGo, false},
		{"go vet ./... routes", "go vet ./...", "route", devFamilyGo, false},
		{"go build ./... routes", "go build ./...", "route", devFamilyGo, false},
		{"go build -o bin/x . stays local", "go build -o bin/x .", "local", "", false},
		{"go build -o=bin/x stays local", "go build -o=bin/x .", "local", "", false},
		{"go test -c stays local", "go test -c ./pkg", "local", "", false},
		{"go test -coverprofile=c.out stays local", "go test -coverprofile=c.out ./...", "local", "", false},
		{"go test -coverprofile c.out stays local", "go test -coverprofile c.out ./...", "local", "", false},
		{"go test -cpuprofile stays local", "go test -cpuprofile=cpu.out ./x", "local", "", false},
		{"go test -exec stays local", "go test -exec sudo ./x", "local", "", false},
		{"go test -trace stays local", "go test -trace t.out ./x", "local", "", false},
		{"a flag before the subcommand is not an output flag", "go -x test ./...", "route", devFamilyGo, false},
		{"go test -run X -count=1 routes", "go test -run X -count=1 ./...", "route", devFamilyGo, false},
		{"go mod tidy routes (writer, applied back)", "go mod tidy", "route", devFamilyGo, true},
		{"go mod vendor routes (writer)", "go mod vendor", "route", devFamilyGo, true},
		{"go mod edit routes (writer)", "go mod edit -go=1.26", "route", devFamilyGo, true},
		{"go mod download stays local", "go mod download", "local", "", false},
		{"go mod why stays local", "go mod why x", "local", "", false},
		{"go generate routes (writer)", "go generate ./...", "route", devFamilyGo, true},
		{"go tool sqlc routes (writer)", "go tool sqlc generate", "route", devFamilyGo, true},
		{"go get routes (writer)", "go get example.com/x@v1", "route", devFamilyGo, true},
		{"go fix routes (writer)", "go fix ./...", "route", devFamilyGo, true},
		{"go fmt routes (writer)", "go fmt ./...", "route", devFamilyGo, true},
		{"go run stays local", "go run ./cmd/x", "local", "", false},
		{"go install stays local", "go install ./cmd/x", "local", "", false},
		{"go env stays local", "go env GOPATH", "local", "", false},
		{"go version stays local", "go version", "local", "", false},
		{"bare go stays local", "go", "local", "", false},
		{"go -C dir test stays local", "go -C sub test ./...", "local", "", false},
		{"go -C=dir test stays local", "go -C=sub test ./...", "local", "", false},
		{"go generate -C dir stays local", "go -C sub generate ./...", "local", "", false},
		{"pnpm typecheck routes", "pnpm typecheck", "route", devFamilyNode, false},
		{"pnpm typecheck:strict routes", "pnpm typecheck:strict", "route", devFamilyNode, false},
		{"pnpm test:ci -- --coverage.changed routes", "pnpm test:ci -- --coverage.changed origin/main", "route",
			devFamilyNode, false},
		{"pnpm test routes", "pnpm test", "route", devFamilyNode, false},
		{"pnpm test:unit routes", "pnpm test:unit", "route", devFamilyNode, false},
		{"pnpm test:shard:2 routes", "pnpm test:shard:2", "route", devFamilyNode, false},
		{"pnpm format-check routes", "pnpm format-check", "route", devFamilyNode, false},
		{"pnpm knip routes", "pnpm knip", "route", devFamilyNode, false},
		{"pnpm vitest run --changed routes", "pnpm vitest run --changed", "route", devFamilyNode, false},
		{"pnpm vitest (watch) stays local", "pnpm vitest", "local", "", false},
		{"pnpm vitest --run routes", "pnpm vitest --run", "route", devFamilyNode, false},
		{"pnpm run lint routes", "pnpm run lint", "route", devFamilyNode, false},
		{"pnpm lint:css routes", "pnpm lint:css", "route", devFamilyNode, false},
		{"pnpm lint:fix stays local", "pnpm lint:fix", "local", "", false},
		{"pnpm format:write stays local", "pnpm format:write", "local", "", false},
		{"pnpm test:watch stays local", "pnpm test:watch", "local", "", false},
		{"pnpm dev stays local", "pnpm dev", "local", "", false},
		{"pnpm storybook stays local", "pnpm storybook", "local", "", false},
		{"pnpm lint-staged stays local", "pnpm lint-staged", "local", "", false},
		{"pnpm install stays local", "pnpm install", "local", "", false},
		{"pnpm add stays local", "pnpm add react", "local", "", false},
		{"pnpm build stays local", "pnpm build", "local", "", false},
		{"pnpm with a leading flag stays local", "pnpm -r test", "local", "", false},
		{"bare pnpm stays local", "pnpm", "local", "", false},
		{"pnpm exec tsc --noEmit routes", "pnpm exec tsc --noEmit", "route", devFamilyNode, false},
		{"pnpm exec tsc (emitting) stays local", "pnpm exec tsc", "local", "", false},
		{"pnpm tsc --noEmit routes", "pnpm tsc --noEmit -p .", "route", devFamilyNode, false},
		{"pnpm exec vitest run routes", "pnpm exec vitest run", "route", devFamilyNode, false},
		{"pnpm exec eslint stays local", "pnpm exec eslint .", "local", "", false},
		{"pnpm test:e2e routes to the playwright workspace", "pnpm test:e2e", "route:playwright", devFamilyE2E, false},
		{"pnpm run test:e2e:chromium routes (playwright)", "pnpm run test:e2e:chromium", "route:playwright",
			devFamilyE2E, false},
		{"pnpm e2e routes (playwright)", "pnpm e2e", "route:playwright", devFamilyE2E, false},
		{"npx playwright test routes to the playwright workspace", "npx playwright test tests/a.spec.ts",
			"route:playwright", devFamilyE2E, false},
		{"pnpm exec playwright test routes (playwright)", "pnpm exec playwright test", "route:playwright",
			devFamilyE2E, false},
		{"pnpm playwright test routes (playwright)", "pnpm playwright test", "route:playwright", devFamilyE2E, false},
		{"npm run e2e routes (playwright)", "npm run e2e", "route:playwright", devFamilyE2E, false},
		{"npx playwright test --ui stays local", "npx playwright test --ui", "local", "", false},
		{"npx playwright test --debug stays local", "npx playwright test --debug", "local", "", false},
		{"npx playwright test --headed stays local", "npx playwright test --headed", "local", "", false},
		{"pnpm e2e:hermetic:ui stays local", "pnpm e2e:hermetic:ui", "local", "", false},
		{"npx playwright codegen stays local", "npx playwright codegen", "local", "", false},
		{"npx playwright show-report stays local", "npx playwright show-report", "local", "", false},
		{"npx playwright install stays local", "npx playwright install chromium", "local", "", false},
		{"npm test routes", "npm test", "route", devFamilyNode, false},
		{"npm t routes", "npm t", "route", devFamilyNode, false},
		{"npm run typecheck routes", "npm run typecheck", "route", devFamilyNode, false},
		{"npm install stays local", "npm install", "local", "", false},
		{"npm ci stays local", "npm ci", "local", "", false},
		{"npx tsc --noEmit routes", "npx tsc --noEmit", "route", devFamilyNode, false},
		{"npx tsc (emitting) stays local", "npx tsc", "local", "", false},
		{"npx vitest run routes", "npx vitest run src/x.test.ts", "route", devFamilyNode, false},
		{"npx vitest (watch) stays local", "npx vitest", "local", "", false},
		{"npx eslint . stays local", "npx eslint .", "local", "", false},
		{"gofmt -l routes", "gofmt -l .", "route", devFamilyGo, false},
		{"gofmt -d routes", "gofmt -d ./cmd", "route", devFamilyGo, false},
		{"gofmt -w routes (with --apply)", "gofmt -w x.go", "route", devFamilyGo, true},
		{"gofmt -s -w routes (with --apply)", "gofmt -s -w ./cmd", "route", devFamilyGo, true},
		{"gofmt on stdin stays local", "gofmt", "local", "", false},
		{"gofmt -s on stdin stays local", "gofmt -s", "local", "", false},
		{"gofmt -r rule on stdin stays local", "gofmt -r a->b", "local", "", false},
		{"gofmt -r rule on a path routes", "gofmt -r a->b -l x.go", "route", devFamilyGo, false},
		{"gofmt - (stdin) stays local", "gofmt -", "local", "", false},
		{"golangci-lint run routes", "golangci-lint run ./...", "route", devFamilyGo, false},
		{"golangci-lint run --fix routes (writer)", "golangci-lint run --fix", "route", devFamilyGo, true},
		{"golangci-lint run --fix=true routes (writer)", "golangci-lint run --fix=true ./...", "route", devFamilyGo,
			true},
		{"golangci-lint fmt routes (writer)", "golangci-lint fmt", "route", devFamilyGo, true},
		{"golangci-lint version stays local", "golangci-lint version", "local", "", false},
		{"golangci-lint linters stays local", "golangci-lint linters", "local", "", false},
		{"an unshimmed tool stays local", "cargo test", "local", "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			words := strings.Fields(testCase.command)
			decision := classifyDevInvocation(words[0], words[1:])
			if got := decision.explain(); got != testCase.wantExplain {
				t.Fatalf("explain = %q, want %q", got, testCase.wantExplain)
			}
			if decision.Family != testCase.wantFamily || decision.IsApplying != testCase.wantApply {
				t.Fatalf("decision = %+v, want family %q apply %t", decision, testCase.wantFamily, testCase.wantApply)
			}
			if !decision.IsRouted {
				return
			}
			if testCase.wantFamily == devFamilyE2E {
				return
			}
			if strings.Join(decision.Argv, " ") != testCase.command || len(decision.FetchDirectories) != 0 {
				t.Fatalf("argv %q fetch %v, want the invocation itself and nothing fetched", decision.Argv,
					decision.FetchDirectories)
			}
		})
	}
}

func TestClassifyDevInvocationPlaywrightRunInstallsBrowsersAndFetchesResults(t *testing.T) {
	decision := classifyDevInvocation("pnpm", []string{"test:e2e", "--project", "chromium"})
	want := []string{"sh", "-c", devE2EPrelude, "devbox-e2e", "pnpm", "test:e2e", "--project", "chromium"}
	if strings.Join(decision.Argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q, want %q", decision.Argv, want)
	}
	if strings.Join(decision.FetchDirectories, " ") != "test-results playwright-report e2e-report blob-report" {
		t.Fatalf("fetch = %v", decision.FetchDirectories)
	}
	if decision.IsApplying {
		t.Fatal("a Playwright run applies nothing")
	}
	// The fetch list is a copy: one decision must not change the next.
	decision.FetchDirectories[0] = "x"
	if devE2EFetchDirectories[0] != "test-results" {
		t.Fatal("a decision shares the package's fetch list")
	}
}

func TestDevShimToolsAreTheInstalledSet(t *testing.T) {
	if strings.Join(devShimTools, " ") != "go gofmt golangci-lint pnpm npm npx" {
		t.Fatalf("shim tools = %v", devShimTools)
	}
	for _, tool := range devShimTools {
		if !isDevShimTool(tool) {
			t.Fatalf("%s is not recognised", tool)
		}
	}
	if isDevShimTool("node") || isDevShimTool("") {
		t.Fatal("an unshimmed tool is recognised")
	}
}
