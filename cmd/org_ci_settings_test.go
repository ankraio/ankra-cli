package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// orgCISettingsMock captures the reads and writes, keeping the exact change
// map so the present-vs-absent contract can be asserted: the endpoint
// distinguishes "not in the body" from "null", and a set that quietly restated
// a neighbouring setting would clobber it.
type orgCISettingsMock struct {
	baseMock

	settings   client.OrganisationCISettings
	updateSeen []map[string]any
	clusters   []client.ClusterListItem
}

func (m *orgCISettingsMock) GetOrganisationCISettings(
	ctx context.Context) (*client.OrganisationCISettings, error) {
	settings := m.settings
	return &settings, nil
}

func (m *orgCISettingsMock) UpdateOrganisationCISettings(ctx context.Context,
	changes map[string]any) (*client.OrganisationCISettings, error) {
	m.updateSeen = append(m.updateSeen, changes)
	settings := m.settings
	return &settings, nil
}

func (m *orgCISettingsMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	response := &client.ClusterListResponse{Result: m.clusters}
	response.Pagination.TotalPages = 1
	return response, nil
}

func resetOrgCISettingsFlags(t *testing.T) {
	t.Helper()
	for _, command := range []*cobra.Command{orgCISettingsGetCmd, orgCISettingsSetCmd} {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			if sliceValue, isSlice := flag.Value.(pflag.SliceValue); isSlice {
				_ = sliceValue.Replace(nil)
			}
			flag.Changed = false
		})
	}
}

func runOrgCISettings(t *testing.T, mock *orgCISettingsMock, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetOrgCISettingsFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs(args)
	executeError := rootCmd.Execute()
	return output.String(), executeError
}

func defaultCISettings() client.OrganisationCISettings {
	return client.OrganisationCISettings{
		BuildFallback:         client.CIBuildFallbackPlatformBuilders,
		MaxParallelRuns:       4,
		MaxParallelSteps:      8,
		ArtifactRetentionDays: 30,
		CacheRetentionDays:    14,
		ImageGate:             client.CIImageGateApplicationDependencies,
		IsDefault:             true,
	}
}

// The gap this command exists to close: the settings that decide whether a
// pipeline can start were unreadable from the CLI, so an operator whose runs
// all failed had no way to see what the build fallback was set to (PLA-825).
func TestRunOrgCISettingsGet_ShowsTheBuildFallbackAndPipelineCluster(t *testing.T) {
	clusterName := "launch-week-2026"
	clusterID := "4b1f0f8e-9c1a-4c2f-9e6f-2a1d8b3c4d5e"
	mock := &orgCISettingsMock{settings: client.OrganisationCISettings{
		ClusterID:             &clusterID,
		ClusterName:           &clusterName,
		BuildFallback:         client.CIBuildFallbackNone,
		MaxParallelRuns:       4,
		MaxParallelSteps:      8,
		ArtifactRetentionDays: 30,
		CacheRetentionDays:    14,
		ImageGate:             client.CIImageGateApplicationDependencies,
	}}
	output, executeError := runOrgCISettings(t, mock, "org", "ci-settings", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "launch-week-2026") {
		t.Errorf("the pipeline cluster must be named, got %s", output)
	}
	if !strings.Contains(output, "Build fallback:          none") {
		t.Errorf("the build fallback must be shown verbatim, got %s", output)
	}
}

func TestRunOrgCISettingsGet_SaysWhenNoPipelineClusterIsChosen(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock, "org", "ci-settings", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "nowhere to run") {
		t.Errorf("an unset pipeline cluster must say what that costs, got %s", output)
	}
	if !strings.Contains(output, "Ankra's default") {
		t.Errorf("settings nobody has changed must say so, got %s", output)
	}
}

// A cluster id with no name is the deleted-cluster case. Printing the id alone
// would read as a healthy setting; the run that fails for it never says the
// target stopped existing.
func TestRunOrgCISettingsGet_CallsOutAPipelineClusterThatNoLongerExists(t *testing.T) {
	clusterID := "4b1f0f8e-9c1a-4c2f-9e6f-2a1d8b3c4d5e"
	settings := defaultCISettings()
	settings.ClusterID = &clusterID
	settings.IsDefault = false
	mock := &orgCISettingsMock{settings: settings}
	output, executeError := runOrgCISettings(t, mock, "org", "ci-settings", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "deleted") {
		t.Errorf("a cluster id with no name must be called out, got %s", output)
	}
}

// The contradiction that cost PLA-825 its thread: opting into the fallback is
// necessary but not sufficient, and the step's own failure message names the
// setting rather than the capability that is actually missing. Reading
// platform_builders here must not read as "the fallback will run".
func TestRunOrgCISettingsGet_WarnsThatPlatformBuildersNeedsTheCapabilityToo(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock, "org", "ci-settings", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "platform-builders capability") {
		t.Errorf("the capability gate must be named, got %s", output)
	}
}

