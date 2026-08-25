# app-catalog

Go + HTML template app for Cloud in a Bottle app discovery and one-click publishing.

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
      "repo_url": "https://github.com/cloud-in-a-bottle/bottled-searxng",
      "repo_ref": "",
      "icon_url": "",
      "tags": ["search", "privacy"],
      "categories": ["search"],
      "website_url": "https://docs.searxng.org",
      "docs_url": "https://github.com/cloud-in-a-bottle/bottled-searxng#readme"
    }
  ]
}
```

Required fields: `name`, `title`, `repo_url`. All others may be omitted.

`name` must be lowercase alphanumeric with optional interior hyphens (matches OpenHost's app name format). It is the catalog's identifier for the app, the default name when deploying, and must be unique within a source.

## Curation

An app appears in the catalog if and only if the feed lists it. To remove an
app, drop it from the source feed's manifest. Apps are ordered alphabetically
by title.

## Submitting an app

The `/submit` page ("List your app") helps contributors add an app to the feed.
You fill in the app's details, the server validates them against the same rules
the feed ingest enforces (app-name format, allowed categories, GitHub repo URL,
and that the repo is public with an `openhost.toml` at its root), and it
generates a canonical `apps/<name>/app.toml` entry.

Submission is a pull request against the feed repo (`CATALOG_SUBMIT_REPO_URL`,
the `cloud-in-a-bottle/app-manifest` repo by default): fork it, add the
generated file, and open a PR. The repo's CI regenerates `catalog.json` and a
maintainer reviews every submission before it appears in the catalog.

## Runtime configuration

- `LISTEN_ADDR` (default `:8080`)
- `OPENHOST_SQLITE_main` (preferred DB path from OpenHost)
- `CATALOG_DB_PATH` (DB path fallback)
- `OPENHOST_ROUTER_URL` (default `http://host.docker.internal:8080`)
- `OPENHOST_APP_TOKEN` (injected by OpenHost; used as the bearer when calling the installer service)
- `OPENHOST_APP_NAME` (default `openhost-catalog`)
- `OPENHOST_APP_BASE_PATH` (injected by OpenHost; used for path-based routing compatibility)
- `DEFAULT_SOURCE_URL` (auto-seeded on first boot if no sources are configured; defaults to the official `cloud-in-a-bottle/app-manifest` catalog)
- `CATALOG_SUBMIT_REPO_URL` (feed repo the "List your app" page points contributors at; defaults to `https://github.com/cloud-in-a-bottle/app-manifest`)
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
