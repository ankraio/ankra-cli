package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

// A platform without paid storage: the add-on route is not registered.
func (mock *registryStorageMock) GetRegistryStorageAddon(context.Context) (*client.RegistryStorageAddon, error) {
	return nil, client.NewUnexpectedResponseError(404, "registry storage request failed: status 404, body: 404 page not found")
}

// registryStorageAddonMock holds paid storage the way the platform does: a
// version that a write must match, a concurrent change that can land just
// before the CLI's write, and refusals to answer with.
type registryStorageAddonMock struct {
	registryStorageMock
	addon     client.RegistryStorageAddon
	revision  int
	addonGets int
	// writes is every blocks/expected-version pair sent, in order.
	writes []string
	// concurrent, when set, changes the stored add-on just before each of
	// the CLI's first concurrentWrites writes.
	concurrent       func(addon *client.RegistryStorageAddon)
	concurrentWrites int
	refusal          error
	getError         error
}

func gibPointer(n int64) *int64 { value := gib(n); return &value }

func newRegistryStorageAddonMock(blocks int) *registryStorageAddonMock {
	mock := &registryStorageAddonMock{revision: 1, addon: client.RegistryStorageAddon{
		Available: true, BlockGiB: 50, PriceCentsPerBlockMonth: 500, Currency: "eur", MaxBlocks: 20,
		BaseLimitBytes: gib(50), BillingReady: true, Version: "a1",
	}}
	mock.setBlocks(blocks)
	return mock
}

func (mock *registryStorageAddonMock) setBlocks(blocks int) {
	mock.addon.Blocks = blocks
	mock.addon.EffectiveLimitBytes = mock.addon.BaseLimitBytes + int64(blocks)*mock.addon.BlockGiB*bytesPerGiB
	mock.addon.MonthlyCents = int64(blocks) * mock.addon.PriceCentsPerBlockMonth
}

func (mock *registryStorageAddonMock) bump() {
	mock.revision++
	mock.addon.Version = fmt.Sprintf("a%d", mock.revision)
}

func (mock *registryStorageAddonMock) GetRegistryStorageAddon(context.Context) (*client.RegistryStorageAddon, error) {
	mock.addonGets++
	if mock.getError != nil {
		return nil, mock.getError
	}
	addon := mock.addon
	return &addon, nil
}

func (mock *registryStorageAddonMock) UpdateRegistryStorageAddon(_ context.Context, blocks int, expectedVersion string) (*client.RegistryStorageAddonUpdateResult, error) {
	mock.writes = append(mock.writes, fmt.Sprintf("%d@%s", blocks, expectedVersion))
	if mock.concurrent != nil && mock.concurrentWrites > 0 {
		mock.concurrentWrites--
		mock.concurrent(&mock.addon)
		mock.bump()
	}
	if mock.refusal != nil {
		return nil, mock.refusal
	}
	if expectedVersion != "" && expectedVersion != mock.addon.Version {
		return nil, client.NewUnexpectedResponseError(409, "Paid storage changed since you read it. Reload it and make your change again.")
	}
	mock.setBlocks(blocks)
	mock.bump()
	storage := registryStorageFixture(int64Pointer(gib(20)), client.RegistryStorageUsageComplete)
	storage.LimitBytes = mock.addon.EffectiveLimitBytes
	return &client.RegistryStorageAddonUpdateResult{Addon: mock.addon, Storage: *storage, HarborApplied: true}, nil
}

func addonStorageFixture(blocks int64) *client.RegistryStorage {
	storage := registryStorageFixture(int64Pointer(gib(20)), client.RegistryStorageUsageComplete)
	storage.LimitBytes = gib(50 + 50*blocks)
	storage.Projects[0].LimitBytes = int64Pointer(storage.LimitBytes)
	storage.BaseLimitBytes = gibPointer(50)
	addonBlocks := int(blocks)
	storage.AddonBlocks = &addonBlocks
	return storage
}

func TestRegistryStorageShowsTheAddon(t *testing.T) {
	mock := newRegistryStorageAddonMock(2)
	mock.storage = addonStorageFixture(2)
	output, runError := runRegistryCommand(t, mock, "", "storage")
	if runError != nil {
		t.Fatalf("storage: %v", runError)
	}
	mustContain(t, output, "Limit:  150 GiB per registry project (50 GiB included, 100 GiB bought)",
		"Add-on: 2 x 50 GiB, EUR 10.00/month")

	none := newRegistryStorageAddonMock(0)
	output, _ = runRegistryCommand(t, none, "", "storage")
	mustContain(t, output, "Add-on: none. Buy more in 50 GiB blocks at EUR 5.00/month each: ankra registry storage buy --blocks <n>")

	unavailable := newRegistryStorageAddonMock(0)
	unavailable.addon.Available = false
	reason := "Paid storage is paused for now."
	blocker := "Your account is restricted for an overdue invoice."
	unavailable.addon.UnavailableReason = &reason
	unavailable.addon.BillingBlocker = &blocker
	output, _ = runRegistryCommand(t, unavailable, "", "storage")
	mustContain(t, output, "buying more is not available - Paid storage is paused for now.",
		"Billing: Your account is restricted for an overdue invoice.")
	mustNotContain(t, output, "Buy more in")
}

