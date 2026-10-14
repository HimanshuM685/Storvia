package model

import (
	"testing"
	"time"
)

func TestLogicalObjectLocation(t *testing.T) {
	tests := []struct {
		name      string
		object    LogicalObject
		want      Provider
		wantError bool
	}{
		{
			name: "drive location",
			object: LogicalObject{
				Status: ObjectReady, Provider: ProviderGoogleDrive,
				DriveConnectionID: ptr("dc-1"), ProviderFileID: ptr("file-1"),
			},
			want: ProviderGoogleDrive,
		},
		{
			name: "s3 location",
			object: LogicalObject{
				Status: ObjectReady, Provider: ProviderS3Compatible,
				BackendID: ptr("b1"), ProviderObjectKey: ptr("storvia/key"),
			},
			want: ProviderS3Compatible,
		},
		{
			name: "ready with no location",
			object: LogicalObject{
				Status: ObjectReady, Provider: ProviderGoogleDrive,
			},
			wantError: true,
		},
		{
			name: "ready with both locations",
			object: LogicalObject{
				Status: ObjectReady, Provider: ProviderGoogleDrive,
				DriveConnectionID: ptr("dc-1"), ProviderFileID: ptr("file-1"),
				BackendID: ptr("b1"), ProviderObjectKey: ptr("key"),
			},
			wantError: true,
		},
		{
			name: "a pending object has no location yet",
			object: LogicalObject{
				Status: ObjectPending,
			},
			wantError: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.object.Location()
			if tc.wantError {
				if err == nil {
					t.Fatalf("Location() = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Location(): %v", err)
			}
			if got != tc.want {
				t.Errorf("Location() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLocationRefusesNonReadyStates keeps a caller from streaming an upload that
// has not finished.
func TestLocationRefusesNonReadyStates(t *testing.T) {
	for _, status := range []ObjectStatus{
		ObjectPending, ObjectUploading, ObjectOutcomeUnknown,
		ObjectBlocked, ObjectFailed, ObjectDeleting, ObjectDeleted, ObjectMissing,
	} {
		object := LogicalObject{
			Status: status, Provider: ProviderGoogleDrive,
			DriveConnectionID: ptr("dc-1"), ProviderFileID: ptr("file-1"),
		}
		if _, err := object.Location(); err == nil {
			t.Errorf("status %q must not yield a location", status)
		}
	}
}

func TestParseByteRange(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		wantNil  bool
		wantErr  bool
		start    int64
		end      int64
		hasRange bool
	}{
		{name: "empty header", header: "", wantNil: true},
		{name: "closed range", header: "bytes=0-99", start: 0, end: 100, hasRange: true},
		{name: "open ended range", header: "bytes=100-", start: 100, hasRange: true},
		{name: "with whitespace", header: " bytes=0-9 ", start: 0, end: 10, hasRange: true},
		{name: "wrong unit", header: "items=0-9", wantErr: true},
		{name: "multiple ranges", header: "bytes=0-9,20-29", wantErr: true},
		{name: "suffix range", header: "bytes=-100", wantErr: true},
		{name: "malformed", header: "bytes=abc-def", wantErr: true},
		{name: "no separator", header: "bytes=100", wantErr: true},
		{name: "negative start", header: "bytes=-5-10", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseByteRange(tc.header)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseByteRange(%q) = %+v, want an error", tc.header, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseByteRange(%q): %v", tc.header, err)
			}
			if tc.wantNil {
				if got != nil {
					t.Errorf("ParseByteRange(%q) = %+v, want nil", tc.header, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ParseByteRange(%q) = nil, want a range", tc.header)
			}
			if got.Start != tc.start {
				t.Errorf("Start = %d, want %d", got.Start, tc.start)
			}
			if got.End != tc.end {
				t.Errorf("End = %d, want %d", got.End, tc.end)
			}
			if got.HasRange != tc.hasRange {
				t.Errorf("HasRange = %v, want %v", got.HasRange, tc.hasRange)
			}
		})
	}
}

func TestByteRangeString(t *testing.T) {
	tests := []struct {
		name  string
		value *ByteRange
		want  string
	}{
		{"nil renders empty", nil, ""},
		{"no range renders empty", &ByteRange{}, ""},
		{"closed range is inclusive in HTTP terms", &ByteRange{Start: 0, End: 100, HasRange: true}, "bytes=0-99"},
		{"open ended", &ByteRange{Start: 500, HasRange: true}, "bytes=500-"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.value.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestByteRangeValid(t *testing.T) {
	const size = 1000
	tests := []struct {
		name  string
		value *ByteRange
		want  bool
	}{
		{"nil is always valid", nil, true},
		{"empty range is valid", &ByteRange{}, true},
		{"within bounds", &ByteRange{Start: 0, End: 100, HasRange: true}, true},
		{"exactly to the end", &ByteRange{Start: 900, End: 1000, HasRange: true}, true},
		{"start at the end is out of bounds", &ByteRange{Start: 1000, End: 1100, HasRange: true}, false},
		{"end past the size", &ByteRange{Start: 0, End: 2000, HasRange: true}, false},
		{"negative start", &ByteRange{Start: -1, End: 100, HasRange: true}, false},
		{"end before start", &ByteRange{Start: 500, End: 100, HasRange: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.value.Valid(size); got != tc.want {
				t.Errorf("Valid(%d) = %v, want %v", size, got, tc.want)
			}
		})
	}
}

// TestRangeRoundTrip checks that parsing and re-rendering a closed range yields
// the same header, which is what the provider receives.
func TestRangeRoundTrip(t *testing.T) {
	parsed, err := ParseByteRange("bytes=100-199")
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.String(); got != "bytes=100-199" {
		t.Errorf("round trip = %q, want bytes=100-199", got)
	}
}

func TestProviderValid(t *testing.T) {
	valid := []Provider{ProviderUnset, ProviderGoogleDrive, ProviderS3Compatible}
	for _, provider := range valid {
		if !provider.Valid() {
			t.Errorf("%q should be valid", provider)
		}
	}
	if Provider("azure_blob").Valid() {
		t.Error("an unknown provider must not be valid")
	}
}

// TestAvailabilityScopeKeyIsScoped proves the circuit key includes the connection
// and egress context, so one connection's outage cannot affect another.
func TestAvailabilityScopeKeyIsScoped(t *testing.T) {
	base := AvailabilityScope{
		TenantID:          "tenant-a",
		UserID:            "user-1",
		DriveConnectionID: "dc-1",
		EgressContext:     "eu-west",
	}

	variants := []struct {
		name   string
		mutate func(*AvailabilityScope)
	}{
		{"different tenant", func(s *AvailabilityScope) { s.TenantID = "tenant-b" }},
		{"different user", func(s *AvailabilityScope) { s.UserID = "user-2" }},
		{"different connection", func(s *AvailabilityScope) { s.DriveConnectionID = "dc-2" }},
		{"different egress context", func(s *AvailabilityScope) { s.EgressContext = "us-east" }},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			tc.mutate(&mutated)
			if mutated.Key() == base.Key() {
				t.Error("the circuit key must differ when the scope differs")
			}
		})
	}

	if base.Key() == "" {
		t.Error("the key must not be empty")
	}
}

func TestObjectMetadataSizeMatches(t *testing.T) {
	meta := ObjectMetadata{SizeBytes: 100}
	if !meta.SizeMatches(100) {
		t.Error("a matching size must report true")
	}
	if meta.SizeMatches(99) {
		t.Error("a mismatched size must report false; this is the ready-state gate")
	}
}

func TestUploadModeValuesAreDistinct(t *testing.T) {
	// The two modes are deliberately different values: a Drive resumable session
	// is not an S3 presigned URL, and a client that treats them as the same will
	// build the wrong transfer.
	if ModeDriveProxy == ModeS3PresignedParts {
		t.Error("the two upload modes must be distinct")
	}
	if string(ModeDriveProxy) != "drive_proxy" || string(ModeS3PresignedParts) != "s3_presigned_parts" {
		t.Error("the wire values of the upload modes are part of the API contract")
	}
}

func TestUploadStateZeroValueIsSafe(t *testing.T) {
	var state UploadState
	if state.Exists || state.Completed || state.Expired {
		t.Error("the zero UploadState must mean nothing is known, not that something completed")
	}
}

func TestFullRangeIsNil(t *testing.T) {
	if FullRange() != nil {
		t.Error("FullRange must be nil so callers pass no range at all")
	}
}

// ptr returns a pointer to a string, used by the fixtures above.
func ptr(value string) *string { return &value }

// keep the time import used by the domain fixtures.
var _ = time.Time{}
