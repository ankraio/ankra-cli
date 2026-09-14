package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// bucketsMock answers the object storage bucket surface and records what the
// commands sent.
type bucketsMock struct {
	baseMock
	buckets          []client.ObjectStorageBucket
	listCalls        int
	bucket           *client.ObjectStorageBucket
	credentials      []client.Credential
	created          *client.CreateObjectStorageBucketRequest
	createdBucket    *client.ObjectStorageBucket
	deletedID        string
	destroyRequested bool
	deleteCalls      int
}

func (mock *bucketsMock) ListObjectStorageBuckets() (*client.ObjectStorageBucketListResult, error) {
	mock.listCalls++
	return &client.ObjectStorageBucketListResult{Items: mock.buckets}, nil
}

func (mock *bucketsMock) GetObjectStorageBucket(bucketID string) (*client.ObjectStorageBucket, error) {
	if mock.bucket == nil || mock.bucket.ID != bucketID {
		return nil, errors.New("Object storage bucket not found.")
	}
	return mock.bucket, nil
}

func (mock *bucketsMock) ListCredentials(_ *string) ([]client.Credential, error) {
	return mock.credentials, nil
}

func (mock *bucketsMock) CreateObjectStorageBucket(request client.CreateObjectStorageBucketRequest) (*client.ObjectStorageBucket, error) {
	mock.created = &request
	return mock.createdBucket, nil
}

func (mock *bucketsMock) DeleteObjectStorageBucket(bucketID string, destroyProviderResources bool) error {
	mock.deleteCalls++
	mock.deletedID = bucketID
	mock.destroyRequested = destroyProviderResources
	return nil
}

// resetBucketFlags restores the bucket commands' flags before and after each
// test: the cobra tree is shared across the test binary.
func resetBucketFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		for name, value := range map[string]string{
			"credential": "", "region": "", "bucket": "", "access-key-id": "", "secret-access-key": "", "wait": "false",
		} {
			_ = bucketCreateCmd.Flags().Set(name, value)
			bucketCreateCmd.Flags().Lookup(name).Changed = false
		}
		for _, name := range []string{"yes", "destroy-provider-resources"} {
			_ = bucketDeleteCmd.Flags().Set(name, "false")
			bucketDeleteCmd.Flags().Lookup(name).Changed = false
		}
		_ = bucketListCmd.Flags().Set("output", "")
		bucketListCmd.Flags().Lookup("output").Changed = false
		_ = bucketGetCmd.Flags().Set("output", "")
		bucketGetCmd.Flags().Lookup("output").Changed = false
	}
	reset()
	t.Cleanup(reset)
}

const bucketTestID = "5b0c7e1a-2d3f-4a5b-8c6d-7e8f9a0b1c2d"

func registryBucket() *client.ObjectStorageBucket {
	return &client.ObjectStorageBucket{
		ID: bucketTestID, Name: "registry", Provider: "hetzner", Region: "fsn1", Bucket: "ankra-registry-5b0c7e1a",
		Endpoint: "https://fsn1.your-objectstorage.com", PathStyle: true, Status: "ready",
		CreatedAt: "2026-09-14T10:00:00Z",
	}
}

func TestResolveBucketIDTakesAUUIDOrAName(t *testing.T) {
	mock := &bucketsMock{buckets: []client.ObjectStorageBucket{*registryBucket()}}
	if resolved, resolveError := resolveBucketID(mock, bucketTestID); resolveError != nil || resolved != bucketTestID ||
		mock.listCalls != 0 {
		t.Fatalf("a uuid must resolve to itself without a listing: %q %v (%d calls)", resolved, resolveError, mock.listCalls)
	}
	if resolved, resolveError := resolveBucketID(mock, "registry"); resolveError != nil || resolved != bucketTestID {
		t.Fatalf("a listed name must resolve: %q %v", resolved, resolveError)
	}
	if _, resolveError := resolveBucketID(mock, "nope"); resolveError == nil || !strings.Contains(resolveError.Error(), "ankra bucket list") {
		t.Fatalf("an unknown name must point at the listing: %v", resolveError)
	}
	twin := *registryBucket()
	twin.ID = "aaaaaaaa-1111-4111-8111-111111111111"
	mock.buckets = append(mock.buckets, twin)
	if _, resolveError := resolveBucketID(mock, "registry"); resolveError == nil || !strings.Contains(resolveError.Error(), "pass the id") {
		t.Fatalf("an ambiguous name must ask for the id: %v", resolveError)
	}
}