// The same caveat must not fire for an organisation that opted out: there is
// no contradiction to warn about when the fallback really is 'none'.
func TestRunOrgCISettingsGet_StaysQuietAboutTheCapabilityWhenTheFallbackIsNone(t *testing.T) {
	settings := defaultCISettings()
	settings.BuildFallback = client.CIBuildFallbackNone
	settings.IsDefault = false
	mock := &orgCISettingsMock{settings: settings}
	output, executeError := runOrgCISettings(t, mock, "org", "ci-settings", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if strings.Contains(output, "platform-builders capability") {
		t.Errorf("nothing to warn about when the fallback is refused, got %s", output)
	}
}

// Only what was passed may reach the body. The endpoint reads presence, so a
// set that restated its neighbours would let one flag overwrite settings the
// caller never mentioned.
func TestRunOrgCISettingsSet_SendsOnlyTheFlagsThatWerePassed(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock,
		"org", "ci-settings", "set", "--build-fallback", "none")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.updateSeen) != 1 {
		t.Fatalf("expected exactly one write, got %d", len(mock.updateSeen))
	}
	changes := mock.updateSeen[0]
	if len(changes) != 1 {
		t.Fatalf("only the named setting may be written, got %v", changes)
	}
	if changes["ci_build_fallback"] != client.CIBuildFallbackNone {
		t.Errorf("expected ci_build_fallback none, got %v", changes["ci_build_fallback"])
	}
}

// --cluster takes a name, like every other cluster flag in the CLI, and the
// endpoint takes a uuid. The resolution happens here so a name is not the one
// form that silently fails.
func TestRunOrgCISettingsSet_ResolvesAClusterNameToItsID(t *testing.T) {
	mock := &orgCISettingsMock{
		settings: defaultCISettings(),
		clusters: []client.ClusterListItem{
			{ID: "4b1f0f8e-9c1a-4c2f-9e6f-2a1d8b3c4d5e", Name: "launch-week-2026"},
		},
	}
	output, executeError := runOrgCISettings(t, mock,
		"org", "ci-settings", "set", "--cluster", "launch-week-2026")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.updateSeen) != 1 {
		t.Fatalf("expected exactly one write, got %d", len(mock.updateSeen))
	}
	if got := mock.updateSeen[0]["ci_cluster_id"]; got != "4b1f0f8e-9c1a-4c2f-9e6f-2a1d8b3c4d5e" {
		t.Errorf("expected the resolved cluster id, got %v", got)
	}
}

// Clearing has to be distinguishable from not naming the setting at all: the
// key must be present and null, not absent.
func TestRunOrgCISettingsSet_ClearsThePipelineClusterWithAnExplicitNull(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock,
		"org", "ci-settings", "set", "--cluster", "")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	changes := mock.updateSeen[0]
	value, isPresent := changes["ci_cluster_id"]
	if !isPresent {
		t.Fatalf("clearing must name the setting, got %v", changes)
	}
	if value != nil {
		t.Errorf("clearing must send null, got %v", value)
	}
}

// The list setting replaces rather than appends, and an empty list is a real
// value meaning "no restriction" - dropping it would make the clear a no-op.
func TestRunOrgCISettingsSet_ClearsTheImagePolicyWithAnEmptyList(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock,
		"org", "ci-settings", "set", "--allowed-image-prefix", "")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	prefixes, isList := mock.updateSeen[0]["ci_allowed_image_prefixes"].([]string)
	if !isList {
		t.Fatalf("the policy must be sent as a list, got %v", mock.updateSeen[0])
	}
	if len(prefixes) != 0 {
		t.Errorf("an empty value clears the list, got %v", prefixes)
	}
}

func TestRunOrgCISettingsSet_KeepsEveryPrefixThatWasPassed(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock, "org", "ci-settings", "set",
		"--allowed-image-prefix", "ghcr.io/ankraio", "--allowed-image-prefix", "docker.io/library")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	prefixes, _ := mock.updateSeen[0]["ci_allowed_image_prefixes"].([]string)
	if len(prefixes) != 2 || prefixes[0] != "ghcr.io/ankraio" || prefixes[1] != "docker.io/library" {
		t.Errorf("both prefixes must reach the body in order, got %v", prefixes)
	}
}

// A mistyped value is the case this whole command is about, so the refusal
// names what may be used rather than only which field was wrong.
func TestRunOrgCISettingsSet_NamesTheVocabularyForAnUnknownBuildFallback(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock,
		"org", "ci-settings", "set", "--build-fallback", "ankra_builders")
	if executeError == nil {
		t.Fatalf("an unknown build fallback must be refused, output: %s", output)
	}
	if !strings.Contains(executeError.Error(), client.CIBuildFallbackPlatformBuilders) {
		t.Errorf("the refusal must name the allowed values, got %v", executeError)
	}
	if len(mock.updateSeen) != 0 {
		t.Errorf("a refused value must not be written, got %v", mock.updateSeen)
	}
}

func TestRunOrgCISettingsSet_RefusesAWriteThatNamesNothing(t *testing.T) {
	mock := &orgCISettingsMock{settings: defaultCISettings()}
	output, executeError := runOrgCISettings(t, mock, "org", "ci-settings", "set")
	if executeError == nil {
		t.Fatalf("a set naming no setting must be refused, output: %s", output)
	}
	if len(mock.updateSeen) != 0 {
		t.Errorf("nothing may be written, got %v", mock.updateSeen)
	}
}
