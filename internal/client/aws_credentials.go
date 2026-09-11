package client

// awsCredentialProvider is the provider value the platform stores on an AWS
// credential, whether it holds an access key pair or an assumable role.
const awsCredentialProvider = "aws"

// ListAwsCredentials lists the organisation's AWS credentials. There is no
// /api/v1/credentials/aws twin of the per-provider list routes the other
// clouds carry: an AWS credential is the platform's existing aws credential
// (keys or role) and is read through the generic provider filter on the
// bearer credentials listing instead. The onboarding, role and keys writes
// are mounted on the session surface only (/org/credentials/aws/...), so
// they have no client method here until a bearer twin exists (see
// cmd/aws_credentials.go).
func (c *Client) ListAwsCredentials() ([]Credential, error) {
	provider := awsCredentialProvider
	return c.ListCredentials(&provider)
}
