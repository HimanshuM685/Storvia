# Storvia Go SDK

Client for the Storvia HTTP API.

```go
import "github.com/shovan04/storvia/sdk/go"
```

## Creating a client

```go
client, err := storvia.New(storvia.Config{
    BaseURL: "https://api.storvia.example",
    Token:   os.Getenv("STORVIA_TOKEN"),
})
if err != nil {
    return err
}
```

`HTTPClient` is shared across calls, so connections are reused. Pass one with a
longer timeout when uploading large files.

## Uploading

The idempotency key is not optional in practice. A browser or proxy retry with a
fresh key creates a second provider write, which is the duplicate-file failure the
API exists to prevent.

```go
result, err := client.CreateUpload(ctx, "upload-2026-10-14-0001", storvia.CreateUploadRequest{
    Name:      "report.pdf",
    MIMEType:  "application/pdf",
    SizeBytes: 10 << 20,
    Category:  "report",
})
if err != nil {
    return err
}

// The server chose the provider. Do not try to influence this.
switch result.Mode {
case storvia.ModeDriveResumableSession:
	// The session URL in result.Session is a write capability for this one file.
	// upload.UploadAll streams the chunks to it with per-chunk Content-Range,
	// then upload.CompleteDrive(ctx, driveFileID) sends back the id Google returned.
case storvia.ModeS3PresignedParts:
	// Upload parts directly with result.Parts; those URLs are bearer tokens.
}
```

Resuming: `Upload.UploadedBytes` reports what this client has sent. It is **local
accounting, not a provider-confirmed offset** — Storvia does not hold the Drive session,
so it cannot say what Google durably accepted. Read the comment on that method before
relying on it.
assume more bytes were accepted than the provider confirmed.

## Error handling

Every failure carries a machine-readable kind, so the client never parses prose.

```go
if err != nil {
    var apiErr *storvia.APIError
    if errors.As(err, &apiErr) {
        switch apiErr.Kind {
        case storvia.KindQuota:
            // Ask the user to free space. Retrying will not help.
        case storvia.KindConsentRequired:
            // Send the user through reconnect.
        case storvia.KindUncertain:
            // The write may have committed. Reconcile; do NOT start a new upload.
        case storvia.KindUnavailable:
            // Retryable. For a download, note there is no second copy to serve from.
        }
    }
}
```

`APIError.Temporary()` reports whether an identical retry could succeed. It is
false for quota, consent, and validation failures.

`APIError.RequestID` correlates the failure with the server's logs. Quote it in a
bug report.

### Transport failures are not failures

A network error produces `KindUncertain`, not a generic error. The request may have
been processed before the response was lost. Retrying with the same idempotency key
is safe; starting a new upload is not.

## Downloading

```go
download, err := client.Download(ctx, objectID)
if err != nil {
    return err
}
defer download.Close()

io.Copy(destination, download.Body)
```

`DownloadRange` sends an inclusive HTTP range. Storvia streams from the file's
recorded location only. If that provider is unavailable the call fails, because
there is no second copy to fall back to.

## Deleting

`DeleteObject` deletes from the file's recorded location. A file uploaded during a
Drive outage is removed from the fallback backend, because that is where the only
copy lives.

## Running the tests

```sh
go test ./sdk/go/...
```

The tests run against an `httptest` stub and cover the wire contract: the
idempotency header, RFC 9457 error decoding, unknown-outcome classification, and
range handling.