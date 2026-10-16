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

export function Card({
  title,
  hint,
  action,
  children,
}: {
  title: ReactNode;
  hint?: string;
  /** An optional control shown on the same line as the title, e.g. a bulk action. */
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="card">
      <div className="card-head">
        <h2>{title}</h2>
        {action ? <div className="card-action">{action}</div> : null}
      </div>
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
