/**
 * The console has no proxy to the API, and that is deliberate.
 *
 * OAuth is backend logic. The browser asks Go to start the consent flow, Google sends it
 * back to Go, Go verifies the state, exchanges the code, issues the session, and then
 * hands the browser to the console. The frontend never sees an authorization code, never
 * verifies anything, and has no route that could accidentally take that job on.
 *
 * So the console is only ever a client. It calls the API cross-origin at
 * STORVIA_API_ORIGIN and Go answers, because cookies are port-agnostic: the session
 * cookie Go sets is sent by the browser on every request to :8080, including the ones the
 * frontend makes. That needs no proxy and no duplicated OAuth handling in the console.
 *
 * Two modes:
 *   - development: no basePath; the console is at / and calls the API cross-origin.
 *   - STORVIA_CONSOLE_EXPORT=1: a static export embedded in the Go binary, same origin,
 *     no CORS at all.
 */
const exporting = process.env.STORVIA_CONSOLE_EXPORT === '1';

/** @type {import('next').NextConfig} */
const nextConfig = exporting
  ? {
      output: 'export',
      // A separate build directory. The export and the dev server must not share
      // `.next`: running `make console` while `next dev` is up left the dev server
      // resolving modules the export had just written, and every page 500'd with
      // "Cannot find module './403.js'".
      distDir: '.next-export',
      // Paths are relative so the export can be mounted under /console.
      basePath: '/console',
      assetPrefix: '/console',
      trailingSlash: true,
      images: { unoptimized: true },
    }
  : {
      // No basePath and no trailingSlash in dev: the dev server owns the origin root, so
      // the console is at /. There is nothing to rewrite, because nothing is proxied.
      env: {
        // Where the API lives. The frontend calls it directly and Go's CORS answers.
        NEXT_PUBLIC_API_ORIGIN: process.env.STORVIA_API_ORIGIN || 'http://127.0.0.1:8080',
        // Where Go sends the browser once OAuth has been verified server-side.
        NEXT_PUBLIC_CONSOLE_URL: process.env.STORVIA_CONSOLE_URL || 'http://127.0.0.1:3001/',
      },
    };

export default nextConfig;
