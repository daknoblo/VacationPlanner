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
map (Leaflet + OpenStreetMap) visualizes the trip's accommodation records; optional AI recommendations suggest
further destinations. The app is strictly private, without authentication, and runs behind
a reverse proxy.

## 2. Technology stack (project-specific)

- **Language:** Go 1.25 (stdlib preferred; minimal, well-maintained dependencies).
  Static binary, `CGO_ENABLED=0`; container builds use Go 1.26.
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
- **AI:** **Microsoft Foundry / Azure OpenAI identity-only**, using the official Azure
  Identity SDK's explicit `ClientSecretCredential` and **OpenAI v1** text Chat Completions.
  The endpoint is resolved automatically from validated ARM account endpoint metadata;
  matching OpenAI endpoints among same-account aliases have deterministic preference.
  Settings displays the discovered endpoint/status read-only and offers a compatible
  deployment select list, saved per account in SQLite, discovery refresh and an automatic
  text probe. Refresh never automatically changes a saved deployment. There are no manual
  endpoint/model/version controls or AI environment overrides. Only text recommendations
  and suggestions plus cached travel-phrase cheatsheets are implemented; destination photos
  are lookups, not AI image generation. Deployment changes trigger one small connection
  check; an explicit recheck button remains without a consent checkbox.
- **Geocoding:** server-proxied in `internal/geo`; default **Photon** (Komoot) for as-you-type
  autocomplete, with a tolerant Photon/Nominatim parser. Base URL is configured under **Settings**;
  optional `GEOCODER_API_KEY` env stays server-side (strict CSP keeps all calls same-origin).
- **PDF export:** pure-Go `github.com/go-pdf/fpdf` with embedded Go UTF-8 fonts
  (`golang.org/x/image/font/gofont`), in `internal/pdf` (emoji stripped).
- **Timezones:** the IANA database is embedded (`time/tzdata`) so the timezone setting works
  in the distroless image.
- **Auth:** none (private, internal, behind Traefik/reverse proxy, listens on `:8080`).

## 3. Functional requirements

### Data model (`internal/models`)

- **`Vacation`** (a planned trip): `ID` (UUID), `Title`, `Destination`,
  `StartDate`/`EndDate`, optional `Latitude`/`Longitude`, `Notes`, optional `Budget`,
  `People`, timestamps. Relations (`TravelSegments`, `Sights`, `Activities`) are loaded on
  demand, not stored on the row. Helpers: `Nights()` (never negative), `HasCoords()`,
  `Days()` (each calendar day of the trip).
- **`TravelSegment`** (arrival/departure): `Kind` ∈ {`arrival`, `departure`} (`Valid()`
  check), `Mode` (flight/train/car/ferry …), `FromLocation`/`ToLocation`, optional
  `DepartAt`/`ArriveAt`, `Notes`.
- **`Sight`**: `Name`, `Category`, `Description`, optional coordinates, optional
  `PlannedDate`, `Visited` flag, `Notes`. `HasCoords()` checks whether the point can be
  placed on the map.
