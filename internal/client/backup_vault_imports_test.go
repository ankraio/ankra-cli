package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestUploadPresignedObjectPutsTheBodyWithoutABearerToken(t *testing.T) {
	var received struct {
		method        string
		authorization string
		contentLength int64
		body          string
		query         string
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received.method = request.Method
		received.authorization = request.Header.Get("Authorization")
		received.contentLength = request.ContentLength
		received.query = request.URL.RawQuery
		body, _ := io.ReadAll(request.Body)
		received.body = string(body)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	apiClient := &Client{BaseURL: server.URL, Token: "secret-token", HTTP: server.Client()}
	uploadError := apiClient.UploadPresignedObject(context.Background(), BackupVaultImportUpload{Method: http.MethodPut, URL: server.URL + "/bucket/imports/1/db/office.dump?X-Amz-Signature=abc"}, strings.NewReader("PGDMP"), 5)
	if uploadError != nil {
		t.Fatal(uploadError)
	}
	if received.method != http.MethodPut || received.body != "PGDMP" || received.contentLength != 5 || received.query != "X-Amz-Signature=abc" {
		t.Errorf("received = %+v", received)
	}
	if received.authorization != "" {
		t.Errorf("the presigned url is the whole credential; no bearer token may be sent, got %q", received.authorization)
	}
}

func TestUploadPresignedObjectReportsTheBucketsRefusal(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte("<Error><Code>SignatureDoesNotMatch</Code></Error>"))
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, HTTP: server.Client()}
	uploadError := apiClient.UploadPresignedObject(context.Background(), BackupVaultImportUpload{Method: http.MethodPut, URL: server.URL + "/x"}, strings.NewReader("x"), 1)
	if uploadError == nil || !strings.Contains(uploadError.Error(), "SignatureDoesNotMatch") {
		t.Errorf("the bucket's reason must surface, got %v", uploadError)
	}
	if attempts != 1 {
		t.Errorf("a refusal is final; it must not be retried, got %d attempts", attempts)
	}
}

func TestUploadPresignedObjectRetriesAServerFailureFromTheStart(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		content, _ := io.ReadAll(request.Body)
		bodies = append(bodies, string(content))
		if len(bodies) == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, HTTP: server.Client()}
	uploadError := apiClient.UploadPresignedObject(context.Background(), BackupVaultImportUpload{Method: http.MethodPut, URL: server.URL + "/x"}, strings.NewReader("PGDMP"), 5)
	if uploadError != nil {
		t.Fatal(uploadError)
	}
	if len(bodies) != 2 || bodies[0] != "PGDMP" || bodies[1] != "PGDMP" {
		t.Errorf("a 5xx must be retried with the whole body again, got %q", bodies)
	}
}

func TestUploadPresignedObjectRefusesMoreThanOneUploadCarries(t *testing.T) {
	apiClient := &Client{BaseURL: "http://unused.invalid"}
	uploadError := apiClient.UploadPresignedObject(context.Background(), BackupVaultImportUpload{Method: http.MethodPut, URL: "http://unused.invalid/x"}, strings.NewReader(""), ImportArtifactMaximumBytes+1)
	if uploadError == nil || !strings.Contains(uploadError.Error(), "at most") {
		t.Errorf("an artifact above the single-upload limit must be refused without a request, got %v", uploadError)
	}
}

func TestBackupVaultImportRoutes(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		if request.Header.Get("Authorization") != "Bearer token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(request.URL.Path, "/restore"):
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"imp-1","status":"restoring","restore":{"operation_id":"op-1","status":"restoring","steps":[]}}`))
		case strings.HasSuffix(request.URL.Path, "/complete"):
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"detail":"The upload is incomplete: db/office.dump is missing from the vault. Upload again, then complete the import."}`))
		case request.Method == http.MethodPost:
			_, _ = writer.Write([]byte(`{"import":{"id":"imp-1","status":"uploading"},"uploads":[{"path":"db/office.dump","method":"PUT","url":"https://vault/x","size_bytes":5}]}`))
		default:
			_, _ = writer.Write([]byte(`{"id":"imp-1","status":"completed"}`))
		}
	}))
	defer server.Close()
	apiClient := &Client{BaseURL: server.URL, Token: "token", HTTP: server.Client()}

	created, createError := apiClient.CreateBackupVaultImport("vault-1", CreateBackupVaultImportRequest{ClusterID: "c", Manifest: []byte(`{"version":1}`)})
	if createError != nil || created.Import.ID != "imp-1" || len(created.Uploads) != 1 || created.Uploads[0].SizeBytes != 5 {
		t.Errorf("create = %+v, %v", created, createError)
	}
	if _, completeError := apiClient.CompleteBackupVaultImport("vault-1", "imp-1"); completeError == nil || !strings.Contains(completeError.Error(), "db/office.dump is missing from the vault") {
		t.Errorf("a 409 detail must surface verbatim, got %v", completeError)
	}
	restored, restoreError := apiClient.RestoreBackupVaultImport("vault-1", "imp-1")
	if restoreError != nil || restored.Status != "restoring" || restored.Restore == nil || restored.Restore.OperationID != "op-1" {
		t.Errorf("restore (202) = %+v, %v", restored, restoreError)
	}
	got, getError := apiClient.GetBackupVaultImport("vault-1", "imp-1")
	if getError != nil || got.Status != "completed" {
		t.Errorf("get = %+v, %v", got, getError)
	}
	want := []string{
		"POST /api/v1/org/backup-vaults/vault-1/imports",
		"POST /api/v1/org/backup-vaults/vault-1/imports/imp-1/complete",
		"POST /api/v1/org/backup-vaults/vault-1/imports/imp-1/restore",
		"GET /api/v1/org/backup-vaults/vault-1/imports/imp-1",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Errorf("paths = %v", paths)
	}
}

