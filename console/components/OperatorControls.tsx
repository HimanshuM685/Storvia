'use client';

import { api } from '@/lib/api';
import type { RoutingChoice, State } from '@/lib/types';
import { Banner, Card, Choice, Pill } from './ui';

type Props = { state: State; onChanged: () => void };

export function OperatorControls({ state, onChanged }: Props) {
  const { routing, faults, consent, storage } = state;

  // Derived from the server rather than kept locally, so what is shown is always what
  // is in effect. A local copy drifts the moment a reload happens.
  const routingChoice: RoutingChoice = routing.forced
    ? 'forced'
    : routing.overridden
      ? 'drive'
      : 'config';

  const fallbackUnavailable = routing.forced && !storage.fallback_ready;

  return (
    <Card title="Operator controls">
      <div className="grid">
        <Choice
          label="Force fallback upload"
          hint={
            routing.overridden
              ? `Overriding config (config has force=${String(routing.configured)}).`
              : `Following config (force=${String(routing.configured)}).`
          }
          value={routingChoice}
          disabled={!routing.available}
          onChange={async (value) => {
            await api.setRouting(value as RoutingChoice);
            onChanged();
          }}
        >
          <option value="drive">Use Google Drive</option>
          <option value="forced">Force the fallback provider</option>
          <option value="config">Follow the config default</option>
        </Choice>

        <Choice
          label="Simulate a Drive outage"
          hint="Fault injection only: it makes the availability check lie. It does not move real data."
          value={faults.enabled ? 'on' : 'off'}
          onChange={async (value) => {
            await api.setFaults(value === 'on');
            onChanged();
          }}
        >
          <option value="off">Off — use the real Drive path</option>
          <option value="on">On — pretend Drive is unavailable</option>
        </Choice>

        <Choice
          label="Outage-fallback disclosure"
          hint={
            storage.fallback_requires_consent
              ? 'Required in this deployment before anything is stored outside Drive.'
              : 'Not required in this deployment — the fallback works on its own.'
          }
          value={consent.granted ? 'yes' : 'no'}
          onChange={async (value) => {
            await api.setConsent(value === 'yes');
            onChanged();
          }}
        >
          <option value="no">Not accepted</option>
          <option value="yes">Accepted</option>
        </Choice>
      </div>

      {fallbackUnavailable ? (
        <div style={{ marginTop: 16 }}>
          <Banner tone="warn" title="The fallback is forced but no backend is ready.">
            Uploads will be refused until a fallback provider is reachable.
          </Banner>
        </div>
      ) : null}

      {routing.forced && consent.granted === false && storage.fallback_requires_consent ? (
        <div style={{ marginTop: 16 }}>
          <Banner tone="warn" title="The fallback is forced but the disclosure is not accepted.">
            This deployment requires consent before a file can be stored outside Drive.
          </Banner>
        </div>
      ) : null}

      <details style={{ marginTop: 16 }}>
        <summary>Read the outage-fallback disclosure (version {consent.disclosure.version})</summary>
        <dl className="small" style={{ marginTop: 12 }}>
          <dt>Destination</dt>
          <dd>{consent.disclosure.destination}</dd>
          <dt>Region</dt>
          <dd>{consent.disclosure.region}</dd>
          <dt>Operator access</dt>
          <dd>{consent.disclosure.operator_access}</dd>
          <dt>Encryption</dt>
          <dd>{consent.disclosure.encryption}</dd>
          <dt>Retention</dt>
          <dd>{consent.disclosure.retention}</dd>
          <dt>Deletion</dt>
          <dd>{consent.disclosure.deletion}</dd>
          <dt>Disconnect</dt>
          <dd>{consent.disclosure.disconnect}</dd>
        </dl>
      </details>
    </Card>
  );
}

export function RoutingSummary({ routing }: { routing: State['routing'] }) {
  const label = routing.forced ? 'Fallback provider' : 'Google Drive';
  return (
    <div className="row">
      {routing.forced ? <Pill tone="warn">{label}</Pill> : <Pill tone="drive">{label}</Pill>}
      <span className="muted">
        Google Drive is the default. If Drive cannot be used, the upload goes to the
        fallback provider instead.
      </span>
    </div>
  );
}
