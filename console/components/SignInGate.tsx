'use client';

import { useEffect } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { apiUrl } from '@/lib/api';

export function SignInGate() {
  const router = useRouter();
  const params = useSearchParams();
  const error = params.get('error');

  // A callback that fails bounces back here with a message; leave it on screen rather
  // than clearing it on the next render, or the operator sees a bare login page with no
  // indication of what went wrong.
  useEffect(() => {
    if (error) {
      const timer = setTimeout(() => router.replace('/'), 10000);
      return () => clearTimeout(timer);
    }
    return undefined;
  }, [error, router]);

  return (
    <div className="signin">
      <div className="card">
        <h1>Storvia console</h1>
        <p className="muted">
          Sign in with the Google account whose Drive this console will use. You stay
          signed in until you sign out.
        </p>
        {error ? (
          <div className="banner error">
            <div>{error}</div>
          </div>
        ) : null}
        <a href={apiUrl('/console/sign-in')}>
          <button className="cta">Sign in with Google</button>
        </a>
      </div>
    </div>
  );
}
