/**
 * Storvia API client for TypeScript and JavaScript.
 *
 * The client mirrors the server's error vocabulary on purpose. An upload whose
 * outcome is unknown surfaces as {@link StorviaError} with kind `uncertain`, not as
 * a generic failure, because the correct action differs completely: reconcile the
 * existing upload, never start a new one elsewhere.
 */

/** Machine-readable failure class, matching the server's problem types. */
export type ErrorKind =
  | 'validation'
  | 'authentication'
  | 'authorization'
  | 'not_found'
  | 'conflict'
  | 'quota'
  | 'rate_limited'
  | 'unavailable'
  | 'uncertain'
  | 'provider_not_configured'
  | 'consent_required'
  | 'internal';

/** The transfer mechanism the server selected. The client never chooses a provider. */
export type UploadMode = 'drive_resumable_session' | 's3_presigned_parts';

/**
 * A Drive resumable upload session.
 *
 * `url` is a write capability for exactly one file: anyone holding it can write to
 * that file until the session expires. Treat it as a secret. Storvia hands it out once
 * and never stores it, so it cannot be recovered if lost.
 */
export interface DriveSession {
  url: string;
  chunk_size: number;
  expires_at: string;
}

/** What the server found when it asked the provider about an unfinished upload. */
export interface ReconcileResult {
  upload_id: string;
  object_id: string;
  provider: string;
  state: string;
  /**
   * - `already_ready` the upload finished long ago
   * - `completed` the provider has the bytes; Storvia recorded that
   * - `nothing_stored` the provider is certain nothing landed; safe to retry
   * - `size_mismatch` something is there but not what was declared
   * - `unknown` nobody can tell, which is NOT the same as `nothing_stored`
   *
   * Only `nothing_stored` means it is safe to start the upload again.
   */
  outcome: string;
  reason?: string;
  location?: string;
}

/** The reply from the complete endpoint. */
interface CompleteResponse {
  object: StorviaObject;
  already_complete: boolean;
}

/** RFC 9457 problem details, as returned for every failure. */
export interface Problem {
  type: string;
  title: string;
  status: number;
  detail: string;
  instance?: string;
  request_id?: string;
  fields?: Record<string, string>;
}

/** An error returned by the Storvia API. */
export class StorviaError extends Error {
  readonly kind: ErrorKind;
  readonly status: number;
  readonly title: string;
  readonly detail: string;
  readonly requestId: string;
  readonly fields: Record<string, string>;

  constructor(init: {
    kind: ErrorKind;
    status: number;
    title: string;
    detail: string;
    requestId?: string;
    fields?: Record<string, string>;
  }) {
    super(`${init.kind}: ${init.detail}`);
    this.name = 'StorviaError';
    this.kind = init.kind;
    this.status = init.status;
    this.title = init.title;
    this.detail = init.detail;
    this.requestId = init.requestId ?? '';
    this.fields = init.fields ?? {};
  }

  /**
   * Whether retrying the identical call could succeed.
   *
   * Quota, consent, and validation failures return false: retrying them burns the
   * user's quota or their patience without changing the outcome.
   */
  get temporary(): boolean {
    return (
      this.kind === 'unavailable' ||
      this.kind === 'rate_limited' ||
      this.kind === 'uncertain' ||
      this.kind === 'internal'
    );
  }

  /**
   * Whether the request may or may not have been processed.
   *
   * This is the dangerous case: the write may have committed. Retry the same
   * operation with the same idempotency key, or reconcile; do not start a new
   * upload.
   */
  get outcomeUnknown(): boolean {
    return this.kind === 'uncertain';
  }

  /** Whether a reconnect of the Drive account is what the user needs to do. */
  get needsReconnect(): boolean {
    return this.kind === 'consent_required';
  }
}

/** A failure raised before a response was received. */
export class TransportError extends Error {
  /** Always true: a transport failure cannot prove the server did not act. */
  readonly outcomeUnknown = true;

  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'TransportError';
  }
}

/** One short-lived upload capability. Treat the URL as a bearer token. */
export interface PresignedPart {
  part_number: number;
  url: string;
  headers?: Record<string, string>;
  expires_at: string;
}

