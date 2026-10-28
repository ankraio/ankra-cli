package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// ObjectStorageBucket is a bucket Ankra created on one of the organisation's
// provider credentials. Status is "provisioning" while the platform creates
// it, "ready" once it exists and answers to its keys, and "error" when
// provisioning - or a requested teardown - failed; ErrorExcerpt then says
// why. Key material is never returned.
type ObjectStorageBucket struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Provider     string  `json:"provider"`
	Region       string  `json:"region"`
	Bucket       string  `json:"bucket"`
	Endpoint     string  `json:"endpoint"`
	PathStyle    bool    `json:"path_style"`
	CredentialID string  `json:"credential_id"`
	Status       string  `json:"status"`
	ErrorExcerpt *string `json:"error_excerpt,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// ObjectStorageBucketListResult wraps the organisation's bucket listing.
type ObjectStorageBucketListResult struct {
	Items []ObjectStorageBucket `json:"items"`
}

// CreateObjectStorageBucketRequest asks the platform to create a bucket on a
// provider credential. The provider comes from the credential; Bucket is
// optional (the platform derives a unique name). The key pair is for Hetzner
// only, whose Cloud API cannot mint Object Storage keys - and even there it
// may be omitted when the pair is stored on the Hetzner credential.
type CreateObjectStorageBucketRequest struct {
	Name            string `json:"name"`
	CredentialID    string `json:"credential_id"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

// ListObjectStorageBuckets returns the organisation's buckets, newest first.
// GET /api/v1/org/object-storage-buckets
func (c *Client) ListObjectStorageBuckets() (*ObjectStorageBucketListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/object-storage-buckets", c.BaseURL)
	var result ObjectStorageBucketListResult
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetObjectStorageBucket returns one bucket by id.
// GET /api/v1/org/object-storage-buckets/{bucket_id}
func (c *Client) GetObjectStorageBucket(bucketID string) (*ObjectStorageBucket, error) {
	url := fmt.Sprintf("%s/api/v1/org/object-storage-buckets/%s", c.BaseURL, neturl.PathEscape(bucketID))
	var result ObjectStorageBucket
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// CreateObjectStorageBucket records the bucket and enqueues the platform job
// that creates it. The returned bucket is in "provisioning"; poll
// GetObjectStorageBucket for ready or error.
// POST /api/v1/org/object-storage-buckets
func (c *Client) CreateObjectStorageBucket(request CreateObjectStorageBucketRequest) (*ObjectStorageBucket, error) {
	url := fmt.Sprintf("%s/api/v1/org/object-storage-buckets", c.BaseURL)
	var result ObjectStorageBucket
	if requestError := c.sendJSON(http.MethodPost, url, request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DeleteObjectStorageBucket stops Ankra managing a bucket. With
// destroyProviderResources the platform also empties and deletes the bucket
// and removes what it created for it; without it, the bucket and its objects
// stay on the provider.
// DELETE /api/v1/org/object-storage-buckets/{bucket_id}
func (c *Client) DeleteObjectStorageBucket(bucketID string, destroyProviderResources bool) error {
	url := fmt.Sprintf("%s/api/v1/org/object-storage-buckets/%s", c.BaseURL, neturl.PathEscape(bucketID))
	if destroyProviderResources {
		url += "?destroy_provider_resources=true"
	}
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}
