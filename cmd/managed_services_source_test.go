package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

// Creating a service from another service's backups ('ankra services setup
// --from'), and connecting an application to a service that already runs
// ('ankra services connect'), against the fake platform of
// managed_services_test.go.

// preparedServiceReview decodes the one setup review a command prepared.
func preparedServiceReview(t *testing.T, platform *fakeServicesPlatform) map[string]any {
	t.Helper()
	prepares := platform.requests(http.MethodPost, "/service-admission/reviews")
	if len(prepares) != 1 {
		t.Fatalf("expected one prepare, got %d", len(prepares))
	}
	var prepared map[string]any
	if decodeError := json.Unmarshal([]byte(prepares[0].body), &prepared); decodeError != nil {
		t.Fatal(decodeError)
	}
	return prepared
}

// A setup that names a source sends the source's id, the moment in UTC and
// an empty consumer list, shows what the platform's plan says about the
// source, and ends by naming the command that connects an application.
func TestServicesSetupFromAnotherServiceSendsTheSourceAndTheMoment(t *testing.T) {
	for _, reference := range []string{"orders-db", fakeServiceInstanceID} {
		t.Run(reference, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			stdout, _, runError := runServicesCommand(t, platform, "",
				"setup", "orders-db-copy", "--package", "postgresql", "--cluster", fakeServiceClusterID,
				"--from", reference, "--at", "2026-11-18T10:30:00+01:00", "--yes")
			if runError != nil {
				t.Fatalf("setup --from: %v", runError)
			}
			prepared := preparedServiceReview(t, platform)
			if prepared["source_instance_id"] != fakeServiceInstanceID {
				t.Errorf("prepare source_instance_id = %v, want %s", prepared["source_instance_id"], fakeServiceInstanceID)
			}
			if prepared["source_moment"] != "2026-11-18T09:30:00Z" {
				t.Errorf("prepare source_moment = %v, want the moment in UTC", prepared["source_moment"])
			}
			if consumers, isList := prepared["consumer_ids"].([]any); !isList || len(consumers) != 0 {
				t.Errorf("consumer_ids must be an empty list with a source, got %v", prepared["consumer_ids"])
			}
			if confirms := platform.requests(http.MethodPost, "/reviews/"+fakeServiceReviewID+"/confirm"); len(confirms) != 1 {
				t.Fatalf("expected one confirm, got %d", len(confirms))
			}
			for _, expected := range []string{
				"Created from:   service orders-db (" + fakeServiceInstanceID + "), as of 2026-11-18T09:30:00Z",
				fakeServiceSourceStatement, "Consumers:      none", "is being set up",
				"It starts with no applications", "ankra services connect orders-db-copy --cluster "} {
				if !strings.Contains(stdout, expected) {
					t.Errorf("setup --from output lacks %q:\n%s", expected, stdout)
				}
			}
			if strings.Contains(stdout, "delivers the connection to each application") {
				t.Errorf("a service with no applications must not describe a delivery:\n%s", stdout)
			}
		})
	}
}

// Without --at the request carries no moment at all, which the platform
// reads as the newest point the backups reach, and the plan says so.
func TestServicesSetupFromWithoutAMomentRestoresToTheEndOfTheArchive(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db-copy", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--from", "orders-db", "--yes")
	if runError != nil {
		t.Fatalf("setup --from: %v", runError)
	}
	prepared := preparedServiceReview(t, platform)
	if _, sent := prepared["source_moment"]; sent {
		t.Errorf("no moment was named, yet source_moment was sent: %v", prepared["source_moment"])
	}
	if !strings.Contains(stdout, "as of the newest point its backups reach") {
		t.Errorf("the plan must say the backups are restored as far as they reach:\n%s", stdout)
	}
}

// A setup that names no source sends none of the optional fields, so the
// request is what it was before they existed.
func TestServicesSetupWithoutASourceSendsNoSourceFields(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID, "--yes")
	if runError != nil {
		t.Fatal(runError)
	}
	prepared := preparedServiceReview(t, platform)
	for _, field := range []string{"source_instance_id", "source_moment", "backup_vault"} {
		if _, sent := prepared[field]; sent {
			t.Errorf("an ordinary setup sent %s: %v", field, prepared[field])
		}
	}
	if strings.Contains(stdout, "Created from") || !strings.Contains(stdout, "delivers the connection to each application") {
		t.Errorf("an ordinary setup must read as before:\n%s", stdout)
	}
}

