# Storvia TypeScript SDK

Client for the Storvia HTTP API, for browsers and Node.

```sh
npm install
npm run check     # type-check
npm test          # run the test suite
npm run build     # emit dist/
```

## Creating a client

```ts
import { StorviaClient } from '@storvia/sdk';

const client = new StorviaClient({
  baseUrl: 'https://api.storvia.example',
  // Omit `token` in a browser: the session cookie is sent automatically.
  token: import.meta.env.VITE_STORVIA_TOKEN,
});
```

## Uploading

The idempotency key is not optional. A retry with a fresh key creates a second
provider write, which is the duplicate-file failure the API exists to prevent.

```ts
const result = await client.createUpload(
  { name: 'report.pdf', mime_type: 'application/pdf', size_bytes: 10 << 20, category: 'report' },
  { idempotencyKey: `upload-${crypto.randomUUID()}` },
);

// The server chose the provider. Do not try to influence this.
if (result.mode === 's3_presigned_parts') {
  // Upload parts straight to the provider with result.parts.
  // Those URLs are bearer tokens: do not log or store them.
} else {
  // drive_resumable_session: the session URL in result.session is a write
  // capability for this one file. PUT each chunk to it with a Content-Range.
}
```

The `upload` helper does the whole transfer for either mode. In Drive mode it streams
the chunks to Google's session and reports the file id Google returned, which is the
only way Storvia can learn where the file went — it deliberately does not keep the
session itself.

```ts
const { object } = await client.upload(
  { name: 'report.pdf', mime_type: 'application/pdf', size_bytes: file.size, category: 'report' },
  file,
  { idempotencyKey },
);
```

If a transfer is interrupted, do not simply start again. Ask first:

```ts
const result = await client.reconcileUpload(uploadId);
if (result.outcome === 'nothing_stored') {
  // Safe to start over. Only this outcome guarantees it.
}
```

`unknown` means nobody can tell, and restarting on an `unknown` is exactly how a
duplicate file gets created.

## Error handling

Every failure carries a machine-readable kind, so the UI never parses prose.

```ts
import { StorviaError, TransportError } from '@storvia/sdk';

try {
  await client.createUpload(request, { idempotencyKey });
} catch (error) {
  if (error instanceof TransportError) {
    // The write may have committed. Reconcile with the same key; do not start a
    // new upload.
  } else if (error instanceof StorviaError) {
    switch (error.kind) {
      case 'quota':
        // Ask the user to free space. Retrying will not help.
        break;
      case 'consent_required':
        // Send the user through reconnect.
        if (error.needsReconnect) await reconnectDrive();
        break;
      case 'uncertain':
        // outcomeUnknown is true: reconcile, never re-upload elsewhere.
        break;
      case 'unavailable':
        // temporary is true: a retry may succeed.
        break;
    }
    console.error(error.requestId); // quote this in a bug report
  }
}
```

`error.temporary` reports whether an identical retry could succeed. It is false
for quota, consent, and validation failures.

`error.outcomeUnknown` is the flag that matters most: when it is set, the request
may already have been processed.

## Showing the storage disclosure

`status()` returns whether Storvia can accept an upload and, importantly, whether
fallback uploads may be stored outside the user's Drive:

```ts
const status = await client.status();
if (status.fallback_stores_outside_drive) {
  // Show the disclosure rather than implying Drive-only storage.
}
```

A file uploaded during a Drive outage stays in the company-managed fallback and is
read and deleted from there, even after Drive recovers. There is no second copy.

## Downloading

```ts
const { response } = await client.download(objectId);
// Read response.body, or use download(objectId, { start: 0, end: 1024 }).
```

Storvia streams from the file's recorded location only. A provider outage surfaces
as an error rather than as a transparent retry.