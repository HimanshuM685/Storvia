package secrets

import (
	"encoding/base64"
	"encoding/hex"
)

// hexDecode and base64Decode are thin wrappers kept here so the key decoding
// logic has no scattered encoding imports.
func hexDecode(value string) ([]byte, error) { return hex.DecodeString(value) }

func base64Decode(value string) ([]byte, error) { return base64.StdEncoding.DecodeString(value) }

// StringRef builds a key reference from a configuration field.
//
// A reference is a name such as "kms://storvia/prod/drive", never the key itself.
// Storing the reference in a row is what makes rotation possible without a
// rewrite.
func StringRef(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}
