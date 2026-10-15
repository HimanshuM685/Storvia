import type { ReactNode } from 'react';

type PillTone = 'on' | 'off' | 'warn' | 'bad' | 'drive';

export function Pill({ tone = 'off', children }: { tone?: PillTone; children: ReactNode }) {
  return <span className={`pill ${tone}`}>{children}</span>;
}

export function Banner({
  tone,
  title,
  children,
}: {
  tone: 'info' | 'ok' | 'warn' | 'error';
  title?: string;
  children?: ReactNode;
}) {
  return (
    <div className={`banner ${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
      <div>
        {title ? <strong>{title}</strong> : null}
        {title && children ? ' ' : null}
        {children}
      </div>
    </div>
  );
}

export function Card({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="card">
      <h2>{title}</h2>
      {hint ? <p className="hint">{hint}</p> : null}
      {children}
    </section>
  );
}

/** A select bound to a value, with an explicit disabled reason rather than a silent
 *  greyed control. */
export function Choice({
  label,
  hint,
  value,
  disabled,
  onChange,
  children,
}: {
  label: string;
  hint?: string;
  value: string;
  disabled?: boolean;
  onChange: (value: string) => void;
  children: ReactNode;
}) {
  return (
    <label className="field">
      {label}
      <select value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
        {children}
      </select>
      {hint ? <span className="small">{hint}</span> : null}
    </label>
  );
}
