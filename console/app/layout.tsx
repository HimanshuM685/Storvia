import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'Storvia console',
  description: 'Operator console for a Storvia deployment.',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    // suppressHydrationWarning is here for browser extensions, not for our code. Some
    // extensions inject attributes onto <html> after the server-rendered HTML is sent but
    // before React hydrates, which React reports as a mismatch it will not patch. We
    // never set attributes on <html> that differ between server and client, so silencing
    // the root element keeps a real bug in the body visible while ignoring that noise.
    <html lang="en" suppressHydrationWarning>
      <body>
        <div className="shell">{children}</div>
      </body>
    </html>
  );
}
