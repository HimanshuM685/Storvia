import { Suspense } from 'react';
import { ConsoleGate } from '@/components/ConsoleGate';

/** The console is a client component: everything it shows comes from the session-gated
 *  API, so there is nothing useful to render on the server. The API and the OAuth
 *  callback stay in Go. */
export default function Page() {
  return (
    <Suspense fallback={<div className="signin"><div className="card">Loading…</div></div>}>
      <ConsoleGate />
    </Suspense>
  );
}