// What cannot work is refused before a review is stored.
func TestServicesSetupFromRefusesBeforePreparing(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		exitCode  int
		message   string
	}{
		{"with a consumer", []string{"--from", "orders-db", "--consumer", fakeServiceConsumerID}, exitUsage, "--consumer cannot be used with --from"},
		{"a moment without a source", []string{"--consumer", fakeServiceConsumerID, "--at", "2026-11-18T09:30:00Z"}, exitUsage, "--at needs --from"},
		{"a moment without a zone", []string{"--from", "orders-db", "--at", "2026-11-18T09:30:00"}, exitUsage, "RFC 3339"},
		{"a moment that is not a time", []string{"--from", "orders-db", "--at", "yesterday"}, exitUsage, "RFC 3339"},
		{"another mode", []string{"--from", "orders-db", "--mode", "existing"}, exitUsage, "--from needs --mode customer"},
		{"an unknown source", []string{"--from", "payments-db"}, exitNotFound, "--from: no service named"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			arguments := append([]string{"setup", "orders-db-copy", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--yes"},
				testCase.arguments...)
			_, _, runError := runServicesCommand(t, platform, "", arguments...)
			if runError == nil || exitCodeFor(runError) != testCase.exitCode || !strings.Contains(runError.Error(), testCase.message) {
				t.Fatalf("expected exit %d mentioning %q, got %v (exit %d)", testCase.exitCode, testCase.message, runError, exitCodeFor(runError))
			}
			if prepares := platform.requests(http.MethodPost, "/reviews"); len(prepares) != 0 {
				t.Errorf("a refused setup stored a review")
			}
		})
	}
}

// A source name two services share is refused with the way out that exists
// for --from: the id. --cluster names where the new service runs, so it
// cannot narrow the source.
func TestServicesSetupFromRefusesAnAmbiguousSourceName(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	previousClient := apiClient
	twin := fakeServiceInstance()
	twin["id"] = "c9b96bb8-0000-4000-8000-0000000000aa"
	twin["cluster_id"] = "c1a2b3c4-0000-4000-8000-0000000000bb"
	apiClient = &twinInstancesClient{Client: platform.client(), items: []map[string]any{fakeServiceInstance(), twin}}
	t.Cleanup(func() { apiClient = previousClient })
	_, resolveError := resolveServiceBackupSource(t.Context(), "orders-db")
	if resolveError == nil || exitCodeFor(resolveError) != exitUsage ||
		!strings.Contains(resolveError.Error(), "pass the service id to --from instead") || strings.Contains(resolveError.Error(), "--cluster") {
		t.Fatalf("expected an ambiguity refusal naming the id and not --cluster, got %v", resolveError)
	}
}

// fakeRetiredServiceTwinID is a retired service that carries the name of the
// fake platform's active one, as a service does after 'ankra services delete'
// and a new setup under the same name.
const fakeRetiredServiceTwinID = "c9b96bb8-0000-4000-8000-0000000000cc"

// fakeRetiredServiceTwin is that retired service's record.
func fakeRetiredServiceTwin(id string, retiredAt string) map[string]any {
	retired := fakeServiceInstance()
	retired["id"] = id
	retired["released_at"] = retiredAt
	retired["deployment_state"] = "released"
	return retired
}

