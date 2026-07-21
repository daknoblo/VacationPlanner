# Backlog

Topics for VacationPlanner, worked on sequentially. Newest ideas at the bottom of
each section. Keep items small and actionable; move done items to **Done**.

## Now / next

### Day-planner overhaul (epic, 2026-07-21)

### Day-planner overhaul (epic, 2026-07-21)

- [x] **Unified day items** — merge Sights and Activities into a single per-day `items`
      model (category, time, location/coordinates, cost); Overview map fed from items.
- [x] **OSM routing** — server-proxied routing client (OpenRouteService/Valhalla; API key
      via `ROUTER_API_KEY`, base URL in Settings) computing per-leg time + distance.
- [x] **Day summary** — a full-width Mermaid route diagram above the calendar
      (Hotel → item → … → Hotel) with drive time/distance per leg.

- [ ] **Edit sights & travel segments** — currently only create/delete are supported;
      add inline edit (name, category, coordinates, dates, notes).
- [ ] **Geocode AI suggestions** — look up coordinates (e.g. OpenStreetMap Nominatim,
      rate-limited + cached) so AI-added sights appear on the map immediately.
- [ ] **Trusted proxy handling** — when running behind Traefik, resolve the real client
      IP from `X-Forwarded-For` (with a configurable trusted-proxy list) for rate
      limiting and logging; currently `RemoteAddr` is used to avoid header spoofing.

## i18n

- [ ] **More languages** — add further locales (a unit test already enforces catalog
      completeness against the English fallback).
- [ ] **Pluralization** — proper plural forms (e.g. "1 night" vs "2 nights",
      "1 Nacht" vs "2 Nächte") instead of a single label.
- [ ] **Localized dates/numbers** — format dates per locale instead of a fixed
      `dd.MM.yyyy` / `yyyy-MM-dd`.

## Features

- [ ] **Search & filter** — filter vacations by date range / destination; filter
      sights by category or visited state.
- [ ] **Attachments** — optional images/links per sight.

## Quality & ops

- [ ] **More store test coverage** — extend the SQLite store tests (edge cases,
      concurrent access) beyond the current CRUD/cascade/round-trip suite.
- [ ] **Accessibility pass** — labels/roles/keyboard navigation review, color contrast.
- [ ] **`LICENSE`** — decide on and add a license file.

## Optional / later

- [ ] **Authentication** — optional login (the app currently assumes a private
      deployment behind a TLS reverse proxy).
- [ ] **Pin GitHub Actions by commit SHA** — supply-chain hardening (intentionally
      **not** done for now; version tags are preferred).

## Done

- [x] Export — iCal (`.ics`) feed for travel segments plus an all-day trip event
      (`GET /vacations/{id}/export.ics`, pure-Go `internal/ical`).
- [x] Day-planner tab UX — two-row tab bar (General / Arrival / Departure / Overview /
      Budget + a collapsible day selector).
- [x] Budget tab — per-vacation budget and number of people.
- [x] Custom item categories — Settings CRUD with seeded defaults
      (Activity / Food / Point of Interest).
- [x] Printable and server-generated PDF itinerary export (per day or full trip).
- [x] Switched persistence from PostgreSQL to SQLite (`modernc.org/sqlite`, pure Go);
      added SQLite store tests (CRUD, cascade, round-trip).
- [x] Multi-language UI (English/German) switchable under Settings.
- [x] Bump `pgx` to v5.9.2 (fixes govulncheck GO-2026-5004).
- [x] `models` unit tests, `GET /vacations` route.
- [x] Repository language switched to English (code, comments, docs);
      `misspell` linter re-enabled.
