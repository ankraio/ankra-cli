package docker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FindDockerfile reports the Dockerfile in dir, if any.
func FindDockerfile(dir string) (string, bool) {
	path := filepath.Join(dir, "Dockerfile")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, true
	}
	return "", false
}

// DockerfileOptions control how a bare Dockerfile is read.
type DockerfileOptions struct {
	// Name is the workload name; defaults to the directory name.
	Name string
	// Image is where the built image will live. Without it the conversion
	// can only guess, and the guess has to be replaced before deploying.
	Image string
}

type dockerfileStage struct {
	name         string
	from         string
	instructions []dockerfileInstruction
}

type dockerfileInstruction struct {
	keyword   string
	arguments string
}

// LoadDockerfile reads a Dockerfile into a one-workload Project. Only what
// the image declares about running itself is read - exposed ports, the
// healthcheck, and volumes. ENV, CMD and ENTRYPOINT are left to the image:
// restating them in a manifest would duplicate what it already carries and,
// for shell-form CMD, change its meaning.
func LoadDockerfile(dir, path string, options DockerfileOptions) (Project, []string, error) {
	stages, err := parseDockerfile(path)
	if err != nil {
		return Project{}, nil, err
	}
	if len(stages) == 0 {
		return Project{}, nil, fmt.Errorf("%s has no FROM instruction", filepath.Base(path))
	}

	name := options.Name
	if name == "" {
		name = sanitiseName(filepath.Base(dir))
	}

	workload := Workload{
		Name:  name,
		Image: options.Image,
		Build: &Build{Context: ".", Dockerfile: filepath.Base(path)},
	}
	var warnings []string

	for _, instruction := range effectiveInstructions(stages, len(stages)-1) {
		switch instruction.keyword {
		case "EXPOSE":
			for _, token := range strings.Fields(instruction.arguments) {
				spec, protocol, found := strings.Cut(token, "/")
				if !found {
					protocol = "tcp"
				}
				container, err := strconv.Atoi(spec)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("%s: EXPOSE %q could not be read", name, token))
					continue
				}
				workload.Ports = append(workload.Ports, Port{Container: container, Protocol: protocol})
			}
		case "VOLUME":
			for _, target := range dockerfileList(instruction.arguments) {
				workload.Volumes = append(workload.Volumes, Volume{
					Name:   sanitiseName(strings.Trim(target, "/")),
					Source: target,
					Target: target,
					Named:  true,
				})
				workload.Stateful = true
			}
		case "HEALTHCHECK":
			if health, ok := dockerfileHealthcheck(instruction.arguments); ok {
				workload.Healthcheck = &health
			}
		}
	}

	project := Project{
		Name:      name,
		Workloads: []Workload{workload},
		Source:    filepath.Base(path),
	}
	for _, volume := range workload.Volumes {
		project.Volumes = append(project.Volumes, volume.Name)
	}
	return project, warnings, nil
}

var dockerfileFromPattern = regexp.MustCompile(`(?i)^(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?`)

func parseDockerfile(path string) ([]dockerfileStage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var stages []dockerfileStage
	var pending string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			pending += strings.TrimSuffix(line, "\\") + " "
			continue
		}
		line = pending + line
		pending = ""

		keyword, arguments, _ := strings.Cut(line, " ")
		keyword = strings.ToUpper(keyword)
		arguments = strings.TrimSpace(arguments)

		if keyword == "FROM" {
			match := dockerfileFromPattern.FindStringSubmatch(arguments)
			stage := dockerfileStage{}
			if match != nil {
				stage.from = match[1]
				stage.name = strings.ToLower(match[2])
			}
			stages = append(stages, stage)
			continue
		}
		if len(stages) == 0 {
			continue
		}
		stages[len(stages)-1].instructions = append(stages[len(stages)-1].instructions, dockerfileInstruction{keyword: keyword, arguments: arguments})
	}
	return stages, scanner.Err()
}

// effectiveInstructions returns a stage's instructions preceded by those it
// inherits from earlier stages in the same file, so a final `FROM base AS
// runtime` sees the EXPOSE that base declared.
func effectiveInstructions(stages []dockerfileStage, index int) []dockerfileInstruction {
	stage := stages[index]
	for parent := index - 1; parent >= 0; parent-- {
		if stages[parent].name != "" && strings.EqualFold(stages[parent].name, stage.from) {
			return append(effectiveInstructions(stages, parent), stage.instructions...)
		}
	}
	return stage.instructions
}

// dockerfileList reads an instruction argument that may be a JSON array or
// whitespace-separated words.
func dockerfileList(arguments string) []string {
	if strings.HasPrefix(arguments, "[") {
		var items []string
		if err := json.Unmarshal([]byte(arguments), &items); err == nil {
			return items
		}
	}
	return strings.Fields(arguments)
}

var healthcheckFlagPattern = regexp.MustCompile(`--([a-z-]+)=(\S+)`)

func dockerfileHealthcheck(arguments string) (Healthcheck, bool) {
	if strings.EqualFold(strings.TrimSpace(arguments), "NONE") {
		return Healthcheck{}, false
	}
	flags, command, found := strings.Cut(arguments, "CMD")
	if !found {
		return Healthcheck{}, false
	}
	health := Healthcheck{}
	for _, match := range healthcheckFlagPattern.FindAllStringSubmatch(flags, -1) {
		switch match[1] {
		case "interval":
			health.IntervalSec = dockerfileSeconds(match[2])
		case "timeout":
			health.TimeoutSec = dockerfileSeconds(match[2])
		case "start-period":
			health.StartSec = dockerfileSeconds(match[2])
		case "retries":
			health.Retries, _ = strconv.Atoi(match[2])
		}
	}
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, "[") {
		health.Test = append([]string{"CMD"}, dockerfileList(command)...)
	} else {
		health.Test = []string{"CMD-SHELL", command}
	}
	return health, len(health.Test) > 1
}

func dockerfileSeconds(value string) int {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return int(duration.Seconds())
}

var invalidNamePattern = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitiseName makes a string usable as a Kubernetes resource name.
func sanitiseName(value string) string {
	value = invalidNamePattern.ReplaceAllString(strings.ToLower(value), "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "app"
	}
	return value
}