// A platform without paid storage shows no add-on line and no error.
func TestRegistryStorageShowWithoutPaidStorage(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryStorageMock{}, "", "storage")
	if runError != nil {
		t.Fatalf("storage: %v", runError)
	}
	mustNotContain(t, output, "Add-on", "bought")
}

// Near full, the hints offer one more block with its price.
func TestRegistryStorageHintsOfferABlock(t *testing.T) {
	mock := newRegistryStorageAddonMock(0)
	mock.storage = registryStorageFixture(int64Pointer(gib(45)), client.RegistryStorageUsageComplete)
	output, _ := runRegistryCommand(t, mock, "", "storage")
	mustContain(t, output, "Buy more:     ankra registry storage buy --blocks 1   (50 GiB more for EUR 5.00/month)")
}

func TestRegistryStorageShowJSONCarriesTheAddon(t *testing.T) {
	mock := newRegistryStorageAddonMock(2)
	mock.storage = addonStorageFixture(2)
	stdout, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "-o", "json")
	var decoded map[string]any
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil {
		t.Fatalf("storage -o json: %v\n%s", runError, stdout)
	}
	addon, ok := decoded["addon"].(map[string]any)
	if !ok || addon["blocks"] != float64(2) || decoded["limit_bytes"] != float64(gib(150)) || decoded["addon_blocks"] != float64(2) {
		t.Fatalf("decoded = %v", decoded)
	}

	stdout, _, _ = runRegistryCommandSplit(t, &registryStorageMock{}, "", "storage", "-o", "json")
	decoded = nil
	if json.Unmarshal([]byte(stdout), &decoded) != nil {
		t.Fatalf("stdout = %s", stdout)
	}
	if _, present := decoded["addon"]; present {
		t.Fatalf("no paid storage on the platform, no addon key: %s", stdout)
	}
}

func TestRegistryStorageBuyConfirmsWithThePrice(t *testing.T) {
	mock := newRegistryStorageAddonMock(2)
	_, stderr, runError := runRegistryCommandSplit(t, mock, "n\n", "storage", "buy", "--blocks", "4")
	if exitCodeFor(runError) != exitCancelled || len(mock.writes) != 0 {
		t.Fatalf("a declined buy must not write: %v writes=%v", runError, mock.writes)
	}
	mustContain(t, stderr, "Paid registry storage: 2 -> 4 blocks of 50 GiB", "Limit:  150 GiB -> 250 GiB per registry project",
		"Price:  EUR 10.00/month -> EUR 20.00/month", "Change it? [y/N]")

	stdout, _, runError := runRegistryCommandSplit(t, mock, "y\n", "storage", "buy", "--blocks", "4")
	if runError != nil || len(mock.writes) != 1 || mock.writes[0] != "4@a1" {
		t.Fatalf("buy: %v writes=%v", runError, mock.writes)
	}
	mustContain(t, stdout, "Paid registry storage set to 4 x 50 GiB, EUR 20.00/month. The limit is now 250 GiB per registry project.",
		"The registry has the new limit now.")

	stdout, _, runError = runRegistryCommandSplit(t, mock, "", "storage", "buy", "--blocks", "0", "--yes")
	if runError != nil || mock.writes[1] != "0@a2" {
		t.Fatalf("buy --blocks 0 --yes: %v writes=%v", runError, mock.writes)
	}
	mustContain(t, stdout, "Paid registry storage removed. The limit is now 50 GiB per registry project.")
}

func TestRegistryStorageBuyJSON(t *testing.T) {
	mock := newRegistryStorageAddonMock(0)
	stdout, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "buy", "--blocks", "1", "--yes", "-o", "json")
	var decoded client.RegistryStorageAddonUpdateResult
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil || decoded.Addon.Blocks != 1 || !decoded.HarborApplied {
		t.Fatalf("buy -o json: %v\n%s", runError, stdout)
	}
}

// A version-only change between the read and the write shows the same
// limit and price, so the change is re-applied without asking again.
func TestRegistryStorageBuyReappliesWithoutAskingWhenNothingShownChanged(t *testing.T) {
	mock := newRegistryStorageAddonMock(2)
	mock.concurrentWrites = 1
	mock.concurrent = func(*client.RegistryStorageAddon) {}
	_, stderr, runError := runRegistryCommandSplit(t, mock, "y\n", "storage", "buy", "--blocks", "4")
	if runError != nil {
		t.Fatalf("buy: %v\n%s", runError, stderr)
	}
	if len(mock.writes) != 2 || mock.writes[0] != "4@a1" || mock.writes[1] != "4@a2" {
		t.Fatalf("writes = %v", mock.writes)
	}
	if strings.Count(stderr, "Change it?") != 1 {
		t.Fatalf("asked %d times, want once:\n%s", strings.Count(stderr, "Change it?"), stderr)
	}
}