// multipartFake is an object store that accepts part PUTs, hands back
// ETags, and records the completion or abort it received.
type multipartFake struct {
	mutex        sync.Mutex
	parts        map[string]string
	completed    string
	aborted      bool
	failPart     string
	completeBody string
}

func newMultipartFake(t *testing.T) (*multipartFake, *httptest.Server) {
	t.Helper()
	fake := &multipartFake{parts: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fake.mutex.Lock()
		defer fake.mutex.Unlock()
		query := request.URL.Query()
		switch {
		case request.Method == http.MethodPut && query.Get("partNumber") != "":
			content, _ := io.ReadAll(request.Body)
			if query.Get("partNumber") == fake.failPart {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			fake.parts[query.Get("partNumber")] = string(content)
			writer.Header().Set("ETag", `"etag-`+query.Get("partNumber")+`"`)
			writer.WriteHeader(http.StatusOK)
		case request.Method == http.MethodPost && query.Get("uploadId") != "":
			content, _ := io.ReadAll(request.Body)
			fake.completed = string(content)
			if fake.completeBody != "" {
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write([]byte(fake.completeBody))
				return
			}
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("<CompleteMultipartUploadResult><ETag>\"final\"</ETag></CompleteMultipartUploadResult>"))
		case request.Method == http.MethodDelete && query.Get("uploadId") != "":
			fake.aborted = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return fake, server
}

func multipartUpload(server *httptest.Server, partSize int64, parts int) BackupVaultImportUpload {
	upload := BackupVaultImportUpload{
		Method: BackupVaultImportUploadMethodMultipart, UploadID: "up-1", PartSizeBytes: partSize,
		CompleteURL: server.URL + "/o?uploadId=up-1&X-Amz-Signature=c", AbortURL: server.URL + "/o?uploadId=up-1&X-Amz-Signature=a",
	}
	for number := 1; number <= parts; number++ {
		upload.Parts = append(upload.Parts, BackupVaultImportUploadPart{PartNumber: number, URL: fmt.Sprintf("%s/o?partNumber=%d&uploadId=up-1&X-Amz-Signature=p", server.URL, number)})
	}
	return upload
}

func TestUploadPresignedObjectSendsAMultipartUploadPartByPart(t *testing.T) {
	fake, server := newMultipartFake(t)
	apiClient := &Client{BaseURL: server.URL, Token: "secret-token", HTTP: server.Client()}
	body := strings.NewReader("0123456789abcdefXYZ")
	uploadError := apiClient.UploadPresignedObject(context.Background(), multipartUpload(server, 8, 3), body, 19)
	if uploadError != nil {
		t.Fatal(uploadError)
	}
	if fake.parts["1"] != "01234567" || fake.parts["2"] != "89abcdef" || fake.parts["3"] != "XYZ" {
		t.Errorf("parts must be the body split at the part size, got %v", fake.parts)
	}
	for _, want := range []string{"<CompleteMultipartUpload>", "<Part><PartNumber>1</PartNumber><ETag>&#34;etag-1&#34;</ETag></Part>", "<PartNumber>3</PartNumber><ETag>&#34;etag-3&#34;</ETag>"} {
		if !strings.Contains(fake.completed, want) {
			t.Errorf("the completion must list every part with its ETag, want %q in:\n%s", want, fake.completed)
		}
	}
	if fake.aborted {
		t.Error("a successful upload must not be aborted")
	}
}

func TestUploadPresignedObjectAbortsAMultipartUploadThatFails(t *testing.T) {
	fake, server := newMultipartFake(t)
	fake.failPart = "2"
	apiClient := &Client{BaseURL: server.URL, HTTP: server.Client()}
	uploadError := apiClient.UploadPresignedObject(context.Background(), multipartUpload(server, 8, 3), strings.NewReader("0123456789abcdefXYZ"), 19)
	if uploadError == nil || !strings.Contains(uploadError.Error(), "part 2 of 3") {
		t.Fatalf("the failing part must be named, got %v", uploadError)
	}
	if !fake.aborted || fake.completed != "" {
		t.Errorf("a failed upload is aborted, never completed: aborted=%v completed=%q", fake.aborted, fake.completed)
	}

	fake, server = newMultipartFake(t)
	fake.completeBody = "<Error><Code>InternalError</Code><Message>We encountered an internal error.</Message></Error>"
	apiClient = &Client{BaseURL: server.URL, HTTP: server.Client()}
	uploadError = apiClient.UploadPresignedObject(context.Background(), multipartUpload(server, 8, 3), strings.NewReader("0123456789abcdefXYZ"), 19)
	if uploadError == nil || !strings.Contains(uploadError.Error(), "InternalError") {
		t.Fatalf("a 200 carrying an error document is a failed completion, got %v", uploadError)
	}
	if !fake.aborted {
		t.Error("a failed completion is aborted")
	}

	apiClient = &Client{BaseURL: server.URL, HTTP: server.Client()}
	uploadError = apiClient.UploadPresignedObject(context.Background(), multipartUpload(server, 8, 2), strings.NewReader("0123456789abcdefXYZ"), 19)
	if uploadError == nil || !strings.Contains(uploadError.Error(), "minted 2 part(s) for an artifact that needs 3") {
		t.Errorf("a part count that does not cover the artifact is refused before any byte moves, got %v", uploadError)
	}
}
