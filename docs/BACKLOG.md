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

- [x] **Accurate weather location labels** — treat the destination as the normal
      weather location on days without a stay; show the actual forecast location
      once and reserve a separate warning for genuinely unlocated accommodations.
- [x] **Start the Ideas map at the trip destination** — center Overview on the
      location saved under General, including empty trips; preserve manual views
      during polling and accommodation route fits, and explain missing coordinates.
- [x] **Verify the published image digest** — feed the normalized registry name
      and immutable build digest directly into Trivy; fail the Docker workflow
      when the scan or SARIF upload fails instead of hiding invalid references.
- [x] **Readable weather cards** — remove header guidance/navigation links, move
      attribution below the cards, show saved data timestamps in the title row
      (including mixed-age ranges), and format forecast metrics as labeled rows
      without repeated timestamps or duplicate destination fallback names.

- [x] **Weather availability countdown** — show estimated days until each trip date
      enters the five-day forecast window, in English/German with singular/plural
      wording and timezone/DST-aware calendar-date counting; shared by the weather
      tab and both calendar views without obscuring missing locations or API errors.

- [x] **Trip weather** — free OpenWeatherMap five-day forecasts in a dedicated tab
      and compact day/week calendar summaries; accommodation-based locations with
      explicit destination fallback, inclusive transfer days, durable shared cache,
      bounded lifecycle worker, opt-in 3/6/12-hour refresh and manual Settings refresh.
      Page/status reads never call the provider; errors preserve saved forecasts.

- [x] **Roomier Ideas map and table** — remove redundant guidance/legend text,
      increase map height by 25%, size the table for ten complete rows and add
      thin row separators while preserving scrolling, sorting and route highlighting.

- [x] **Compact idea source labels** — primary-color source badges and a Google Maps
      pin beside the links, replacing visible GPS coordinates while retaining costs.

- [x] **Local POI search and reviewed location proposals** — restaurant/POI filters,
      explicit-town bounds, circular search progress, spelling/location previews with
      accept/reject controls, title-based lookup for empty fields, and persisted
      confirmation-only background proposals with source-safe rejection.

- [x] **Weekly activity resizing** — top/bottom drag handles and keyboard adjustments,
      automatic time persistence and day-view synchronization, cancel/failure rollback,
      and atomic scheduling that preserves source booking details.

- [x] Region-prefixed planning dates — show accommodation regions before each date
      in the Ideas day dropdown, sharing calendar timezone/transfer-day rules and
      localized missing-data labels. Keep submitted dates unchanged and refresh
      labels from saved data only.

- [x] Cached driving values in the Ideas overview — show the shortest saved road
      distance per idea with the paired duration and named origin before selecting a
      stay. Add accessible numeric sorting, centered metrics/day selectors and
      responsive right-aligned, vertically centered card thumbnails. Preserve map
      numbering, viewport, cache-only reads and explicit missing values.

- [x] Consistent page/header width and branding — center all app pages and the header
      at 75% desktop width, keep responsive mobile widths, align the brand left and
      background status right with separated navigation, prefix browser titles with
      the app name, and show a reduced-motion-aware circular spinner only for active work.

- [x] Two-column idea cards and accommodation search markers — display two saved
      places per desktop row, one on mobile, with full-width inline editors.
      Add current-trip accommodation presets and clickable/keyboard-accessible bed
      markers to the AI map; fit and display the current search radius, refresh
      changed bookings, reject removed presets, and support the offline demo.

- [x] Compact Ideas map and direct planning — shorten help, align the origin selector
      and header counters, replace repetitive route-status cells with trip-day selection,
      and show short descriptions below idea titles. Preserve original items and times
      when scheduling. Generate missing bilingual descriptions through a bounded durable
      AI worker, reuse saved results, and expose background progress and failures.

