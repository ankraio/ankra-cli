package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// ProtocolVersion is the module wire protocol this CLI speaks. An external
// module reports the version it implements in its description; the registry
// refuses one it does not understand rather than guessing at the shape of
// its output.
const ProtocolVersion = 1

// The three verbs an external module must answer, and the one it may. Each
// is invoked as `ankra-module-<name> <verb>` with a JSON request on stdin and
// a JSON response expected on stdout; anything on stderr is relayed as
// diagnostics.
const (
	VerbDescribe = "describe"
	VerbDetect   = "detect"
	VerbConvert  = "convert"
	VerbExport   = "export"
)

// Time limits per verb. Describe and detect look at a directory; convert may
// legitimately read a large tree or shell out; export dumps databases of
// whatever size the source holds.
const (
	describeTimeout = 15 * time.Second
	detectTimeout   = 30 * time.Second
	convertTimeout  = 5 * time.Minute
	exportTimeout   = 6 * time.Hour
)

// detectRequest is the stdin payload for the detect verb.
type detectRequest struct {
	Dir string `json:"dir"`
}

type externalModule struct {
	path        string
	description Description
}

// loadExternal runs the describe verb once and keeps the answer, so Describe
// never has to start a process.
func loadExternal(ctx context.Context, path string) (Module, error) {
	ctx, cancel := context.WithTimeout(ctx, describeTimeout)
	defer cancel()

	var description Description
	if err := runModule(ctx, path, VerbDescribe, nil, &description, nil); err != nil {
		return nil, err
	}
	if description.Name == "" {
		return nil, fmt.Errorf("describe returned no name")
	}
	if description.Protocol != ProtocolVersion {
		return nil, fmt.Errorf("speaks module protocol %d, this CLI speaks %d", description.Protocol, ProtocolVersion)
	}
	description.Path = path
	description.Builtin = false
	return &externalModule{path: path, description: description}, nil
}

func (m *externalModule) Describe() Description {
	return m.description
}

func (m *externalModule) Detect(ctx context.Context, dir string) (Detection, error) {
	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()
	var detection Detection
	err := runModule(ctx, m.path, VerbDetect, detectRequest{Dir: dir}, &detection, nil)
	return detection, err
}

func (m *externalModule) Convert(ctx context.Context, request ConvertRequest) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()
	var result Result
	err := runModule(ctx, m.path, VerbConvert, request, &result, nil)
	return result, err
}

// Export implements DataExporter. The module's stderr is relayed live to the
// request's progress writer: a dump can run for minutes, and a module that
// narrates what it is doing must be heard while it does it, not after.
func (m *externalModule) Export(ctx context.Context, request ExportRequest) (Export, error) {
	ctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	var export Export
	err := runModule(ctx, m.path, VerbExport, request, &export, request.Progress)
	return export, err
}

// runModule invokes one verb and decodes its stdout. A non-zero exit carries
// the module's stderr, which is where a well-behaved module explains itself;
// when progress is set, stderr is also relayed to it as it arrives.
func runModule(ctx context.Context, path, verb string, input, output interface{}, progress io.Writer) error {
	command := exec.CommandContext(ctx, path, verb)
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		command.Stdin = bytes.NewReader(payload)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if progress != nil {
		command.Stderr = io.MultiWriter(&stderr, progress)
	}

	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s timed out", verb)
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%s failed: %s", verb, lastLines(message, 5))
	}
	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		return fmt.Errorf("%s returned output that is not the expected JSON: %w", verb, err)
	}
	return nil
}

func lastLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}
