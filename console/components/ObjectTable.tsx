'use client';

import { useState } from 'react';
import { api, downloadUrl, formatBytes } from '@/lib/api';
import type { ObjectView } from '@/lib/types';
import { Banner, Card } from './ui';

export function ObjectTable({ objects }: { objects: ObjectView[] }) {
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  async function remove(id: string) {
    if (!window.confirm('Delete this object from the provider?')) return;
    setBusyId(id);
    setError(null);
    try {
      await api.deleteObject(id);
      window.location.reload();
    } catch (e) {
      const err = e as { message?: string };
      setError(err.message ?? 'The delete failed.');
    } finally {
      setBusyId(null);
    }
  }

  return (
    <Card title="Stored objects">
      {error ? <Banner tone="error">{error}</Banner> : null}

      {objects.length === 0 ? (
        <p className="muted">Nothing stored yet. Upload something above.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Size</th>
              <th>Provider</th>
              <th>Status</th>
              <th>Provider ref</th>
              <th>Stored</th>
              <th aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {objects.map((object) => (
              <tr key={object.id}>
                <td>
                  {object.name}
                  <div className="muted mono small">{object.logical_key}</div>
                </td>
                <td>{formatBytes(object.size)}</td>
                <td>
                  <span className={`pill ${object.provider === 'google_drive' ? 'drive' : 'on'}`}>
                    {object.provider === 'google_drive' ? 'Google Drive' : object.provider}
                  </span>
                </td>
                <td>
                  <span
                    className={`pill ${
                      object.status === 'ready' ? 'on' : object.status === 'failed' ? 'bad' : 'warn'
                    }`}
                  >
                    {object.status}
                  </span>
                </td>
                <td className="mono">{object.provider_ref || '—'}</td>
                <td className="muted small">{new Date(object.created_at).toLocaleString()}</td>
                <td className="actions">
                  {object.downloadable ? (
                    <a className="mono" href={downloadUrl(object.id)}>
                      download
                    </a>
                  ) : null}
                  <button
                    className="link"
                    disabled={busyId === object.id}
                    onClick={() => void remove(object.id)}
                  >
                    delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}