- [x] Route numbers and rich labels — blue idea markers and clickable route labels
      focus the same complete saved route as the road itself. Labels show a circled
      idea number, driving distance and duration, retain collision-free placement,
      and stay stationary on hover/focus. Cover pointer and keyboard activation;
      keep location popups when no saved route is available.

- [x] Route label placement and focus — prevent distance-label overlaps, keep labels
      within the viewport and away from controls/markers, and link shifted labels with
      dashed guides. Reposition after pan/zoom/resize, prioritizing active routes when
      space runs out. Hover/focus uses contrasting magenta; click or Enter/Space fits
      the complete cached road geometry and polling preserves the chosen view.

- [x] Combined location/route refresh — Settings updates missing locations and regions
      alongside failed/incomplete routes for the selected trip, even without a routing
      key. Remove the planner's old hint/refresh block. Routing queues bounded initial
      geocoding before preparing missing pairs, without repeatedly retrying ambiguous
      places. Yellow accessible location warnings open the original idea location editor
      from lists, map tables and day/week planners.

- [x] Ideas map interaction improvements — move missing-route retries to Settings with
      an explicit trip selector, remove both map buttons, and reload cached data on
      accommodation selection. Fit the selected stay, ideas and actual road detours;
      preserve manual view changes during polling. Numbered distance labels and
      route hover/keyboard focus reveal and subtly highlight the matching table row
      without scrolling the page. Preserve successful routes and other trips' caches.

- [x] ORS summary-only response compatibility — use validated summary metrics for
      two-point directions without instruction segments; retain real multi-leg
      breakdowns. Cover failed-cache recovery and persistence without repeat calls.
      Separate read-only map refresh from explicit missing-route retries, and show
      saved/failed/pending counts rather than treating checked attempts as successes.

- [x] Ideas map overview, accommodation zoom and all-route overlays — default full
      overview, zoom to a selected stay and render actual provider road geometry.
      Persist all located accommodation/idea pairs through a paced lifecycle worker,
      discover new/geocoded coordinates automatically, show progress in the header and
      keep GET/poll handlers read-only. Coordinate/provider CAS, explicit failure retries
      and restart-safe results preserve source bookings and avoid repeated lookups.

- [x] Ideas map and driving comparison — all accommodation and saved idea markers,
      including scheduled activities; selectable accommodation origin, dated lodging
      labels, numbered idea popups/table and explicit missing/unavailable route states.
      Background routing, persistent caching and bilingual static demo.

- [x] Background idea geocoding — resolve missing coordinates/regions from saved
      place names and addresses, with exact Wikipedia-coordinate fallback for omitted
      landmarks. Keep ambiguous plans unlocated, preserve concurrent edits and expose
      direct location-editor links in the planner.

- [x] Regional AI search-center dropdown — retain the saved destination default,
      add unique accommodation-region midpoints, and keep custom search/map points.
      Refresh presets after enrichment without resetting search settings; resolve
      selections from current bookings and reject removed regions before provider calls.

- [x] Participant introductions in Cheatsheet — add current selected travelers'
      self-introductions to the standard table, reuse matching custom phrases and
      preserve vocabulary when participants change. Backfill one validated translated
      name frame per cached destination/language through the bounded worker; no names
      sent to the provider and no paid work from GET/poll requests.

- [x] Accommodation marker dates — overview map hover titles and popups include
      the booked check-in/check-out dates in the configured display timezone.

- [x] Calendar accommodation regions and refresh controls — safe cached accommodation
      regions, continuous day/week bands, both regions on transfer days and non-disruptive
      background refresh. Existing ideas can select a geocoded location without losing
      titles, links or payments; missing coordinates and lookup failures are explicit.

- [x] Shared-address lodging lookup — prefer the name-confirmed accommodation over
      restaurants or other POIs at the same address, without hiding intermediate stays.
- [x] Background progress — top-right activity indicator with live geographic batch
      progress and pending translation/discovery status, persistent idle state and
      visible status-fetch failures.
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
