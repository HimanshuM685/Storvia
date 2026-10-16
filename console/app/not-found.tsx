import { Card } from '@/components/ui';

/**
 * Shown for any path that is not the console root.
 *
 * The console is a single page, so this is almost always a mistyped URL. It renders on
 * the server (it is not interactive) and uses the design system, which the root layout
 * has already applied.
 */
export default function NotFound() {
  return (
    <main>
      <Card title="Page not found">
        <p className="hint">
          The console is a single page and this address is not part of it.
        </p>
        <a className="mono" href="/">
          Go to the console
        </a>
      </Card>
    </main>
  );
}