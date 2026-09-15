package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// InstallOptions are the choices `ankra skills install` takes for one client
// beyond the skills themselves: whether the always-applied rule (and skill
// index), the workflow commands, and the guard hook were installed. They are
// recorded per client so that `ankra upgrade`, which reinstalls the skills
// through the new binary, replays exactly what the person chose instead of
// the defaults.
type InstallOptions struct {
	Rules     bool `json:"rules"`
	Workflows bool `json:"workflows"`
	Hooks     bool `json:"hooks"`
}

// DefaultInstallOptions is what a plain `ankra skills install` does: rule and
// workflows on, the hook off. An install made before the options were
// recorded refreshes with these, which is what the refresh always did.
func DefaultInstallOptions() InstallOptions {
	return InstallOptions{Rules: true, Workflows: true, Hooks: false}
}

// installRecordFile is where the per-client options live, relative to the
// scope root. It sits beside the client-neutral skills library under .ankra
// rather than inside any one client's directory, because several clients
// share that library and the file must be keyed by client, not by path.
const installRecordFile = ".ankra/skills-install.json"

// installRecord is the on-disk shape: one entry per client ID.
type installRecord struct {
	Clients map[string]InstallOptions `json:"clients"`
}

// InstallRecordPath is the absolute path of the install record under root.
func InstallRecordPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(installRecordFile))
}

// readInstallRecord loads the record under root; a missing file is an empty
// record, an unreadable or malformed one is an error, because "nothing
// recorded" and "could not read what was recorded" call for different
// answers.
func readInstallRecord(root string) (installRecord, error) {
	record := installRecord{Clients: map[string]InstallOptions{}}
	path := InstallRecordPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return record, nil
		}
		return record, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("%s is not valid JSON (%w); fix or remove it and retry", path, err)
	}
	if record.Clients == nil {
		record.Clients = map[string]InstallOptions{}
	}
	return record, nil
}

func writeInstallRecord(root string, record installRecord) error {
	path := InstallRecordPath(root)
	if len(record.Clients) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// RecordInstallOptions stores the options an install used for one client,
// replacing whatever was recorded for it before.
func RecordInstallOptions(root, clientID string, options InstallOptions) error {
	record, err := readInstallRecord(root)
	if err != nil {
		return err
	}
	record.Clients[clientID] = options
	return writeInstallRecord(root, record)
}

// ForgetInstallOptions drops the recorded options for one client, for a full
// uninstall. A client with nothing recorded is not an error.
func ForgetInstallOptions(root, clientID string) error {
	record, err := readInstallRecord(root)
	if err != nil {
		return err
	}
	if _, found := record.Clients[clientID]; !found {
		return nil
	}
	delete(record.Clients, clientID)
	return writeInstallRecord(root, record)
}

// RecordedInstallOptions returns the options recorded for one client and
// whether anything was recorded at all. A client with no record (an install
// made by a release that did not write one) reports found=false and the
// defaults, so a caller can replay them without a special case.
func RecordedInstallOptions(root, clientID string) (InstallOptions, bool, error) {
	record, err := readInstallRecord(root)
	if err != nil {
		return DefaultInstallOptions(), false, err
	}
	options, found := record.Clients[clientID]
	if !found {
		return DefaultInstallOptions(), false, nil
	}
	return options, true, nil
}
