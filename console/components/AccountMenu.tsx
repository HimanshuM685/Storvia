'use client';

import { useState } from 'react';
import { apiUrl, initialsFor } from '@/lib/api';
import type { State } from '@/lib/types';

/**
 * The signed-in account, as a person rather than as a configured id.
 *
 * The point of this component: an operator has to be able to see *which* Google account
 * they are about to upload from. Showing `user_dev_admin @ tenant_dev_admin` tells them
 * nothing about that — those are deployment labels, and they read identically no matter
 * which account is connected.
 *
 * The fallback chain is deliberate: Google display name, then the email address, then
 * the configured id. Each is less certain to be meaningful than the one before it, and
 * the last is a last resort rather than the default.
 */
export function AccountMenu({ account }: { account: State['account'] }) {
  const [open, setOpen] = useState(false);

  const label = (account.label || account.display_name || account.email || account.user_id || '').trim();
  const name = account.display_name?.trim() || label;
  const picture = account.picture_url?.trim();

  return (
    <div className="account">
      <button
        type="button"
        className="account-trigger"
        onClick={() => setOpen((was) => !was)}
        aria-expanded={open}
        aria-haspopup="true"
        title={account.email || name}
      >
        <Avatar picture={picture} label={name} />
        <span className="account-name">{name || 'Not signed in'}</span>
        <svg className="chevron" width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
          <path d="M2.5 4.5 6 8l3.5-3.5" fill="none" stroke="currentColor" strokeWidth="1.6"
                strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>

      {open ? (
        <>
          {/* Clicking away closes it. Without this the panel stays open over the page. */}
          <div className="account-scrim" onClick={() => setOpen(false)} aria-hidden="true" />
          <div className="account-panel" role="menu">
            <div className="account-head">
              <Avatar picture={picture} label={name} large />
              <div className="account-ident">
                <div className="account-display">{name || 'Unknown account'}</div>
                {account.email ? <div className="account-email">{account.email}</div> : null}
              </div>
            </div>

            <dl className="account-facts">
              <div>
                <dt>Google account</dt>
                <dd className="mono">{account.subject || '—'}</dd>
              </div>
              <div>
                <dt>Tenant</dt>
                <dd className="mono">{account.tenant_id}</dd>
              </div>
            </dl>

            <form method="post" action={apiUrl('/console/sign-out')}>
              {/* A navigation, not a fetch: the session lives in Go, so only Go can
                  end it and only Go's origin can clear the cookie. */}
              <button type="submit" className="ghost account-signout">
                Sign out
              </button>
            </form>
          </div>
        </>
      ) : null}
    </div>
  );
}

function Avatar({
  picture,
  label,
  large = false,
}: {
  picture?: string;
  label: string;
  large?: boolean;
}) {
  const [failed, setFailed] = useState(false);
  const size = large ? 44 : 26;

  // Google's photo links are public CDN URLs, not bearer tokens. If one 404s -- the
  // account changed its picture, or the link expired -- an initial is better than a
  // broken image icon.
  if (picture && !failed) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        className={`avatar${large ? ' avatar-lg' : ''}`}
        src={picture}
        alt=""
        width={size}
        height={size}
        onError={() => setFailed(true)}
        referrerPolicy="no-referrer"
      />
    );
  }
  return (
    <span className={`avatar avatar-fallback${large ? ' avatar-lg' : ''}`} style={{ fontSize: large ? 15 : 11 }}>
      {initialsFor(label)}
    </span>
  );
}