// The source of --from may be a retired service: its backups outlive it. So
// where every other lookup lets the active service of a name win, a name an
// active and a retired service share is not answered for the caller. Each
// candidate is named with its id and whether it is active or when it was
// retired. A name only one service has resolves whatever its state.
func TestServicesBackupSourceNameSharedAcrossStatesAsksForTheID(t *testing.T) {
	const otherRetiredID = "c9b96bb8-0000-4000-8000-0000000000dd"
	cases := []struct {
		name       string
		inventory  []map[string]any
		resolvesTo string
		candidates []string
	}{
		{"an active and a retired service", []map[string]any{
			fakeServiceInstance(), fakeRetiredServiceTwin(fakeRetiredServiceTwinID, "2026-11-20T23:12:52Z")}, "",
			[]string{fakeServiceInstanceID + " on cluster " + fakeServiceClusterID + ", active",
				fakeRetiredServiceTwinID + " on cluster " + fakeServiceClusterID + ", retired 2026-11-20T23:12:52Z"}},
		{"two retired services", []map[string]any{
			fakeRetiredServiceTwin(fakeRetiredServiceTwinID, "2026-11-20T23:12:52Z"),
			fakeRetiredServiceTwin(otherRetiredID, "2026-11-20T23:21:40Z")}, "",
			[]string{fakeRetiredServiceTwinID + " on cluster " + fakeServiceClusterID + ", retired 2026-11-20T23:12:52Z",
				otherRetiredID + " on cluster " + fakeServiceClusterID + ", retired 2026-11-20T23:21:40Z"}},
		{"one retired service", []map[string]any{
			fakeRetiredServiceTwin(fakeRetiredServiceTwinID, "2026-11-20T23:12:52Z")}, fakeRetiredServiceTwinID, nil},
		{"one active service", []map[string]any{fakeServiceInstance()}, fakeServiceInstanceID, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			previousClient := apiClient
			apiClient = adaptMockClient(&twinInstancesClient{Client: platform.client(), items: testCase.inventory})
			t.Cleanup(func() { apiClient = adaptMockClient(previousClient) })
			resolved, resolveError := resolveServiceBackupSource(t.Context(), "orders-db")
			if testCase.resolvesTo != "" {
				if resolveError != nil || resolved == nil || resolved.ID != testCase.resolvesTo {
					t.Fatalf("expected the one service of the name (%s), got %v, %v", testCase.resolvesTo, resolved, resolveError)
				}
				return
			}
			if resolveError == nil || exitCodeFor(resolveError) != exitUsage ||
				!strings.Contains(resolveError.Error(), "pass the service id to --from instead") {
				t.Fatalf("expected a usage refusal asking for the id, got %v (resolved %v)", resolveError, resolved)
			}
			for _, candidate := range testCase.candidates {
				if !strings.Contains(resolveError.Error(), candidate) {
					t.Errorf("the refusal does not name %q:\n%v", candidate, resolveError)
				}
			}
		})
	}
}

// The commands that act on a service in use keep their rule: the active
// service of a name wins over a retired one, so retiring a service and
// setting it up again does not make its name ambiguous for them.
func TestResolveServiceInstanceStillTakesTheActiveServiceOverARetiredOne(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	previousClient := apiClient
	apiClient = adaptMockClient(&twinInstancesClient{Client: platform.client(), items: []map[string]any{
		fakeRetiredServiceTwin(fakeRetiredServiceTwinID, "2026-11-20T23:12:52Z"), fakeServiceInstance()}})
	t.Cleanup(func() { apiClient = adaptMockClient(previousClient) })
	resolved, resolveError := resolveServiceInstance(newServicesGetCommand(), "orders-db")
	if resolveError != nil || resolved == nil || resolved.ID != fakeServiceInstanceID {
		t.Fatalf("expected the active service (%s), got %v, %v", fakeServiceInstanceID, resolved, resolveError)
	}
}

// A setup whose --from name an active and a retired service share stops
// before a review is prepared: with --yes nothing else would stand between
// the name and a service created from the wrong one's data.
func TestServicesSetupFromPreparesNothingForANameSharedAcrossStates(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	previousClient := apiClient
	apiClient = adaptMockClient(&twinInstancesClient{Client: platform.client(), items: []map[string]any{
		fakeServiceInstance(), fakeRetiredServiceTwin(fakeRetiredServiceTwinID, "2026-11-20T23:12:52Z")}})
	t.Cleanup(func() { apiClient = adaptMockClient(previousClient) })
	servicesCommand := newServicesCommand()
	var stdout, stderr strings.Builder
	servicesCommand.SetOut(&stdout)
	servicesCommand.SetErr(&stderr)
	servicesCommand.SetIn(strings.NewReader(""))
	servicesCommand.SetArgs([]string{"setup", "orders-db-copy", "--package", "postgresql", "--cluster", fakeServiceClusterID,
		"--from", "orders-db", "--yes"})
	runError := servicesCommand.Execute()
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--from: 2 services are named") {
		t.Fatalf("expected the shared name refused as a usage error, got %v (exit %d)", runError, exitCodeFor(runError))
	}
	for _, candidate := range []string{fakeServiceInstanceID, fakeRetiredServiceTwinID, "active", "retired 2026-11-20T23:12:52Z"} {
		if !strings.Contains(runError.Error(), candidate) {
			t.Errorf("the refusal does not name %q:\n%v", candidate, runError)
		}
	}
	if prepares := platform.requests(http.MethodPost, "/reviews"); len(prepares) != 0 {
		t.Errorf("a setup refused for its source name prepared %d review(s)", len(prepares))
	}
	if strings.Contains(stdout.String(), "is being set up") {
		t.Errorf("a refused setup must not read as started:\n%s", stdout.String())
	}
}

