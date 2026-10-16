'use client';

/**
 * The root error boundary.
 *
 * Next requires this file. Without it, when the root layout itself fails to render, the
 * dev server serves its own "missing required error components, refreshing..." page and
 * loops the browser on reload — the application never gets to show anything at all. That
 * is worse than a visible error, because it hides the error.
 *
 * This boundary owns its own <html> and <body> because it replaces the root layout when
 * it renders. It cannot rely on globals.css having been applied by that layout, so the
 * styling here is inline and deliberately plain.
 *
 * The message is generic on purpose. An error boundary is not the place to diagnose a
 * failure — it is the place to tell the operator something is wrong and give them a way
 * out. In development the overlay carries the detail.
 */
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <html lang="en">
      <body
        style={{
          fontFamily:
            'system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif',
          background: '#0e1116',
          color: '#e6e8eb',
          display: 'flex',
          minHeight: '100vh',
          margin: 0,
          alignItems: 'center',
          justifyContent: 'center',
          padding: 24,
        }}
      >
        <div style={{ maxWidth: 520 }}>
          <h1 style={{ fontSize: 18, margin: '0 0 8px' }}>The console could not start</h1>
          <p style={{ color: '#98a1b3', fontSize: 14, lineHeight: 1.5, margin: '0 0 20px' }}>
            Something failed while rendering this page. This is usually the API being
            unreachable rather than a problem with your data. Nothing has been deleted or
            changed.
          </p>
          {error.digest ? (
            <p style={{ color: '#6b7482', fontSize: 12, fontFamily: 'monospace', margin: '0 0 20px' }}>
              Reference: {error.digest}
            </p>
          ) : null}
          <button
            onClick={reset}
            style={{
              background: 'transparent',
              color: '#e6e8eb',
              border: '1px solid #2a3038',
              borderRadius: 8,
              padding: '8px 14px',
              fontSize: 13,
              cursor: 'pointer',
            }}
          >
            Try again
          </button>
        </div>
      </body>
    </html>
  );
}