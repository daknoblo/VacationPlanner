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
- [ ] **Attachment links** — optional external links (URLs) per item, complementing
      the uploaded file attachments (documents are already supported).

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

- [x] Shared-address lodging lookup — prefer the name-confirmed accommodation over
      restaurants or other POIs at the same address, without hiding intermediate stays.
- [x] Background progress — top-right activity indicator with live geographic batch
      progress and pending translation/discovery status, hidden when idle.
- [x] Roomier overview map — automatic framing reserves 15% per edge (minimum
      30 pixels), limits initial zoom to 13 and preserves manual view changes.
- [x] Manual vacation archive — dashboard sections for planned and archived trips;
      an archive action appears after the final day, using the configured timezone.
      Existing and newly created vacations stay active until explicitly archived.
- [x] Incremental translation and regional planning — one vocabulary table with
      queued custom words, preserved input while polling, automatic accommodation
      geocoding, region-grouped ideas with manual overrides, centered budget tiles
      and direct links for unassigned costs.
- [x] Automatic and custom Cheatsheets — creation queues a background generation;
      own words/sentences are translated, saved and safely retryable after failure.
- [x] Clearer trip overview — accommodation-only map, live day/week activity counts,
      and roomier responsive budget cards and expense rows.
- [x] Travel experience update — automatic deployment checks without a checkbox,
      cached destination-language Cheatsheet with 22 phrases, persistent idea links
      and coordinates, and a graphical hotel-to-POI daily route.
- [x] Source-based budget corrections — link expenses to original entries, preserve
      payer/category metadata on edits, retain bookings when toggling multistop, and
      make travel autosaves transactional and UUID-scoped.
- [x] Foundry-only AI configuration — remove the legacy API-key/classic adapters
      and endpoint/model/version overrides; resolve advertised account endpoints
      automatically and replace manual inputs with discovered deployment selection
      and live metadata status. Migration 0017 removes only obsolete AI settings.
- [x] Foundry AI integration — explicit service-principal authentication, bounded
      account/deployment discovery with independent persistent caches, account-bound
      text selection and diagnostics, optional image-account inventory, and preserved
      API-key mode. Includes runtime container configuration and local stub tests.
- [x] Travel summary next to the headings — the Arrival/Departure tab shows the
      total distance & time inline with the "Anreise"/"Abreise" headings, updated
      live as the legs change.
- [x] Activity distance & time — every activity now shows, in gray, its start
      point plus the distance and time to reach it (routed when a routing key is
      set, straight-line estimate otherwise). The start point defaults to the
      previous stop of the day (the day's hotel for the first stop) and can be
      changed per activity via a picker.
- [x] Day/Week card lists — the day and week calendar views gained an
      Overview-style activity card list below the hour grid.
- [x] Auto-vacuum — a Settings picker to run the database optimization
      automatically (daily / every 3 days / weekly / every 2 weeks / monthly),
      via a background maintenance loop.
- [x] Accommodation budget & map marker — lodgings now have an optional cost
      (counted in the trip budget) and a geocoded location shown as a marker on
      the overview map.
- [x] Accommodations ("Unterkunft") — a dedicated tab (between Arrival/Departure
      and Day plan) to add lodgings with a check-in/check-out date & time; each
      shows as a narrow strip over its hours on the left of the day/week planner.
- [x] Inline document preview — open PDFs/images in an in-page modal (no new tab).
- [x] Overview travel totals — per-direction total distance & travel time
      (summed across legs) shown on the Arrival/Departure overview rows.
- [x] Map viewport fix — persist a per-trip map zoom captured from the geocoder
      result type so a country/region no longer opens zoomed in too far.
- [x] Database optimization — a Settings button that runs `VACUUM` (+ checkpoint
      and `PRAGMA optimize`) to reclaim space, reporting the freed/new size.
- [x] Document attachments — upload one or more files (PDFs, images, …) to
      activities and to individual arrival/departure legs; open PDFs/images
      inline or download other types via a small open icon next to the plus.
      Files are stored as BLOBs in SQLite so they are covered by backups.
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