// A plan that does not create the service from the source and moment asked
// for is never confirmed, and never offered for a later confirm: a platform
// from before this lane ignores the request fields and plans an empty
// service.
func TestServicesSetupFromNeverConfirmsAPlanThatIsNotTheOneAskedFor(t *testing.T) {
	moment := func(value string) *string { return &value }
	cases := []struct {
		name      string
		configure func(*fakeServicesPlatform)
		arguments []string
		message   string
	}{
		{"the platform ignores the source", func(platform *fakeServicesPlatform) { platform.sourceIgnored = true },
			nil, "cannot create a service from another service's backups yet"},
		{"the platform ignores the source, review only", func(platform *fakeServicesPlatform) { platform.sourceIgnored = true },
			[]string{"--review-only"}, "cannot create a service from another service's backups yet"},
		{"the plan names another source", func(platform *fakeServicesPlatform) {
			platform.sourcePlanInstanceID = "c9b96bb8-0000-4000-8000-0000000000aa"
		}, nil, "not from orders-db"},
		{"the plan names another moment", func(platform *fakeServicesPlatform) { platform.sourcePlanMoment = moment("2026-11-18T09:31:00Z") },
			[]string{"--at", "2026-11-18T09:30:00Z"}, "as of 2026-11-18T09:31:00Z, not as of 2026-11-18T09:30:00Z"},
		{"the plan names a moment and none was asked", func(platform *fakeServicesPlatform) { platform.sourcePlanMoment = moment("2026-11-18T09:30:00Z") },
			nil, "as of 2026-11-18T09:30:00Z, not as of the newest point its backups reach"},
		{"the plan drops the moment", func(platform *fakeServicesPlatform) { platform.sourcePlanMoment = moment("") },
			[]string{"--at", "2026-11-18T09:30:00Z"}, "as of the newest point its backups reach, not as of 2026-11-18T09:30:00Z"},
		{"the plan's moment is not a time", func(platform *fakeServicesPlatform) { platform.sourcePlanMoment = moment("soon") },
			[]string{"--at", "2026-11-18T09:30:00Z"}, "does not read as a time"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			testCase.configure(platform)
			arguments := append([]string{"setup", "orders-db-copy", "--package", "postgresql", "--cluster", fakeServiceClusterID,
				"--from", "orders-db", "--yes"}, testCase.arguments...)
			stdout, stderr, runError := runServicesCommand(t, platform, "", arguments...)
			if runError == nil || exitCodeFor(runError) != exitBlocked || !strings.Contains(runError.Error(), testCase.message) ||
				!strings.Contains(runError.Error(), "nothing was set up") {
				t.Fatalf("expected a blocked setup mentioning %q, got %v (exit %d)", testCase.message, runError, exitCodeFor(runError))
			}
			if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
				t.Fatalf("a plan that is not the one asked for was confirmed: %+v", confirms)
			}
			if strings.Contains(stdout+stderr, "ankra services reviews confirm") {
				t.Errorf("a plan that is not the one asked for must not be offered for a later confirm:\n%s%s", stdout, stderr)
			}
		})
	}
}

// The same moment spelled another way is the same moment.
func TestServicesSetupFromAcceptsThePlansSpellingOfTheMoment(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	spelled := "2026-11-18T09:30:00.000Z"
	platform.sourcePlanMoment = &spelled
	if _, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db-copy", "--package", "postgresql", "--cluster", fakeServiceClusterID,
		"--from", "orders-db", "--at", "2026-11-18T09:30:00Z", "--yes"); runError != nil {
		t.Fatalf("the plan names the moment asked for, spelled with a fraction: %v", runError)
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 1 {
		t.Fatalf("expected one confirm, got %d", len(confirms))
	}
}