/** A new upload request. */
export interface CreateUploadRequest {
  /** Display name. It is never used as a storage key. */
  name: string;
  /** Untrusted metadata used for routing and download headers. */
  mime_type: string;
  size_bytes: number;
  category: string;
}

/** The server's routing decision plus transfer instructions. */
export interface CreateUploadResult {
  upload_id: string;
  object_id: string;
  mode: UploadMode;
  /** Set in Drive mode. Its URL is a write capability for this one file. */
  session?: DriveSession;
  provider: string;
  state: string;
  /** True when this answers an idempotent repeat rather than a new provider write. */
  replayed: boolean;
  /** Non-empty only when Drive was unavailable and the fallback was used. */
  fallback_reason?: string;
  chunk_size: number;
  uploaded_bytes: number;
  expires_at?: string;
  parts?: PresignedPart[];
}

/** The state of an upload. */
export interface UploadView {
  upload_id: string;
  object_id: string;
  state: string;
  provider: string;
  fallback_reason?: string;
  uploaded_bytes: number;
  expected_bytes: number;
  attempt_state: string;
  /** When true the upload is pinned to its provider and must not be restarted. */
  mutation_may_have_started: boolean;
  created_at: string;
  updated_at: string;
}

/** A stored file. Provider identifiers are deliberately absent. */
export interface StorviaObject {
  object_id: string;
  name: string;
  mime_type: string;
  size_bytes: number;
  status: string;
  provider: string;
  category: string;
  fallback_reason?: string;
  verified_at?: string;
  created_at: string;
}

/** Whether Storvia can accept an upload, and why not when it cannot. */
export interface StorageStatus {
  primary_provider: string;
  drive_connected: boolean;
  fallback_enabled: boolean;
  fallback_vendor?: string;
  fallback_ready: boolean;
  fallback_requires_consent: boolean;
  /** The single answer needed before offering an upload control. */
  accepts_uploads: boolean;
  blocked_reason?: string;
  /** True when outage uploads may be stored outside the user's Drive. */
  fallback_stores_outside_drive: boolean;
}

/** A Drive connection, reported without any credential. */
export interface DriveConnection {
  connected: boolean;
  status: string;
  subject?: string;
  granted_scopes?: string[];
  folder_present: boolean;
  last_success_at?: string;
  stale: boolean;
}

/** Options for the client. */
export interface StorviaClientOptions {
  /** API root, for example `https://api.storvia.example`. */
  baseUrl: string;
  /** Session credential. Omit when the browser relies on the session cookie. */
  token?: string;
  /** Custom fetch implementation, mainly for tests and server runtimes. */
  fetch?: typeof fetch;
  /** Extra headers applied to every request. */
  headers?: Record<string, string>;
}

/** Options for a single request. */
export interface RequestOptions {
  /** Abort the request. */
  signal?: AbortSignal;
  /** Stable across retries of the same logical request. */
  idempotencyKey?: string;
}

const PROBLEM_PREFIX = 'https://storvia.dev/problems/';

const KNOWN_KINDS: ReadonlySet<string> = new Set<ErrorKind>([
  'validation',
  'authentication',
  'authorization',
  'not_found',
  'conflict',
  'quota',
  'rate_limited',
  'unavailable',
  'uncertain',
  'provider_not_configured',
  'consent_required',
  'internal',
]);

/**
 * Maps a problem type to a known kind.
 *
 * An unregistered type degrades to `internal` rather than being trusted verbatim:
 * a caller branching on a kind it does not understand would take the wrong action.
 */
function kindFromProblemType(type: string): ErrorKind {
  if (!type.startsWith(PROBLEM_PREFIX)) {
    return 'internal';
  }
  const kind = type.slice(PROBLEM_PREFIX.length);
  return KNOWN_KINDS.has(kind) ? (kind as ErrorKind) : 'internal';
}

/** Maps a status to a kind when no problem document is available. */
function kindForStatus(status: number): ErrorKind {
  switch (status) {
    case 400:
    case 422:
      return 'validation';
    case 401:
      return 'authentication';
    case 403:
      return 'authorization';
    case 404:
      return 'not_found';
    case 409:
      return 'conflict';
    case 428:
      return 'consent_required';
    case 424:
      return 'provider_not_configured';
    case 429:
      return 'rate_limited';
    case 503:
      return 'unavailable';
    default:
      return 'internal';
  }
}

