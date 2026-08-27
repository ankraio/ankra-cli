package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ExternalModulePrefix is the executable name prefix the registry looks for:
// an `ankra-module-heroku` on PATH is the `heroku` module.
const ExternalModulePrefix = "ankra-module-"

// Registry holds the built-in modules and discovers external ones.
type Registry struct {
	builtins []Module
	// searchDirs returns the directories scanned for external modules, in
	// priority order. Tests replace it to avoid the real PATH.
	searchDirs func() []string
	// load turns an executable path into a module. Tests replace it to
	// avoid running processes.
	load func(ctx context.Context, path string) (Module, error)
}

// NewRegistry builds a registry over the given built-in modules.
func NewRegistry(builtins ...Module) *Registry {
	return &Registry{
		builtins:   builtins,
		searchDirs: defaultSearchDirs,
		load:       loadExternal,
	}
}

// defaultSearchDirs is ~/.ankra/modules followed by PATH, so a user can
// install a module without touching their PATH.
func defaultSearchDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".ankra", "modules"))
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// Modules returns every usable module, built-ins first. Externals that fail
// to load are reported as notes rather than errors: one broken module on
// PATH must not take the whole command down.
func (r *Registry) Modules(ctx context.Context) ([]Module, []string) {
	modules := append([]Module(nil), r.builtins...)
	seen := map[string]bool{}
	for _, module := range r.builtins {
		seen[module.Describe().Name] = true
	}

	var notes []string
	for _, dir := range r.searchDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name, ok := externalModuleName(entry.Name())
			if !ok || seen[name] {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if !isExecutable(path) {
				continue
			}
			module, err := r.load(ctx, path)
			if err != nil {
				notes = append(notes, fmt.Sprintf("skipped %s: %v", path, err))
				continue
			}
			seen[name] = true
			modules = append(modules, module)
		}
	}
	return modules, notes
}

// Lookup finds a module by name.
func (r *Registry) Lookup(ctx context.Context, name string) (Module, error) {
	modules, _ := r.Modules(ctx)
	for _, module := range modules {
		if module.Describe().Name == name {
			return module, nil
		}
	}
	return nil, fmt.Errorf("no module named %q (run `ankra migrate modules` to list them)", name)
}

// Candidate is one module's verdict on a directory.
type Candidate struct {
	Module    Module
	Detection Detection
	Err       error
}

// Detect asks every module about dir and returns the answers, most confident
// first. Modules that errored come last with Err set.
func (r *Registry) Detect(ctx context.Context, dir string) ([]Candidate, []string) {
	modules, notes := r.Modules(ctx)
	candidates := make([]Candidate, 0, len(modules))
	for _, module := range modules {
		detection, err := module.Detect(ctx, dir)
		candidates = append(candidates, Candidate{Module: module, Detection: detection, Err: err})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if (candidates[i].Err == nil) != (candidates[j].Err == nil) {
			return candidates[i].Err == nil
		}
		if candidates[i].Detection.Confidence != candidates[j].Detection.Confidence {
			return candidates[i].Detection.Confidence > candidates[j].Detection.Confidence
		}
		return candidates[i].Module.Describe().Name < candidates[j].Module.Describe().Name
	})
	return candidates, notes
}

func externalModuleName(fileName string) (string, bool) {
	if !strings.HasPrefix(fileName, ExternalModulePrefix) {
		return "", false
	}
	name := strings.TrimPrefix(fileName, ExternalModulePrefix)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, ".exe")
	}
	if name == "" {
		return "", false
	}
	return name, true
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// Validate checks a conversion result before anything is written: file paths
// must stay inside the output directory, and every manifest must point at a
// file the result actually contains. Built-in modules get the same check as
// external ones, so a bug in either surfaces as a clean error rather than a
// stray write.
func Validate(result Result) error {
	for file := range result.Files {
		if filepath.IsAbs(file) || strings.HasPrefix(file, "..") || strings.Contains(file, "/../") || strings.Contains(file, `\`) {
			return fmt.Errorf("module returned an unsafe file path %q", file)
		}
	}
	for _, stack := range result.Cluster.Spec.Stacks {
		for _, manifest := range stack.Manifests {
			if manifest.FromFile == "" {
				continue
			}
			if _, ok := result.Files[manifest.FromFile]; !ok {
				return fmt.Errorf("manifest %q refers to %s, which the module did not return", manifest.Name, manifest.FromFile)
			}
		}
	}
	if result.Cluster.Metadata.Name == "" {
		return fmt.Errorf("module returned a cluster with no name")
	}
	return nil
}
