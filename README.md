# openhost-catalog

Go + HTML template app for OpenHost app discovery and one-click publishing.

## What it does

- Aggregates app entries from a configurable list of JSON feed sources
- Renders a server-side catalog UI (no React)
- Publishes apps to OpenHost with a single click via the **installer v2 service**
- Polls deployment status and app logs via the same service
- Surfaces a permission-grant link if the owner has not yet authorized the catalog to install the requested repo

Deployment configuration is always read from the target repo's `openhost.toml` during deploy.

## How install works

The catalog calls the OpenHost router's `installer` v2 service to install apps. There is no owner API token involved — the catalog uses its own `OPENHOST_APP_TOKEN` (injected by the router for every installed app) and a `[[services.v2.consumes]]` grant.

Service URL: `github.com/imbue-openhost/openhost/services/installer`

The catalog's `openhost.toml` declares:

```toml
[[services.v2.consumes]]
service = "github.com/imbue-openhost/openhost/services/installer"
shortname = "installer"
version = ">=0.1.0"
grants = [
  { capability = "install", repo_url_prefix = "https://github.com/" },
]
```

The catalog then calls `POST /api/services/v2/call/installer/install` (and `GET /api/services/v2/call/installer/status/<name>`, `GET /api/services/v2/call/installer/logs/<name>`). The `installer` segment is the manifest-declared shortname; the router proxies these to the installer service.

When the owner installs the catalog (or the catalog is auto-installed on first boot via `default_apps`), this grant is recorded against the catalog. Install calls are then authorized by prefix-match against `repo_url`. If the catalog tries to install a repo outside the granted prefix, the router returns a 403 with a `grant_url` that the publish page links to.

This replaces the legacy `APP_REPO_ROUTER_TOKEN` mechanism, which granted owner-level access to the router. The new model is scoped to install-only, and the prefix can be narrowed by the owner from the dashboard's permissions page.

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
      "docs_url": "https://github.com/imbue-openhost/openhost-searxng#readme"
    }
  ]
}
```

Required fields: `name`, `title`, `repo_url`. All others may be omitted.

`name` must be lowercase alphanumeric with optional interior hyphens (matches OpenHost's app name format). It is the catalog's identifier for the app, the default name when deploying, and must be unique within a source.

## Runtime configuration

- `LISTEN_ADDR` (default `:8080`)
- `OPENHOST_SQLITE_main` (preferred DB path from OpenHost)
- `CATALOG_DB_PATH` (DB path fallback)
- `OPENHOST_ROUTER_URL` (default `http://host.docker.internal:8080`)
- `OPENHOST_APP_TOKEN` (injected by OpenHost; used as the bearer when calling the installer service)
- `OPENHOST_APP_NAME` (default `openhost-catalog`)
- `OPENHOST_APP_BASE_PATH` (injected by OpenHost; used for path-based routing compatibility)
- `DEFAULT_SOURCE_URL` (auto-seeded on first boot if no sources are configured; defaults to the official `imbue-openhost/openhost-apps` catalog)
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
