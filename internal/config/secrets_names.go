package config

// Canonical environment variable names for secrets.
//
// The names are a code-level convention, not configuration. They deliberately do
// not appear in the JSON files: a checked-in config should contain ordinary
// settings only, and a secret's location should be the environment itself, not a
// field in a file that gets reviewed and committed.
//
// The actual values live only in an ignored `.env` file or runtime secret
// injection. These constants name *where to look*; they are not secrets and
// nothing sensitive is derived from them.
const (
	// EnvDatabasePassword is the PostgreSQL password. The host, port, user, and
	// database name are ordinary configuration and live in the JSON.
	EnvDatabasePassword = "STORVIA_DATABASE_PASSWORD"

	// EnvRedisPassword is the Redis password. The address is ordinary
	// configuration.
	EnvRedisPassword = "STORVIA_REDIS_PASSWORD"

	// EnvSessionSigningKey is the HMAC key used to sign session cookies.
	EnvSessionSigningKey = "STORVIA_SESSION_SIGNING_KEY"

	// EnvEncryptionKey is the AES-256-GCM key that encrypts stored Drive refresh
	// tokens. EnvEncryptionKeyPrevious is its predecessor during a rotation.
	EnvEncryptionKey         = "STORVIA_ENCRYPTION_KEY"
	EnvEncryptionKeyPrevious = "STORVIA_ENCRYPTION_KEY_PREVIOUS"

	// EnvGoogleClientID and EnvGoogleClientSecret are the Google OAuth web
	// application credentials. The redirect URI is ordinary configuration.
	EnvGoogleClientID     = "STORVIA_GOOGLE_CLIENT_ID"
	EnvGoogleClientSecret = "STORVIA_GOOGLE_CLIENT_SECRET"

	// EnvR2AccessKeyID and EnvR2SecretAccessKey are the Cloudflare R2
	// credentials. EnvR2SessionToken is only set for temporary credentials. The
	// account endpoint is ordinary configuration and lives in the JSON.
	EnvR2AccessKeyID     = "STORVIA_R2_ACCESS_KEY_ID"
	EnvR2SecretAccessKey = "STORVIA_R2_SECRET_ACCESS_KEY"
	EnvR2SessionToken    = "STORVIA_R2_SESSION_TOKEN"

	// EnvAWSAccessKeyID and EnvAWSSecretAccessKey are the AWS S3 credentials, used
	// when AWS is the chosen fallback instead of R2.
	EnvAWSAccessKeyID     = "STORVIA_AWS_ACCESS_KEY_ID"
	EnvAWSSecretAccessKey = "STORVIA_AWS_SECRET_ACCESS_KEY"
)

// AllSecretEnvVars lists every secret variable name, so tooling and tests can
// audit the boundary without repeating the list.
func AllSecretEnvVars() []string {
	return []string{
		EnvDatabasePassword,
		EnvRedisPassword,
		EnvSessionSigningKey,
		EnvEncryptionKey,
		EnvEncryptionKeyPrevious,
		EnvGoogleClientID,
		EnvGoogleClientSecret,
		EnvR2AccessKeyID,
		EnvR2SecretAccessKey,
		EnvR2SessionToken,
		EnvAWSAccessKeyID,
		EnvAWSSecretAccessKey,
	}
}
