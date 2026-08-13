# openhost-catalog

Go + HTML template app for OpenHost app discovery and one-click publishing.

## What it does

- Aggregates app entries from a configurable list of JSON feed sources
- Renders a server-side catalog UI (no React)
- Install buttons open the OpenHost router's `/add_app` page in a new tab, pre-filled with the repo URL and suggested app name, so the owner can review permissions before deploying

## How install works

Clicking Install on any catalog app opens the OpenHost router's `/add_app` page in a new tab. The URL is constructed as:

```
<router-base-url>/add_app?repo=<repo-url>&name=<app-id>
```

The `/add_app` page (served by the router) shows the app's manifest, required permissions, and a confirmation step before deployment begins. This gives the owner full visibility into what will be installed before any action is taken.

The `router-base-url` is derived from the request's `Host` / `X-Forwarded-Host` headers, with the catalog's own app-name prefix stripped, so it always points to the correct OpenHost instance.

## Feed format

Each source URL must return JSON with schema `openhost.catalog.v1`:

```json
{
  "schema": "openhost.catalog.v1",
  "source_id": "official",
  "source_name": "OpenHost Official",
  "generated_at": "2026-03-28T00:00:00Z",
  "apps": [
    {
      "name": "searxng",
      "title": "SearXNG",
      "description": "Privacy-respecting metasearch engine",
      "repo_url": "https://github.com/imbue-openhost/openhost-searxng",
      "repo_ref": "",
      "icon_url": "",
      "tags": ["search", "privacy"],
      "categories": ["search"],
      "website_url": "https://docs.searxng.org",
      "docs_url": "https://github.com/imbue-openhost/openhost-searxng#readme",
      "openhost_integration_score": 5
    }
  ]
}
```

Required fields: `name`, `title`, `repo_url`. All others may be omitted.

`name` must be lowercase alphanumeric with optional interior hyphens (matches OpenHost's app name format). It is the catalog's identifier for the app, the default name when deploying, and must be unique within a source.

## Integration score

Each app may carry an `openhost_integration_score` — an integer **1-5** rating
how natively the app integrates with OpenHost (SSO quality, data/secret
conventions, guest handling). It is **not** a rating of the upstream project's
quality.

The score is optional. The catalog treats a missing/`0` score as **unrated** and
renders it as "—" / "Unrated"; it does not mean a score of zero. The catalog
clamps the score to 1-5 on ingest.

The canonical rubric and the checklist for assigning a score live in the feed
repo: [app-manifest/SCORING.md](https://github.com/imbue-openhost/app-manifest/blob/main/SCORING.md).

### How it's surfaced

- **Catalog listing**: a star rating per app; hovering shows `N/5`. Higher-rated
  apps sort first; unrated apps sort last.
- **App detail page**: the star rating and `N/5`. The "OpenHost integration"
  label links to the rubric.

## Runtime configuration

- `LISTEN_ADDR` (default `:8080`)
- `OPENHOST_SQLITE_main` (preferred DB path from OpenHost)
- `CATALOG_DB_PATH` (DB path fallback)
- `OPENHOST_ROUTER_URL` (default `http://host.docker.internal:8080`)
- `OPENHOST_APP_TOKEN` (injected by OpenHost; used as the bearer when calling the installer service)
- `OPENHOST_APP_NAME` (default `openhost-catalog`)
- `OPENHOST_APP_BASE_PATH` (injected by OpenHost; used for path-based routing compatibility)
- `DEFAULT_SOURCE_URL` (auto-seeded on first boot if no sources are configured; defaults to the official `imbue-openhost/app-manifest` catalog)
- `CATALOG_ALLOW_HTTP_REPO_URLS` (default `false`)
- `CATALOG_ALLOW_FILE_URLS` (default `false`)
- `CATALOG_HTTP_TIMEOUT_SECONDS` (default `10`)

## Development

```bash
go mod tidy
go run ./cmd/openhost-catalog
```

Open `http://localhost:8080`.

## OpenHost manifest

See `openhost.toml`.
