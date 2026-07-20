# VacationPlanner – Project requirements & instructions

> Per-project instruction file. It is the **authoritative reference** for the
> implementation of this tool. The shared technical foundation is described in the
> blueprint (`blueprint/foundation.md`, `best-practices.md`, `security.md`) and is not
> repeated here.

**Language rule:** The repository is kept in **English** throughout — code identifiers,
comments and documentation. The **Web UI is internationalized** (`internal/i18n`) and
localizable; **English and German** ship initially and are switchable under Settings.

---

## 1. Goal of the application

A web-based **vacation planner**: the user manages multiple planned trips with a date
range, destination and notes, plans arrival and departure as travel segments and collects
sights (points of interest) including category, date and a "visited" state. An interactive
map (Leaflet + OpenStreetMap) visualizes all sights; optional AI recommendations suggest
further destinations. The app is strictly private, without authentication, and runs behind
a reverse proxy.

## 2. Technology stack (project-specific)

- **Language:** Go 1.25 (stdlib preferred; minimal, well-maintained dependencies).
  Static binary, `CGO_ENABLED=0`.
- **Module path:** `github.com/daknoblo/vacationplanner`.
- **Routing:** `go-chi/chi/v5` on top of the standard `net/http`.
- **Persistence:** **SQLite** via `modernc.org/sqlite` (pure Go, no CGO). The database
  file path is set via `DB_PATH` (default `vacation.db`); WAL mode and foreign keys are
  enabled per connection. Migrations are embedded via `//go:embed`
  (`internal/store/migrations/*.sql`) and run automatically on startup.
- **UI:** Server-rendered `html/template` + **HTMX** + **Leaflet** (both vendored under
  `web/static/vendor/`). All templates and assets embedded in the binary via `embed`.
- **i18n:** Tiny dependency-free catalog in `internal/i18n` (English fallback). Templates
  translate via a `{{t "key"}}` function bound per request; the language is resolved from
  the `lang` cookie, then `Accept-Language`, then the default.
- **AI:** **OpenAI-compatible** `/chat/completions` endpoint (OpenAI, Azure OpenAI, Ollama,
  LocalAI, vLLM …), configurable via env. An empty `OPENAI_API_KEY` disables AI features.
- **Auth:** none (private, internal, behind Traefik/reverse proxy, listens on `:8080`).

## 3. Functional requirements

### Data model (`internal/models`)

- **`Vacation`** (a planned trip): `ID` (UUID), `Title`, `Destination`,
  `StartDate`/`EndDate`, optional `Latitude`/`Longitude`, `Notes`, timestamps.
  Relations (`TravelSegments`, `Sights`) are loaded on demand, not stored on the row.
  Helpers: `Nights()` (nights, never negative), `HasCoords()`.
- **`TravelSegment`** (arrival/departure): `Kind` ∈ {`arrival`, `departure`} (`Valid()`
  check), `Mode` (flight/train/car/ferry …), `FromLocation`/`ToLocation`, optional
  `DepartAt`/`ArriveAt`, `Notes`.
- **`Sight`**: `Name`, `Category`, `Description`, optional coordinates, optional
  `PlannedDate`, `Visited` flag, `Notes`. `HasCoords()` checks whether the point can be
  placed on the map.

### Routes / actions (`internal/server/routes.go`)

- `GET /` – overview of all vacations.
- `GET /vacations`, `POST /vacations`; `GET/POST/DELETE /vacations/{id}` – CRUD.
- `GET /vacations/{id}/api/sights` – sights as JSON (map markers).
- `POST /vacations/{id}/sights` · `POST /vacations/{id}/travel` – create sights and
  travel segments.
- `POST /vacations/{id}/ai/recommendations` – AI recommendations for this destination.
- `POST /sights/{id}/visited`, `DELETE /sights/{id}`, `DELETE /travel/{id}`.
- `GET /settings`, `POST /settings` – choose the UI language (stored in the `lang` cookie).
- `GET /healthz`, `GET /readyz` – health/readiness.

### Behavior

- Clicking the map fills coordinates for new entries; markers for all sights with
  coordinates.
- AI suggestions can be added as sights with a single click.
- AI-generated content is **never rendered as raw HTML** (`html/template` escaping).
- The UI language is switchable in Settings and persisted per client.

## 4. Architecture & structure

- Clear separation: domain (`internal/models`) · persistence (`internal/store`) ·
  HTTP/handlers/middleware/rendering (`internal/server`) · AI client (`internal/ai`) ·
  configuration (`internal/config`) · i18n (`internal/i18n`) · web assets (`web/`).
- Standard layout: `cmd/server/main.go` (incl. health-probe subcommand),
  `internal/...`, `web/templates` + `web/static` (both `embed`).
- The middleware order in `routes()` is deliberate (RequestID → Logger → Recoverer →
  Security headers → Timeout → Body limit → Rate limit → CSRF → Localize).
- Templates get a per-request `t` translator via a cloned template set at render time.
- Domain/computation logic is **unit-tested** (`*_test.go` in the respective packages).

## 5. Deployment & configuration

- Docker image: multi-stage, multi-arch (`amd64`+`arm64`), `distroless/static-debian12:nonroot`.
  Build/publish via GitHub Actions → GHCR (`docker-publish.yml`) with SBOM + provenance,
  followed by a Trivy image scan.
- Operated via `docker compose` (single app container with a persistent SQLite volume)
  behind a reverse proxy; port `:8080` internally.
- Configuration exclusively via env (`internal/config`), never commit secrets:
  - `APP_ENV` (`production` ⇒ JSON logs, HSTS, secure cookies), `HTTP_ADDR` (`:8080`).
  - `DB_PATH` (SQLite database file path; default `vacation.db`).
  - `OPENAI_BASE_URL` / `OPENAI_API_KEY` / `OPENAI_MODEL` (AI; empty key = disabled).
  - `CSRF_KEY` (hex, 32 bytes; **required in production**, ephemeral in dev).

## 6. Non-goals / deliberate simplifications

- **No authentication / user management** – operated only behind a TLS reverse proxy.
- No multi-tenancy; the instance targets a single private user.
- No external frontend framework/build step – HTMX + Leaflet are vendored, no Node/bundler.

## 7. Working conventions (for the agent)

- Before committing, these must be green: `gofmt`, `go vet ./...`, `go build ./...`,
  `go test -race ./...`. Additionally `golangci-lint run` (incl. gosec and misspell) and
  `govulncheck ./...`.
- Comments and documentation are in **English**; UI strings are **not** hard-coded but
  live in the `internal/i18n` catalogs (a test enforces catalog completeness). `misspell`
  is enabled and excludes `internal/i18n/messages.go` (which holds non-English translations).
- Migrations are **additive** and numbered (`internal/store/migrations/NNNN_*.sql`);
  do not modify existing migrations.
- Preserve security defaults: CSRF for all state-changing requests, strict
  security headers/CSP, rate limiting, request limits, non-root container.
- Templates/static live under `web/` (via `embed`); do not replace vendored assets with
  external CDN references.
- Do not create separate Markdown docs unless explicitly requested.
- Tasks are tracked in `BACKLOG.md` (repo root) and worked on **sequentially**.