// A Hetzner bucket with no key flags sends no keys at all, so the platform
// uses the pair stored on the Hetzner credential rather than half of one.
func TestBucketCreateUsesTheCredentialPairWhenNoKeysArePassed(t *testing.T) {
	mock := &bucketsMock{
		credentials:   []client.Credential{{ID: "cred-hz", Name: "hetzner-main", Provider: "hetzner"}},
		createdBucket: &client.ObjectStorageBucket{ID: bucketTestID, Name: "registry", Provider: "hetzner", Status: "provisioning"},
	}
	setMockClient(t, mock)
	resetBucketFlags(t)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("bucket", "create", "registry")
	})
	if executeError != nil {
		t.Fatal(executeError)
	}
	if mock.created == nil || mock.created.CredentialID != "cred-hz" || mock.created.Region != "fsn1" ||
		mock.created.AccessKeyID != "" || mock.created.SecretAccessKey != "" {
		t.Fatalf("request = %+v", mock.created)
	}
	if !strings.Contains(output, "being created") {
		t.Fatalf("output must say the bucket is on its way:\n%s", output)
	}
}

func TestBucketCreateRefusesKeysForAProviderThatMintsThem(t *testing.T) {
	mock := &bucketsMock{credentials: []client.Credential{{ID: "cred-up", Name: "upcloud-main", Provider: "upcloud"}}}
	setMockClient(t, mock)
	resetBucketFlags(t)

	_, executeError := executeCommand("bucket", "create", "custody", "--access-key-id", "k", "--secret-access-key", "s")
	if executeError == nil || !strings.Contains(executeError.Error(), "only used for hetzner") {
		t.Fatalf("expected the keys to be refused, got %v", executeError)
	}
	if mock.created != nil {
		t.Fatal("nothing may be sent after a refusal")
	}
}

func TestBucketCreateRefusesHalfAHetznerPair(t *testing.T) {
	mock := &bucketsMock{credentials: []client.Credential{{ID: "cred-hz", Name: "hetzner-main", Provider: "hetzner"}}}
	setMockClient(t, mock)
	resetBucketFlags(t)

	_, executeError := executeCommand("bucket", "create", "registry", "--secret-access-key", "s")
	if executeError == nil || !strings.Contains(executeError.Error(), "needs --access-key-id") {
		t.Fatalf("expected half a pair to be refused, got %v", executeError)
	}
	if mock.created != nil {
		t.Fatal("nothing may be sent after a refusal")
	}
}

func TestBucketCreateAsksForACredentialWhenSeveralFit(t *testing.T) {
	mock := &bucketsMock{credentials: []client.Credential{
		{ID: "a", Name: "hetzner-main", Provider: "hetzner"}, {ID: "b", Name: "upcloud-main", Provider: "upcloud"},
	}}
	setMockClient(t, mock)
	resetBucketFlags(t)

	_, executeError := executeCommand("bucket", "create", "registry")
	if executeError == nil || !strings.Contains(executeError.Error(), "pass --credential") {
		t.Fatalf("expected the command to refuse to guess, got %v", executeError)
	}
}

func TestBucketDeleteLeavesTheBucketByDefault(t *testing.T) {
	mock := &bucketsMock{buckets: []client.ObjectStorageBucket{*registryBucket()}, bucket: registryBucket()}
	setMockClient(t, mock)
	resetBucketFlags(t)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("bucket", "delete", "registry", "--yes")
	})
	if executeError != nil {
		t.Fatal(executeError)
	}
	if mock.deletedID != bucketTestID || mock.destroyRequested {
		t.Fatalf("delete sent id=%q destroy=%v", mock.deletedID, mock.destroyRequested)
	}
	if !strings.Contains(output, "untouched") {
		t.Fatalf("output must say the bucket stayed:\n%s", output)
	}
}

