package config

// Canonical environment variable names for fallback endpoints.
//
// An endpoint is a hostname, not a secret. It lives in the environment rather than in
// the checked-in JSON for one reason: the account-specific URL is a property of the
// account you deploy into, and every line of a committed config is a line someone has
// to edit again when they move it. Keeping it out means the checked-in files are
// identical for every deployment of a given version.
//
// It is deliberately separate from secrets_names.go. The names below are not secrets
// and are not treated as one: nothing redacts them, and `AllSecretEnvVars` does not
// include them.
const (
	// EnvR2Endpoint is the Cloudflare R2 S3 endpoint for the account.
	EnvR2Endpoint = "STORVIA_R2_ENDPOINT"

	// EnvAWSEndpoint is the AWS S3 endpoint, used when AWS is the chosen fallback.
	// It is only needed for a non-AWS S3-compatible service such as MinIO; leave it
	// empty to let the AWS SDK use its own regional default.
	EnvAWSEndpoint = "STORVIA_AWS_ENDPOINT"
)

// EndpointEnvVar returns the environment variable holding a vendor's endpoint.
//
// Endpoint names are chosen by the vendor for the same reason the credential names
// are: a deployment cannot accidentally point an R2 backend at an AWS endpoint.
func EndpointEnvVar(vendor string) string {
	if vendor == VendorAWSS3 {
		return EnvAWSEndpoint
	}
	return EnvR2Endpoint
}

// AllEndpointEnvVars lists the endpoint variable names, so tooling can audit them
// without repeating the list.
func AllEndpointEnvVars() []string {
	return []string{EnvR2Endpoint, EnvAWSEndpoint}
}