// Someone else changed the blocks: the limit and price shown change, so
// the CLI asks again before writing (the second prompt here gets no
// answer, so nothing is written a second time).
func TestRegistryStorageBuyAsksAgainWhenThePriceChanged(t *testing.T) {
	mock := newRegistryStorageAddonMock(2)
	mock.concurrentWrites = 1
	mock.concurrent = func(addon *client.RegistryStorageAddon) {
		addon.Blocks = 3
		addon.EffectiveLimitBytes = gib(200)
		addon.MonthlyCents = 1500
	}
	_, stderr, runError := runRegistryCommandSplit(t, mock, "y\n", "storage", "buy", "--blocks", "4")
	if exitCodeFor(runError) != exitCancelled || len(mock.writes) != 1 {
		t.Fatalf("a changed price must be confirmed again: %v writes=%v", runError, mock.writes)
	}
	mustContain(t, stderr, "Paid registry storage: 3 -> 4 blocks of 50 GiB", "Price:  EUR 15.00/month -> EUR 20.00/month")

	again := newRegistryStorageAddonMock(2)
	again.concurrentWrites = 1
	again.concurrent = mock.concurrent
	if _, _, runError := runRegistryCommandSplit(t, again, "", "storage", "buy", "--blocks", "4", "--yes"); runError != nil || len(again.writes) != 2 {
		t.Fatalf("--yes re-applies without asking: %v writes=%v", runError, again.writes)
	}
}

func TestRegistryStorageBuyRefusals(t *testing.T) {
	twice := newRegistryStorageAddonMock(2)
	twice.concurrentWrites = 5
	twice.concurrent = func(*client.RegistryStorageAddon) {}
	_, _, runError := runRegistryCommandSplit(t, twice, "", "storage", "buy", "--blocks", "4", "--yes")
	if runError == nil || runError.Error() != "Paid storage changed since you read it. Reload it and make your change again." ||
		exitCodeFor(runError) != exitError || len(twice.writes) != 2 {
		t.Fatalf("a second conflict fails with the platform's text: %v (exit %d) writes=%v", runError, exitCodeFor(runError), twice.writes)
	}

	for status, text := range map[int]string{
		402: "Billing is not set up for this organisation. Add a payment method in Billing first.",
		422: "Your registry holds 120 GiB, so you need at least 2 blocks. Free space first or keep 2.",
	} {
		refused := newRegistryStorageAddonMock(3)
		refused.refusal = client.NewUnexpectedResponseError(status, text)
		_, _, runError := runRegistryCommandSplit(t, refused, "", "storage", "buy", "--blocks", "1", "--yes")
		if runError == nil || runError.Error() != text || exitCodeFor(runError) != exitError {
			t.Fatalf("%d must print the platform's text with exit 1: %v (exit %d)", status, runError, exitCodeFor(runError))
		}
	}

	tooMany := newRegistryStorageAddonMock(0)
	_, _, runError = runRegistryCommandSplit(t, tooMany, "", "storage", "buy", "--blocks", "21", "--yes")
	if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "ankra registry storage request") || len(tooMany.writes) != 0 {
		t.Fatalf("above max_blocks points at request: %v", runError)
	}

	same := newRegistryStorageAddonMock(2)
	stdout, _, runError := runRegistryCommandSplit(t, same, "", "storage", "buy", "--blocks", "2")
	if runError != nil || len(same.writes) != 0 || !strings.Contains(stdout, "already holds 2 paid storage blocks") {
		t.Fatalf("no change, no write: %v %s", runError, stdout)
	}

	unavailable := newRegistryStorageAddonMock(0)
	unavailable.addon.Available = false
	reason := "Paid storage is paused for now."
	unavailable.addon.UnavailableReason = &reason
	_, _, runError = runRegistryCommandSplit(t, unavailable, "", "storage", "buy", "--blocks", "1", "--yes")
	if exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "Paid storage is paused for now.") || len(unavailable.writes) != 0 {
		t.Fatalf("unavailable: %v", runError)
	}

	for _, arguments := range [][]string{{"storage", "buy"}, {"storage", "buy", "--blocks", "-1"}} {
		if _, _, runError := runRegistryCommandSplit(t, newRegistryStorageAddonMock(0), "", arguments...); exitCodeFor(runError) != exitUsage {
			t.Fatalf("%v must be a usage error: %v", arguments, runError)
		}
	}

	_, _, runError = runRegistryCommandSplit(t, &registryStorageMock{}, "", "storage", "buy", "--blocks", "1", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "not available on this platform yet") {
		t.Fatalf("a platform without paid storage says so: %v", runError)
	}
}
