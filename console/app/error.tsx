'use client';

import { Banner, Card } from '@/components/ui';

/**
 * The route error boundary.
 *
 * Next requires this file. When the console's own components throw, this catches it and
 * offers a retry, instead of the dev server replacing the page with "missing required
 * error components, refreshing..." and reloading on a loop.
 *
 * The distinction from global-error.tsx matters: this one renders *inside* the root
 * layout, so it can use the design system. That also means the API being unreachable is
 * not an error here — the console treats a 401 as "not signed in" and renders the
 * sign-in gate, so a genuinely unreachable API surfaces through the gate's own banner.
 * What reaches this boundary is a bug in the console.
 */
export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <main>
      <Card title="Something went wrong">
        <Banner tone="error" title="The console hit an unexpected error.">
          This is a fault in the console, not in your files. Nothing was deleted.
        </Banner>
        <p className="hint">
          If this keeps happening, check the API log — it records the failing request and
          its cause.
        </p>
        {error.digest ? (
          <p className="muted small" style={{ fontFamily: 'monospace' }}>
            Reference: {error.digest}
          </p>
        ) : null}
        <div className="row">
          <button onClick={reset}>Try again</button>
        </div>
      </Card>
    </main>
  );
}