/** The Storvia API client. */
export class StorviaClient {
  private readonly baseUrl: string;
  private readonly token: string | undefined;
  private readonly fetchImpl: typeof fetch;
  private readonly extraHeaders: Record<string, string>;

  constructor(options: StorviaClientOptions) {
    if (!options.baseUrl) {
      throw new Error('storvia: a baseUrl is required');
    }
    const parsed = new URL(options.baseUrl);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      throw new Error(`storvia: baseUrl must be http or https, got ${parsed.protocol}`);
    }
    this.baseUrl = parsed.toString().replace(/\/$/, '');
    this.token = options.token;
    this.fetchImpl = options.fetch ?? globalThis.fetch;
    this.extraHeaders = options.headers ?? {};
  }

  /** Performs a request and decodes the response. */
  private async request<T>(
    method: string,
    path: string,
    init: {
      body?: unknown;
      raw?: BodyInit;
      contentType?: string;
      options?: RequestOptions;
    } = {},
  ): Promise<T> {
    const headers: Record<string, string> = {
      Accept: 'application/json',
      ...this.extraHeaders,
    };
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }
    if (init.options?.idempotencyKey) {
      headers['Idempotency-Key'] = init.options.idempotencyKey;
    }

    let body: BodyInit | undefined;
    if (init.raw !== undefined) {
      body = init.raw;
      headers['Content-Type'] = init.contentType ?? 'application/octet-stream';
    } else if (init.body !== undefined) {
      body = JSON.stringify(init.body);
      headers['Content-Type'] = 'application/json';
    }

    let response: Response;
    try {
      response = await this.fetchImpl(`${this.baseUrl}${path}`, {
        method,
        headers,
        body,
        // Cookies are sent when the browser holds a session; the bearer header
        // covers server-to-server callers.
        credentials: 'include',
        signal: init.options?.signal,
      });
    } catch (cause) {
      // A transport failure cannot prove the server did not process the request.
      // Reporting it as an unknown outcome is what prevents a client-side retry
      // from creating a duplicate file.
      throw new TransportError('storvia: the request did not complete', { cause });
    }

    if (!response.ok) {
      throw await this.decodeError(response);
    }

    if (response.status === 204) {
      return undefined as T;
    }
    const text = await response.text();
    if (!text) {
      return undefined as T;
    }
    return JSON.parse(text) as T;
  }

  /** Turns an error response into a StorviaError. */
  private async decodeError(response: Response): Promise<StorviaError> {
    let payload: Problem | undefined;
    try {
      const text = await response.text();
      payload = text ? (JSON.parse(text) as { error?: Problem }).error : undefined;
    } catch {
      // A non-JSON body means something in front of the API failed. The status is
      // still usable; the upstream body is deliberately not surfaced.
      payload = undefined;
    }

    if (!payload) {
      return new StorviaError({
        kind: kindForStatus(response.status),
        status: response.status,
        title: response.statusText || 'Request failed',
        detail: 'the API returned a response that could not be parsed',
        requestId: response.headers.get('X-Request-ID') ?? undefined,
      });
    }

    return new StorviaError({
      kind: kindFromProblemType(payload.type),
      status: payload.status || response.status,
      title: payload.title,
      detail: payload.detail,
      requestId: payload.request_id ?? response.headers.get('X-Request-ID') ?? undefined,
      fields: payload.fields,
    });
  }

  /** Reports whether Storvia can accept an upload, and why not when it cannot. */
  status(options?: RequestOptions): Promise<StorageStatus> {
    return this.request<StorageStatus>('GET', '/v1/storage/status', { options });
  }

  /**
   * Starts a new upload.
   *
   * `idempotencyKey` must be stable across retries of the same logical request. A
   * fresh key per attempt defeats the protection and can create duplicate files.
   */
  async createUpload(
    request: CreateUploadRequest,
    options?: RequestOptions,
  ): Promise<CreateUploadResult> {
    if (!options?.idempotencyKey) {
      // Raised as a rejection rather than a synchronous throw so callers only
      // need one error path.
      throw new Error(
        'storvia: an idempotencyKey is required; a fresh key per attempt can create duplicate files',
      );
    }
    return this.request<CreateUploadResult>('POST', '/v1/storage/uploads', {
      body: request,
      options,
    });
  }

  /** Reads an upload's state. */
  getUpload(uploadId: string, options?: RequestOptions): Promise<UploadView> {
    return this.request<UploadView>('GET', `/v1/storage/uploads/${encodeURIComponent(uploadId)}`, {
      options,
    });
  }

  /**
   * Abandons an upload that has not begun writing bytes.
   *
   * The server refuses this once the upload may already have written data. That
   * refusal is correct and must not be worked around by starting a new upload.
   */
  cancelUpload(uploadId: string, options?: RequestOptions): Promise<void> {
    return this.request<void>(
      'DELETE',
      `/v1/storage/uploads/${encodeURIComponent(uploadId)}`,
      { options },
    );
  }

  /**
   * Finalizes an upload.
   *
   * For the Drive mode, `driveFileId` is the id Google returned in the final chunk
   * response. Storvia treats it as untrusted input and re-reads the file from Drive,
   * adopting it only if it sits in the Storvia folder, is not trashed, and has the
   * declared size.
   *
   * Idempotent: repeating it returns the existing object.
   */
  async completeUpload(
    uploadId: string,
    driveFileId?: string,
    options?: RequestOptions,
  ): Promise<StorviaObject> {
    const response = await this.request<CompleteResponse>(
      'POST',
      `/v1/storage/uploads/${encodeURIComponent(uploadId)}/complete`,
      { body: driveFileId ? { drive_file_id: driveFileId } : undefined, options },
    );
    return response.object;
  }

  /**
   * Asks the server what actually happened to an unfinished upload.
   *
   * It reports; it never retries and never re-uploads. Read the outcome before
   * starting over: an upload that is merely unknown may well have succeeded.
   */
  async reconcileUpload(
    uploadId: string,
    options?: RequestOptions,
  ): Promise<ReconcileResult> {
    return this.request<ReconcileResult>(
      'POST',
      `/v1/storage/uploads/${encodeURIComponent(uploadId)}/reconcile`,
      { options },
    );
  }

  /** Reads an object's metadata. */
  getObject(objectId: string, options?: RequestOptions): Promise<StorviaObject> {
    return this.request<StorviaObject>(
      'GET',
      `/v1/storage/objects/${encodeURIComponent(objectId)}`,
      { options },
    );
  }

  /**
   * Removes an object from its recorded location.
   *
   * The deletion is pinned to wherever the file was actually written. A file
   * uploaded during a Drive outage is deleted from the fallback, because that is
   * where the only copy lives.
   */
  deleteObject(objectId: string, options?: RequestOptions): Promise<void> {
    return this.request<void>('DELETE', `/v1/storage/objects/${encodeURIComponent(objectId)}`, {
      options,
    });
  }

  /**
   * Streams an object's content.
   *
   * There is no second location to fall back to, so a provider outage surfaces as
   * an error rather than as a transparent retry. The caller owns the response body.
   */
  async download(
    objectId: string,
    range?: { start: number; end?: number },
    options?: RequestOptions,
  ): Promise<{ response: Response; mimeType: string; sizeBytes: number }> {
    const headers: Record<string, string> = { ...this.extraHeaders };
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }
    if (range && range.start >= 0) {
      // HTTP ranges are inclusive at the end.
      headers['Range'] =
        range.end !== undefined ? `bytes=${range.start}-${range.end - 1}` : `bytes=${range.start}-`;
    }

    let response: Response;
    try {
      response = await this.fetchImpl(
        `${this.baseUrl}/v1/storage/objects/${encodeURIComponent(objectId)}/content`,
        { method: 'GET', headers, credentials: 'include', signal: options?.signal },
      );
    } catch (cause) {
      throw new TransportError('storvia: the download did not start', { cause });
    }

    if (!response.ok) {
      throw await this.decodeError(response);
    }

    const sizeHeader = response.headers.get('Content-Length');
    return {
      response,
      mimeType: response.headers.get('Content-Type') ?? 'application/octet-stream',
      sizeBytes: sizeHeader ? Number.parseInt(sizeHeader, 10) : -1,
    };
  }

  /** Reads the Drive connection state. */
  getDriveConnection(options?: RequestOptions): Promise<DriveConnection> {
    return this.request<DriveConnection>('GET', '/v1/storage/connections/google-drive', {
      options,
    });
  }

  /**
   * Begins the Drive consent flow.
   *
   * Send the browser to the returned URL. It is shown once and should not be logged.
   */
  async startDriveConnection(options?: RequestOptions): Promise<string> {
    const result = await this.request<{ authorize_url: string; expires_in_seconds: number }>(
      'POST',
      '/v1/storage/connections/google-drive/start',
      { options },
    );
    if (!result.authorize_url) {
      throw new Error('storvia: the server returned an empty consent URL');
    }
    return result.authorize_url;
  }

  /**
   * Removes the Drive connection.
   *
   * New Drive requests and new uploads stop, and the user's own Drive files are
   * left untouched.
   */
  disconnectDrive(options?: RequestOptions): Promise<void> {
    return this.request<void>('DELETE', '/v1/storage/connections/google-drive', { options });
  }

  /**
   * Uploads a file through the Drive proxy path, chunk by chunk.
   *
   * The chunk boundaries come from the server. On an unknown outcome the helper
   * stops and reports it rather than retrying, because retrying an upload whose
   * bytes may already be at the provider is how duplicates are created.
   */
  async upload(
    request: CreateUploadRequest,
    body: Blob,
    options?: RequestOptions,
  ): Promise<{ result: CreateUploadResult; object: StorviaObject }> {
    const result = await this.createUpload(request, options);

    if (result.mode === 's3_presigned_parts') {
      // The presigned flow uploads straight to the provider with the returned
      // capabilities, so there is nothing for this helper to stream.
      return { result, object: await this.completeUpload(result.upload_id, undefined, options) };
    }

    const session = result.session;
    if (!session || !session.url) {
      throw new StorviaError({
        kind: 'unavailable',
        status: 502,
        title: 'No Drive session',
        detail: 'The server did not return a Drive resumable session for this upload.',
      });
    }

    const driveFileId = await this.uploadToDriveSession(session, result.chunk_size, body);

    // Google returns the created file in the final chunk response. That id is the
    // only way Storvia can learn where the file went, because it deliberately does
    // not keep the session itself.
    return {
      result,
      object: await this.completeUpload(result.upload_id, driveFileId, options),
    };
  }

  /**
   * Streams a file into a Drive resumable session and returns the resulting file id.
   *
   * Every chunk except the last is a multiple of 256 KiB, because Drive rejects
   * anything else, and each chunk declares its own byte range. Without that range a
   * retry cannot be distinguished from the original and the file ends up with a
   * hole in it.
   */
  private async uploadToDriveSession(
    session: DriveSession,
    chunkSize: number,
    body: Blob,
  ): Promise<string> {
    const size = Math.max(chunkSize || session.chunk_size || 256 * 1024, 1);
    let offset = 0;
    let fileId: string | undefined;

    while (offset < body.size) {
      const end = Math.min(offset + size, body.size);
      const slice = body.slice(offset, end);

      const response = await fetch(session.url, {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/octet-stream',
          'Content-Range': `bytes ${offset}-${end - 1}/${body.size}`,
        },
        body: slice,
      });

      // 308 asks for more; 200 and 201 mean the file is finished and carry the
      // created resource.
      if (response.status === 200 || response.status === 201) {
        const created = (await response.json()) as { id?: string };
        fileId = created.id;
      } else if (response.status !== 308) {
        throw new StorviaError({
          kind: 'unavailable',
          status: 502,
          title: 'Drive rejected a chunk',
          detail:
            `Google Drive rejected the byte range ${offset}-${end - 1}/${body.size} ` +
            `with status ${response.status}.`,
        });
      }
      offset = end;
    }

    if (!fileId) {
      throw new StorviaError({
        kind: 'unavailable',
        status: 502,
        title: 'No file id',
        detail: 'Google Drive did not return a file id, so the upload cannot be completed.',
      });
    }
    return fileId;
  }
}
