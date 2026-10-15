import type { RoutingChoice, State, UploadResult } from './types';

export class ApiError extends Error {
  readonly status: number;
  readonly detail?: string;

  constructor(message: string, status: number, detail?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.detail = detail;
  }
}

/**
 * Where the API is.
 *
 * The console is a client, never a proxy. In development the API is on a different port
 * and Go answers cross-origin; in an embedded release it is the same origin. Both are the
 * same code path, because cookies are port-agnostic -- the session Go sets is sent on
 * every request, whichever port it was set from.
 */
export const API_ORIGIN =
  process.env.NEXT_PUBLIC_API_ORIGIN?.replace(/\/$/, '') ?? '';

export const API_BASE = `${API_ORIGIN}/console/api`;

/** Absolute Go URL, used for anything the browser must navigate to rather than fetch.
 *  Sign-in in particular: it is a round trip through Google, not a request. */
export function apiUrl(path: string): string {
  return `${API_ORIGIN}${path}`;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    // The session cookie is what authenticates the console, so it must ride along.
    // 'include' rather than 'same-origin': in development the API is on another
    // port, and without it the browser would send no session cookie and every call
    // would look signed out.
    credentials: 'include',
    cache: 'no-store',
  });

  if (response.status === 204) return undefined as T;

  let body: unknown = null;
  try {
    body = await response.json();
  } catch {
    // An empty or non-JSON body is not fatal on its own; the status decides.
  }

  if (!response.ok) {
    const record = (body ?? {}) as Record<string, unknown>;
    throw new ApiError(
      typeof record.error === 'string' ? record.error : `Request failed (${response.status})`,
      response.status,
      typeof record.detail === 'string' ? record.detail : undefined,
    );
  }
  return body as T;
}

const json = (payload: unknown): RequestInit => ({
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(payload),
});

export const api = {
  state: () => request<State>(`${API_BASE}/state`),

  setRouting: (choice: RoutingChoice) =>
    request<State['routing']>(`${API_BASE}/routing`, json({
      // `null` means "stop overriding and follow the configured default".
      forced: choice === 'forced' ? true : choice === 'drive' ? false : null,
    })),

  setFaults: (enabled: boolean) =>
    request<State['faults']>(`${API_BASE}/faults`, json({ enabled })),

  setConsent: (granted: boolean) =>
    request<State['consent']>(`${API_BASE}/consent`, json({ granted })),

  ensureFolder: () => request<{ created: boolean }>(`${API_BASE}/folder`, json({})),

  disconnect: () => request<{ disconnected: boolean }>(`${API_BASE}/disconnect`, json({})),

  deleteObject: (id: string) =>
    request<{ deleted: string }>(`/console/api/objects/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),

  upload: (file: File, onProgress?: (fraction: number) => void) =>
    new Promise<UploadResult>((resolve, reject) => {
      const form = new FormData();
      form.append('file', file);

      // XHR rather than fetch: this is the one call where progress matters, and fetch
      // still cannot report upload progress.
      const xhr = new XMLHttpRequest();
      xhr.open('POST', `${API_BASE}/upload`);
      xhr.withCredentials = true;

      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable && onProgress) {
          onProgress(event.loaded / event.total);
        }
      };

      xhr.onload = () => {
        let body: Record<string, unknown> = {};
        try {
          body = JSON.parse(xhr.responseText);
        } catch {
          /* handled by the status check below */
        }
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(body as unknown as UploadResult);
          return;
        }
        reject(new ApiError(
          typeof body.error === 'string' ? body.error : `Upload failed (${xhr.status})`,
          xhr.status,
          typeof body.detail === 'string' ? body.detail : undefined,
        ));
      };

      xhr.onerror = () => reject(new ApiError('The upload request failed', 0));
      xhr.send(form);
    }),
};

export function downloadUrl(objectId: string): string {
  return `${API_BASE}/objects/${encodeURIComponent(objectId)}/content`;
}

export function formatBytes(value: number | undefined): string {
  if (value === undefined || value === null) return '—';
  if (value < 1024) return `${value} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let scaled = value / 1024;
  let unit = 0;
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  return `${scaled.toFixed(scaled >= 10 ? 0 : 1)} ${units[unit]}`;
}
