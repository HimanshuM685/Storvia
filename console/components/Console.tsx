'use client';

import { useCallback, useEffect, useState } from 'react';
import type { State } from '@/lib/types';
import { apiUrl } from '@/lib/api';
import { DrivePanel } from './DrivePanel';
import { ObjectTable } from './ObjectTable';
import { OperatorControls, RoutingSummary } from './OperatorControls';
import { Banner, Card, Pill } from './ui';
import { UploadPanel } from './UploadPanel';

/**
 * The signed-in console.
 *
 * Presentational on purpose: `ConsoleGate` owns the fetch, so there is exactly one
 * place that decides whether anyone is signed in, and this component never has to
 * reason about the loading and 401 states again.
 */
export function Console({ initial, reload }: { initial: State; reload: () => Promise<void> }) {
  const [state, setState] = useState<State>(initial);

  // Adopt a reload triggered from outside this component (for example a back-navigation
  // after signing out elsewhere) without duplicating the fetch.
  useEffect(() => {
    setState(initial);
  }, [initial]);

  const refresh = useCallback(async () => {
    await reload();
  }, [reload]);

  const target = state.routing.forced
    ? state.storage.fallback_vendor || 'the fallback provider'
    : 'Google Drive';

  return (
    <>
      <header className="topbar">
        <h1>Storvia console</h1>
        <Pill tone={state.routing.forced ? 'warn' : 'drive'}>{target}</Pill>
        <span className="spacer" />
        <span className="who">
          {state.account.user_id} @ {state.account.tenant_id}
        </span>
        <button className="ghost" onClick={() => void refresh()}>
          Refresh
        </button>
        {/* A plain form post to Go. The session lives there, so the console does not
            get a vote in ending it. */}
        <form method="post" action={apiUrl('/console/sign-out')}>
          <button className="ghost">Sign out</button>
        </form>
      </header>

      <main>
        <Card title="Where uploads will go">
          <RoutingSummary routing={state.routing} />
        </Card>

        <UploadPanel
          willRouteTo={target}
          acceptsUploads={state.storage.accepts_uploads}
          blockedReason={state.storage.blocked_reason}
          onUploaded={() => void refresh()}
        />

        <DrivePanel connection={state.connection} onChanged={() => void refresh()} />

        <OperatorControls state={state} onChanged={() => void refresh()} />

        <ObjectTable objects={state.objects} />

        {state.diagnostics && Object.keys(state.diagnostics).length > 0 ? (
          <Card title="Diagnostics">
            <dl className="kv">
              {Object.entries(state.diagnostics).map(([key, value]) => (
                <div key={key} style={{ display: 'contents' }}>
                  <dt>{key}</dt>
                  <dd className="mono">
                    {typeof value === 'object' ? JSON.stringify(value) : String(value)}
                  </dd>
                </div>
              ))}
            </dl>
          </Card>
        ) : null}
      </main>
    </>
  );
}