// The destroying delete names the provider-side bucket in its prompt, and a
// declined prompt sends nothing.
func TestBucketDeleteWithDestroyNamesTheBucketAndHonoursANo(t *testing.T) {
	mock := &bucketsMock{buckets: []client.ObjectStorageBucket{*registryBucket()}, bucket: registryBucket()}
	setMockClient(t, mock)
	resetBucketFlags(t)

	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	prompt, executeError := executeCommand("bucket", "delete", "registry", "--destroy-provider-resources")
	if executeError == nil {
		t.Fatal("a declined prompt must not succeed")
	}
	for _, fragment := range []string{"ankra-registry-5b0c7e1a", "hetzner", "every object in it"} {
		if !strings.Contains(prompt, fragment) {
			t.Errorf("prompt missing %q:\n%s", fragment, prompt)
		}
	}
	if mock.deleteCalls != 0 {
		t.Fatal("a declined prompt must not send the delete")
	}

	resetBucketFlags(t)
	rootCmd.SetIn(strings.NewReader("y\n"))
	var confirmError error
	output := captureStdout(t, func() {
		_, confirmError = executeCommand("bucket", "delete", "registry", "--destroy-provider-resources")
	})
	if confirmError != nil {
		t.Fatal(confirmError)
	}
	if !mock.destroyRequested || !strings.Contains(output, "destroyed at the provider") {
		t.Fatalf("destroy=%v output:\n%s", mock.destroyRequested, output)
	}
}

// The platform tears a still-provisioning bucket down whatever is asked, so
// the prompt and the result must say so rather than promise the bucket stays.
func TestBucketDeleteOfAProvisioningBucketSaysItIsTornDown(t *testing.T) {
	provisioning := registryBucket()
	provisioning.Status = "provisioning"
	mock := &bucketsMock{buckets: []client.ObjectStorageBucket{*provisioning}, bucket: provisioning}
	setMockClient(t, mock)
	resetBucketFlags(t)

	rootCmd.SetIn(strings.NewReader("y\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	var prompt string
	var executeError error
	output := captureStdout(t, func() {
		prompt, executeError = executeCommand("bucket", "delete", "registry")
	})
	if executeError != nil {
		t.Fatal(executeError)
	}
	if !strings.Contains(prompt, "still being created") || strings.Contains(prompt, "stay on the provider") {
		t.Fatalf("prompt must say the half-made bucket is removed:\n%s", prompt)
	}
	if !strings.Contains(output, "destroyed at the provider") {
		t.Fatalf("output must not claim the bucket stays:\n%s", output)
	}
}

func TestWaitForBucketProvisioningKeepsPollingOnAStatusThatIsNotAVerdict(t *testing.T) {
	original := bucketPollInterval
	bucketPollInterval = time.Millisecond
	t.Cleanup(func() { bucketPollInterval = original })

	statuses := []string{"provisioning", "", "deleting", "ready"}
	mock := &sequencedBucketsMock{statuses: statuses}
	final, waitError := waitForBucketProvisioning(context.Background(), mock, bucketTestID)
	if waitError != nil || final.Status != "ready" || mock.calls != len(statuses) {
		t.Fatalf("final=%+v calls=%d err=%v", final, mock.calls, waitError)
	}
}

type sequencedBucketsMock struct {
	baseMock
	statuses []string
	calls    int
}

func (mock *sequencedBucketsMock) GetObjectStorageBucket(bucketID string) (*client.ObjectStorageBucket, error) {
	status := mock.statuses[mock.calls]
	mock.calls++
	return &client.ObjectStorageBucket{ID: bucketID, Name: "registry", Status: status}, nil
}

func TestBucketGetShowsTheErrorAndTheWayOut(t *testing.T) {
	failed := registryBucket()
	failed.Status = "error"
	excerpt := "the provider refused: BucketAlreadyExists"
	failed.ErrorExcerpt = &excerpt
	mock := &bucketsMock{buckets: []client.ObjectStorageBucket{*failed}, bucket: failed}
	setMockClient(t, mock)
	resetBucketFlags(t)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("bucket", "get", "registry")
	})
	if executeError != nil {
		t.Fatal(executeError)
	}
	for _, fragment := range []string{"BucketAlreadyExists", "--destroy-provider-resources", "ankra-registry-5b0c7e1a"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("output missing %q:\n%s", fragment, output)
		}
	}
	if strings.Contains(output, "secret") {
		t.Errorf("get must never print key material:\n%s", output)
	}
}