// --backup-vault sends the vault's name, and the plan shows the vault and the
// sentence the platform asks the person to approve.
func TestServicesSetupNamesTheBackupVault(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID,
		"--backup-vault", " production-backups ", "--yes")
	if runError != nil {
		t.Fatalf("setup --backup-vault: %v", runError)
	}
	if prepared := preparedServiceReview(t, platform); prepared["backup_vault"] != "production-backups" {
		t.Errorf("prepare backup_vault = %v, want the name without the spaces around it", prepared["backup_vault"])
	}
	for _, expected := range []string{"Backup vault:   production-backups",
		"This service is backed up into the backup vault production-backups."} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("setup --backup-vault output lacks %q:\n%s", expected, stdout)
		}
	}
	if confirms := platform.requests(http.MethodPost, "/reviews/"+fakeServiceReviewID+"/confirm"); len(confirms) != 1 {
		t.Fatalf("expected one confirm, got %d", len(confirms))
	}
}

// A service created from another one names no vault of its own: the plan
// carries the vault that holds the other service's backups, and says so.
func TestServicesSetupFromShowsTheVaultThatHoldsTheBackups(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db-copy", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--from", "orders-db", "--yes")
	if runError != nil {
		t.Fatalf("setup --from: %v", runError)
	}
	if _, sent := preparedServiceReview(t, platform)["backup_vault"]; sent {
		t.Errorf("no vault was named, yet backup_vault was sent")
	}
	for _, expected := range []string{"Backup vault:   " + fakeServiceArchiveVault, "which holds the archive of the service orders-db"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("setup --from output lacks %q:\n%s", expected, stdout)
		}
	}
}

// A plan that does not name the vault --backup-vault asked for is never
// confirmed, and never offered for a later confirm: a platform from before a
// setup could name its vault ignores the request field.
func TestServicesSetupNeverConfirmsAPlanForAnotherBackupVault(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*fakeServicesPlatform)
		arguments []string
		message   string
	}{
		{"the platform ignores the vault", func(platform *fakeServicesPlatform) { platform.vaultIgnored = true },
			nil, "cannot name a setup's backup vault yet"},
		{"the platform ignores the vault, review only", func(platform *fakeServicesPlatform) { platform.vaultIgnored = true },
			[]string{"--review-only"}, "cannot name a setup's backup vault yet"},
		{"the plan names another vault", func(platform *fakeServicesPlatform) { platform.planVaultName = "staging-backups" },
			nil, "into the backup vault staging-backups, not into production-backups"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			testCase.configure(platform)
			arguments := append([]string{"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID,
				"--consumer", fakeServiceConsumerID, "--backup-vault", "production-backups", "--yes"}, testCase.arguments...)
			stdout, stderr, runError := runServicesCommand(t, platform, "", arguments...)
			if runError == nil || exitCodeFor(runError) != exitBlocked || !strings.Contains(runError.Error(), testCase.message) ||
				!strings.Contains(runError.Error(), "nothing was set up") {
				t.Fatalf("expected a blocked setup mentioning %q, got %v (exit %d)", testCase.message, runError, exitCodeFor(runError))
			}
			if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
				t.Fatalf("a plan for another backup vault was confirmed: %+v", confirms)
			}
			if strings.Contains(stdout+stderr, "ankra services reviews confirm") {
				t.Errorf("a plan for another backup vault must not be offered for a later confirm:\n%s%s", stdout, stderr)
			}
		})
	}
}

// A stored review created from another service says so when it is read and
// when it is confirmed later, and its receipt names the connect command.
func TestServicesReviewsShowTheSource(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.reviewHasSource = true
	stdout, _, runError := runServicesCommand(t, platform, "", "reviews", "get", fakeServiceReviewID)
	if runError != nil {
		t.Fatal(runError)
	}
	for _, expected := range []string{"Created from:   service orders-db (" + fakeServiceInstanceID + "), as of 2026-11-18T09:30:00Z",
		fakeServiceSourceStatement, "Backup vault:   " + fakeServiceArchiveVault, "Destinations:   none"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("reviews get lacks %q:\n%s", expected, stdout)
		}
	}
	stdout, _, runError = runServicesCommand(t, platform, "", "reviews", "confirm", fakeServiceReviewID, "--digest", fakeServiceReviewDigest, "--yes")
	if runError != nil {
		t.Fatal(runError)
	}
	for _, expected := range []string{"Created from:   service orders-db", "It starts with no applications", "ankra services connect orders-db"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("reviews confirm lacks %q:\n%s", expected, stdout)
		}
	}
}