- **`Activity`** (planned on a specific day): `Day`, `Title`, `Category`, `StartMin`/`EndMin`
  (minutes from midnight, drive the day planner's hour grid), `Description`, `Location`.
  Helpers: `OnDay()`, `StartLabel()`/`EndLabel()` (`HH:MM`).
- **`Category`** (user-managed item label): `ID` (UUID), `Name`, `Icon`, `SortOrder`,
  `CreatedAt`. Offered on the sight/activity forms; seeded with Activity / Food /
  Point of Interest. Deleting a category never orphans data (item labels are denormalized).

### Routes / actions (`internal/server/routes.go`)

- `GET /` – overview of all vacations.
- `GET /vacations`, `POST /vacations`; `GET/POST/DELETE /vacations/{id}` – CRUD.
- `POST /vacations/{id}/archive` – manually archive an ended vacation, preserving its data.
- `GET /vacations/{id}/api/sights` – sights as JSON (map markers).
- `GET /vacations/{id}/export` (`?day=` optional) – print-friendly itinerary (per day or full).
- `GET /vacations/{id}/export.pdf` (`?day=` optional) – server-generated PDF itinerary.
- `POST /vacations/{id}/sights` · `POST /vacations/{id}/travel` ·
  `POST /vacations/{id}/activities` – create sights, travel segments and activities.
- `POST /vacations/{id}/ai/recommendations` – AI recommendations for this destination.
- `POST /sights/{id}/visited`, `DELETE /sights/{id}`, `DELETE /travel/{id}`.
- `POST /activities/{id}` (planner drag/resize), `DELETE /activities/{id}`.
- `GET /api/geocode?q=` – server-proxied destination autocomplete.
- `GET /api/activities/suggest?q=&dest=` – AI activity suggestions (empty when AI disabled).
- `GET /settings`, `POST /settings` – choose the UI language (stored in the `lang` cookie).
- `POST /settings/ai` – save a discovered compatible chat deployment and check it when changed.
- `GET /settings/ai/status` – read cached discovery and connection status; no Azure call.
- `POST /settings/ai/discover` – refresh configured accounts' ARM metadata.
- `POST /settings/ai/probe` – explicit text connection recheck.
- `GET/POST /vacations/{id}/cheatsheet` – read or generate cached destination-language phrases.
- `POST /vacations/{id}/cheatsheet/phrases` – translate and retain a custom word or sentence.
- Custom phrases are queued individually and share the standard vocabulary table;
  polling must not replace/reset the input form or trigger standard-list regeneration.
- Cheatsheets add "My name is ..." for current selected trip participants. A validated
  translated `{name}` frame is cached alongside the fixed vocabulary; personal names
  are inserted locally, never sent to the provider. The lifecycle worker backfills
  existing sheets through the bounded durable queue, never from GET/poll handlers.
  Participant changes do not regenerate vocabulary; matching custom introductions are
  reused without deleting custom data. Failed frame jobs require attempt-bound retries.
- `GET /vacations/{id}/api/overview-map` – accommodation-only overview markers.
- `GET /vacations/{id}/api/ideas-map` – read-only markers for all accommodation and
  saved ideas, including scheduled items and explicit unlocated records.
- `GET /vacations/{id}/api/ideas-route?lodging=&item=` – read-only cached driving leg
  between current same-trip records. The Ideas map defaults to Overview; selecting an
  accommodation reloads cached data and fits its ideas and saved road geometries.
  Distance labels match numbered table rows; route hover/focus highlights and reveals
  the destination within the table without scrolling the page and turns the road magenta.
  Distance labels use collision-free viewport placement with dashed leader lines when
  displaced; recalculate after pan/zoom/resize. In a crowded viewport prioritize the
  active road's label rather than overlap labels. Labels contain a circled number and
  saved distance/time. The road, blue numbered marker and label activate the same full
  saved-route fit (also via keyboard). Hover/focus must not move the activated label
  under the pointer. Markers without a saved road keep their location popup.
  Preserve focused/manual views on polling. The map has no refresh/retry buttons.
  Keep its help concise, route totals small and right-aligned in the title row, and
  origin label/select on one line. The table has no route-status column; show a short
  description under each title, right-aligned distance/time, then a trip-day selector.
  Date-only scheduling reuses the original item without inventing or changing times;
  preserve source fields atomically and refresh the planner through itemsChanged.
  A bounded lifecycle worker generates missing descriptions once per identifying source,
  in English/German in one call, with durable reservation and attempt-bound completion.
  Existing descriptions take precedence. Never send notes or participants for descriptions,
  retry failed paid calls on polling/restart, or start AI work from GET/status handlers.
- `POST /settings/route/retry` – Settings trip-selectable retry for missing idea routes,
  with CSRF and trip validation; also queue missing location/region enrichment.
  The Refresh action works without a routing key and then clearly reports location-only
  work. Preserve complete results, manual locations and other trips' caches.
  Before routing, queue a bounded initial geography attempt for trips with missing
  coordinates; do not repeatedly retry ambiguous places. Edits/manual refresh can retry.
  The planner has no introductory text/refresh block. Missing idea coordinates show
  a yellow circled exclamation mark linking to the existing location editor, including
  map tables and day/week blocks; a manually set region does not suppress this warning.
- `POST /vacations/{id}/ideas-routes/retry` – explicitly retry failed or incomplete
  cached routes. A lifecycle worker prepares every located accommodation/idea pair
  without page visits, one at a time with pacing. SQLite results survive restart;
  current coordinate/provider comparisons protect against stale writes and reads.
  New/geocoded pairs are discovered automatically. GET/status polling never dispatches
  routing calls. Missing geometry is explicit, never replaced with a straight line.
  Two-point ORS replies can legitimately omit segments with instructions disabled;
  use validated provider summary metrics for the sole leg. Map reloads only read
  SQLite; retry missing routes in Settings is a separate CSRF-protected action and preserves
  successful results. Display saved, failed and pending route counts separately.
- `POST /vacations/{id}/geography/refresh` – retry bounded coordinate/region enrichment.
- `GET /vacations/{id}/api/daycounts` – counts of day-assigned items, including untimed items.
- `GET /vacations/{id}/api/calendar-regions` – read-only saved accommodation regions
  for horizontal day/week calendar bands; never starts provider work.
- `GET /vacations/{id}/api/ai-centers` – read-only destination, accommodation and accommodation-region
  search presets. Regional midpoints use all located accommodations in that region.
  Recommendation POSTs re-resolve presets from current trip bookings; custom map points
  remain supported, and missing/removed presets fail before any AI call. Accommodation
  markers select their booking and fit the current search radius without provider work.
- `GET /vacations/{id}/api/dayroute?day=` – derived daily driving route, never additional bookings.
- `POST /settings/region` – week start + timezone; `POST /settings/geo` – geocoder base URL.
- `POST /settings/categories`, `DELETE /settings/categories/{categoryID}` – manage item categories.
- `GET /healthz`, `GET /readyz` – health/readiness.
- `GET /api/background-status` – read-only local progress for geography, translation,
  background driving routes and AI discovery; never enqueue work or make provider
  calls from status polling.
- The header's background status remains visible while idle, using a neutral
  "No active requests" state rather than claiming all data is resolved. Initial loads
  and status-fetch failures are explicit; only genuinely active work animates, with
  a circular spinner respecting reduced-motion preferences. All app pages and the
  header share a centered 75% desktop width and responsive smaller-screen widths.
  The brand is left-aligned, status right-aligned, and navigation has 1–2 cm spacing
  before the status. Browser titles start with "Vacationplanner".

### Behavior

- Clicking the map fills coordinates for new entries; markers for all sights with
  coordinates.
- AI suggestions can be added as sights with a single click.
- New vacations queue their Cheatsheet generation after successful persistence. A bounded
  lifecycle-managed worker handles the durable queue; page reads never start paid calls.
  Custom translations are cached separately and preserved across standard-list regeneration.
- Added ideas retain safe external reference links and supplied coordinates through edits
  and scheduling. The budget is derived from source bookings; editing it opens those
  originals, and missing payer assignments must not be guessed.
- Ideas support geographic region grouping and an editable region override. Background
  geography enrichment updates only unchanged geo fields; it must never overwrite costs,
  payers or manual regions, or invent hotel locations from destination centers.
- Existing ideas can select a location in their inline editor to persist coordinates
  for background region lookup, without changing their title or reference links.
  Unlocated ideas also receive bounded background name/address geocoding with exact-name
  and trip-locality checks. Exact Wikipedia titles/redirects can provide coordinates
  for omitted landmarks; only fixed Wikipedia API hosts are used, never arbitrary
  reference URLs. Ambiguous/generic/distant matches stay unlocated. CAS updates protect
  source names, links, manual regions, coordinates and destination edits, while leaving
  costs, payers, schedules and notes intact. Planner links open the original location editor.
  Accommodation regions are cached separately and shown as merged horizontal calendar
  bands. Local check-in/check-out dates are inclusive; transfer days show both regions.
  Missing regions and missing accommodations remain explicit, never inferred from POIs.
- AI-generated content is **never rendered as raw HTML** (`html/template` escaping).
- The UI language is switchable in Settings and persisted per client.

## 4. Architecture & structure

- Clear separation: domain (`internal/models`) · persistence (`internal/store`) ·
  HTTP/handlers/middleware/rendering (`internal/server`) · AI recommendations (`internal/ai`) ·
  Azure identity/discovery/inference (`internal/foundry`) ·
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
  - `AZURE_RESOURCE_ID`, `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`
    (all four required to enable AI; resource ID is a full Cognitive Services account ID).
  - `AZURE_IMAGE_RESOURCE_ID` (optional separate read-only inventory with the same identity;
    no image generation). These five are the only AI environment variables. No identity
    disables AI; partial identity is an error, never a fallback. Secrets are runtime-only.
  - `GEOCODER_API_KEY` (optional; for keyed Photon/Nominatim-compatible geocoders; base URL in Settings).
  - `ROUTER_API_KEY` (optional; OpenRouteService key for driving time/distance between stops; base URL in Settings).
  - `CSRF_KEY` (hex, 32 bytes; **required in production**, ephemeral in dev).

## 6. Non-goals / deliberate simplifications

- **No authentication / user management** – operated only behind a TLS reverse proxy.
- No multi-tenancy; the instance targets a single private user.
- No external frontend framework/build step – HTMX + Leaflet are vendored, no Node/bundler.

## 7. Working conventions (for the agent)

- Before committing, these must be green: `gofmt`, `go vet ./...`, `go build ./...`,
  `go test -race ./...`. Additionally `golangci-lint run` (incl. gosec and misspell) and
  `govulncheck ./...`.
- After completing user-requested changes and passing validation, publish a new
  release directly; the user has authorized automatic publication without a separate
  approval prompt. Verify CI, versioned multi-architecture images and updated Pages
  documentation/screenshots before reporting publication as successful.
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
- Tasks are tracked in `docs/BACKLOG.md` and worked on **sequentially**.
