'use client';

import { useRef, useState } from 'react';
import { api, formatBytes } from '@/lib/api';
import type { UploadResult } from '@/lib/types';
import { Banner, Card, Pill } from './ui';

type Props = {
  onUploaded: () => void;
  willRouteTo: string;
  acceptsUploads: boolean;
  blockedReason?: string;
};

export function UploadPanel({ onUploaded, willRouteTo, acceptsUploads, blockedReason }: Props) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [result, setResult] = useState<UploadResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function handleUpload() {
    const file = inputRef.current?.files?.[0];
    if (!file) {
      setError('Choose a file first.');
      return;
    }
    setBusy(true);
    setError(null);
    setResult(null);
    setProgress(0);
    try {
      const uploaded = await api.upload(file, setProgress);
      setResult(uploaded);
      onUploaded();
    } catch (e) {
      const err = e as { message?: string; detail?: string };
      setError(err.detail ?? err.message ?? 'The upload failed.');
    } finally {
      setBusy(false);
      setProgress(null);
    }
  }

  return (
    <Card
      title="Upload a file"
      hint="Runs the whole client sequence: create, transfer to the provider, verify, commit."
    >
      {!acceptsUploads && blockedReason ? (
        <Banner tone="error" title="Uploads are currently refused.">
          {blockedReason}
        </Banner>
      ) : null}

      <div className="row">
        <label className="field" style={{ flex: '1 1 260px' }}>
          File
          <input ref={inputRef} type="file" disabled={busy} />
        </label>
        <button onClick={handleUpload} disabled={busy || !acceptsUploads}>
          {busy ? 'Uploading…' : 'Upload'}
        </button>
        <span className="muted small">will go to {willRouteTo}</span>
      </div>

      {progress !== null ? (
        <div className="progress">
          <div style={{ width: `${Math.round(progress * 100)}%` }} />
        </div>
      ) : null}

      {error ? (
        <Banner tone="error" title="The upload failed.">
          {error}
        </Banner>
      ) : null}

      {result ? (
        <div style={{ marginTop: 16 }}>
          <div className="row">
            {result.provider === 'google_drive' ? (
              <Pill tone="drive">Google Drive</Pill>
            ) : (
              <Pill tone="warn">{result.provider}</Pill>
            )}
            <Pill tone={result.outcome === 'ready' ? 'on' : 'bad'}>{result.outcome}</Pill>
            <span className="muted small">
              {formatBytes(result.size)} in {result.elapsed}
              {result.bytes_sent !== result.size ? ` · ${formatBytes(result.bytes_sent)} sent` : ''}
            </span>
          </div>
          <dl className="kv" style={{ marginTop: 12 }}>
            <dt>Object</dt>
            <dd className="mono">{result.object_id}</dd>
            <dt>Provider ref</dt>
            <dd className="mono">{result.provider_ref || '—'}</dd>
            {result.mime_type ? (
              <>
                <dt>Type</dt>
                <dd className="mono">{result.mime_type}</dd>
              </>
            ) : null}
          </dl>
        </div>
      ) : null}
    </Card>
  );
}