// connect sends the binding and the generation of the service it read, and
// says where the application's pickup can be followed.
func TestServicesConnectSendsTheBindingAndTheGenerationRead(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "", "connect", "orders-db", "--consumer", fakeServiceConsumerID)
	if runError != nil {
		t.Fatalf("connect: %v", runError)
	}
	connects := platform.requests(http.MethodPost, "/instances/"+fakeServiceInstanceID+"/consumers")
	if len(connects) != 1 {
		t.Fatalf("expected one connect, got %d", len(connects))
	}
	if connects[0].body != `{"consumer_id":"`+fakeServiceConsumerID+`","expected_generation":2}` {
		t.Errorf("connect must carry the binding and the generation read, got %s", connects[0].body)
	}
	for _, expected := range []string{`The application is connected to service "orders-db" on cluster prod.`,
		"ankra services get orders-db --cluster prod"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("connect output lacks %q:\n%s", expected, stdout)
		}
	}
}

// Under -o json stdout is the service as it reads afterwards and nothing
// else, without any credential value the platform's answer carried.
func TestServicesConnectStructuredOutputStaysParseable(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, stderr, runError := runServicesCommand(t, platform, "", "connect", fakeServiceInstanceID, "--consumer", fakeServiceConsumerID, "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var connected client.ServiceInstance
	if decodeError := json.Unmarshal([]byte(stdout), &connected); decodeError != nil {
		t.Fatalf("stdout is not the service document: %v\n%s", decodeError, stdout)
	}
	if connected.ID != fakeServiceInstanceID {
		t.Errorf("stdout names service %q, want %s", connected.ID, fakeServiceInstanceID)
	}
	if strings.Contains(stdout+stderr, fakeServiceSecretValue) {
		t.Fatalf("a credential value reached the output:\n%s%s", stdout, stderr)
	}
}

// What cannot work is refused before anything is sent, and a conflict keeps
// the platform's sentence and adds where to look.
func TestServicesConnectRefusals(t *testing.T) {
	t.Run("a value that is not a binding id", func(t *testing.T) {
		platform := newFakeServicesPlatform(t)
		_, _, runError := runServicesCommand(t, platform, "", "connect", "orders-db", "--consumer", "storefront")
		if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "consumer binding id") {
			t.Fatalf("expected a usage refusal, got %v (exit %d)", runError, exitCodeFor(runError))
		}
		if calls := platform.requests(http.MethodPost, "/consumers"); len(calls) != 0 {
			t.Errorf("a refused connect was sent")
		}
	})
	t.Run("a retired service", func(t *testing.T) {
		platform := newFakeServicesPlatform(t)
		platform.released = true
		_, _, runError := runServicesCommand(t, platform, "", "connect", "orders-db", "--consumer", fakeServiceConsumerID)
		if runError == nil || exitCodeFor(runError) != exitBlocked || !strings.Contains(runError.Error(), "was retired") {
			t.Fatalf("expected a blocked connect, got %v (exit %d)", runError, exitCodeFor(runError))
		}
		if calls := platform.requests(http.MethodPost, "/consumers"); len(calls) != 0 {
			t.Errorf("a connect to a retired service was sent")
		}
	})
	t.Run("the service changed since it was read", func(t *testing.T) {
		platform := newFakeServicesPlatform(t)
		platform.connectConflict = true
		_, _, runError := runServicesCommand(t, platform, "", "connect", "orders-db", "--consumer", fakeServiceConsumerID)
		if runError == nil || !strings.Contains(runError.Error(), "Service configuration changed") ||
			!strings.Contains(runError.Error(), "ankra services get orders-db") {
			t.Fatalf("expected the platform's conflict with where to look, got %v", runError)
		}
	})
}
