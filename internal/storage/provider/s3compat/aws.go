package s3compat

import (
	"github.com/aws/aws-sdk-go-v2/aws"
)

// The helpers below wrap the SDK's pointer helpers so the call sites read
// cleanly. They are thin on purpose: no business logic belongs in a shim.

func awsString(v string) *string { return aws.String(v) }

func awsInt32(v int32) *int32 { return aws.Int32(v) }

func awsInt64(v int64) *int64 { return aws.Int64(v) }
