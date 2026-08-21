import { createMDX } from "fumadocs-mdx/next";

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  basePath: "/docs",
  // Velafi 2026-05-15: enable standalone output when STANDALONE=true (Docker
  // build sets this). Produces apps/docs/.next/standalone/ with traced
  // node_modules so the runtime image stays minimal. Dev keeps default
  // (no standalone) since `next dev` ignores this flag anyway.
  ...(process.env.STANDALONE === "true"
    ? {
        output: "standalone",
        // fumadocs-mdx is consumed as a webpack loader at build time, not
        // imported at runtime, so default tracing misses it. SSR of MDX
        // pages still loads .source/source.config.mjs which imports
        // fumadocs-mdx/config — without explicit inclusion, the standalone
        // image's node_modules has no fumadocs-mdx and every page 500s.
        outputFileTracingIncludes: {
          "/**/*": [
            "../../node_modules/.pnpm/fumadocs-mdx*/**/*",
            "../../node_modules/.pnpm/fumadocs-core*/**/*",
            "./.source/**/*",
            "./content/**/*",
            "./source.config.ts",
          ],
        },
      }
    : {}),
  // Visiting http://host/ (outside basePath) would otherwise 404 — redirect
  // to the docs root. basePath: false makes the source and destination
  // literal (not re-prefixed with `/docs`), so the redirect runs before
  // basePath routing kicks in.
  async redirects() {
    return [
      {
        source: "/",
        destination: "/docs",
        basePath: false,
        permanent: false,
      },
      {
        source: "/zh/getting-started/cloud-quickstart",
        destination: "/zh/cloud-quickstart",
        permanent: true,
      },
      {
        source: "/zh/getting-started/self-hosting",
        destination: "/zh/self-host-quickstart",
        permanent: true,
      },
      {
        source: "/zh/guides/quickstart",
        destination: "/zh/cloud-quickstart",
        permanent: true,
      },
      {
        source: "/zh/guides/agents",
        destination: "/zh/agents",
        permanent: true,
      },
      {
        source: "/zh/cli/installation",
        destination: "/zh/cli",
        permanent: true,
      },
      {
        source: "/zh/cli/reference",
        destination: "/zh/cli",
        permanent: true,
      },
    ];
  },
};

export default withMDX(config);
