# SyntheticBrew landing page — isolated Astro rebuild

This directory is a standalone marketing site. It does not import, symlink, or load source code from the current `syntheticbrew-cloud-web/cloud-web-astro` application. Verified screenshots, brand assets, analytics identifiers, and discovery files were copied into `public/` so this build can be previewed and deployed independently.

## Run locally

```bash
npm install
npm run dev
```

The default development URL is `http://localhost:4326`.

## Verify

```bash
npm run build
```

The build command performs Astro type checks, creates the static site, and runs `scripts/audit-build.mjs`. The audit checks required old and new routes, title and description limits, exactly one H1, canonical tags, valid JSON-LD, image alt text, internal links, sitemap output, robots, and AI discovery resources.

## Preserved application boundaries

This repository builds only the marketing document root. Production routing must continue to send these existing paths to their existing services:

- `/docs` and `/docs/*` → Astro/Starlight documentation build
- `/login`, `/register`, `/dashboard/*`, `/billing/*`, `/settings`, `/team`, password and verification routes, and `/examples/*` → Cloud React SPA
- `/api/*` → Cloud API
- `/releases/*` → release artifact document root
- `app.syntheticbrew.ai`, `api.syntheticbrew.ai`, and `mcp.syntheticbrew.ai` → their existing upstreams

The included `deploy/Caddyfile` documents the complete routing contract and uses a true 404 response instead of returning the homepage for unknown marketing URLs.

## Analytics carried forward

- Google Analytics 4 measurement ID `G-QM8W4T1H8S` loaded with `gtag.js`. There is no separate `GTM-*` container in the existing site.
- Yandex Metrica counter `109861753` with the existing SSR, Webvisor, click map, ecommerce dataLayer, referrer, URL, bounce, and link-tracking options.
- CTA events are sent to both systems without form values or other PII: `cloud_signup_start`, `self_host_quickstart`, and `contact_sales_start`.

Sales links point directly to Tim’s existing personal Calendly URL: `https://calendly.com/timkrav/metting-with-tim-chirp`.

## Release checklist

1. Build and preview this directory in isolation.
2. Crawl the preview and run Lighthouse for desktop and mobile.
3. Compare all production marketing paths, metadata, screenshots, analytics, `robots.txt`, `llms.txt`, `.well-known` resources, and sitemap output.
4. Deploy `dist/` to a new versioned marketing directory.
5. Switch only the marketing document root in Caddy; do not replace the SPA, docs, releases, or API roots.
6. Verify Cloud registration, login, example demos, docs, release downloads, Calendly, analytics, 404 responses, and both sitemaps.
7. Run the agent-readiness curl suite against the preview (see `deploy/agent-readiness.md`); after deploy, re-run the isitagentready.com scan against production.
8. Retain the previous marketing artifact for immediate rollback.
9. Submit the new sitemap in Google Search Console and request recrawls for the homepage and highest-priority feature and solution pages.
