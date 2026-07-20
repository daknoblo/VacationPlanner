# 🌴 VacationPlanner

Ein webbasierter Urlaubsplaner in **Go** mit moderner, schlanker Server-Rendering-Architektur
(HTMX + Leaflet), PostgreSQL-Persistenz, OpenAI-kompatiblen KI-Empfehlungen und einem
**multi-arch, distroless** Docker-Image.

## Features

- **Urlaube verwalten** – mehrere geplante Reisen mit Zeitraum (Von/Bis), Reiseziel und Notizen.
- **An- & Abreise** – Reiseabschnitte mit Verkehrsmittel, Start/Ziel und Ab-/Ankunftszeiten.
- **Sehenswürdigkeiten** – Punkte von Interesse mit Kategorie, Beschreibung, Datum, Notizen und
  „besucht"-Status.
- **Interaktive Karte** – Leaflet + OpenStreetMap. Marker für alle Sehenswürdigkeiten; Klick auf
  die Karte übernimmt Koordinaten für neue Einträge.
- **KI-Empfehlungen** – über jeden **OpenAI-kompatiblen** Endpunkt (OpenAI, Azure OpenAI, Ollama,
  LocalAI, vLLM …). Vorschläge lassen sich per Klick als Sehenswürdigkeit übernehmen.
- **Notizfelder** – frei nutzbare Notizen auf Urlaubs-, Reise- und Sehenswürdigkeiten-Ebene.
- **Sicherheit by default** – CSRF-Schutz, strikte Security-Header inkl. CSP, Rate-Limiting,
  Request-Limits, non-root distroless Container.

## Tech-Stack

| Bereich        | Technologie                                                        |
| -------------- | ------------------------------------------------------------------ |
| Sprache        | Go 1.25 (statisches Binary, `CGO_ENABLED=0`)                       |
| Routing        | `chi/v5` + Standard-`net/http`                                     |
| Datenbank      | PostgreSQL via `pgx/v5` (pure Go, kein CGO), eingebettete Migrationen |
| Frontend       | Server-gerendertes `html/template` + **HTMX** + **Leaflet** (vendored) |
| KI             | OpenAI-kompatible `/chat/completions` (konfigurierbar)             |
| Container      | Multi-Stage → `gcr.io/distroless/static-debian12:nonroot`, multi-arch |
| Qualität       | golangci-lint (v2), gosec, govulncheck, CodeQL, Trivy              |

## Architektur

```mermaid
flowchart LR
    Browser["Browser<br/>(HTMX + Leaflet)"] -->|HTTP| Server["Go HTTP Server<br/>chi + html/template"]
    Server --> Store["Store<br/>pgx/v5"]
    Store --> DB[("PostgreSQL")]
    Server -->|/chat/completions| AI["OpenAI-kompatibler<br/>Endpunkt"]
    Server -->|embed| Assets["Templates + Static<br/>(im Binary)"]
```

Alle Templates und statischen Assets (inkl. Leaflet & HTMX) werden per `//go:embed` in das
Binary eingebettet – das Image bleibt vollständig eigenständig und offline-fähig.

## Schnellstart (Docker Compose)

Voraussetzung: Docker mit laufendem Daemon.

```bash
# optional: KI aktivieren
export OPENAI_API_KEY=sk-...
# empfohlen für Produktion:
export CSRF_KEY=$(openssl rand -hex 32)

docker compose up --build
```

Danach: <http://localhost:8080>

## Lokale Entwicklung (ohne Container)

```bash
# 1) PostgreSQL bereitstellen (Beispiel via Docker)
docker run --rm -p 5432:5432 \
  -e POSTGRES_USER=vacation -e POSTGRES_PASSWORD=vacation -e POSTGRES_DB=vacation \
  postgres:16-alpine

# 2) Konfiguration
cp .env.example .env   # Werte anpassen; DATABASE_URL zeigt auf localhost
set -a && source .env && set +a

# 3) Starten (Migrationen laufen automatisch beim Start)
make run     # oder: go run ./cmd/server
```

Weitere Ziele: `make help` (build, test, lint, sec, vuln, docker-build, docker-buildx, up, down).

## Konfiguration

| Variable          | Default                     | Beschreibung                                             |
| ----------------- | --------------------------- | ------------------------------------------------------- |
| `APP_ENV`         | `development`               | `production` aktiviert JSON-Logs, HSTS, Secure-Cookies. |
| `HTTP_ADDR`       | `:8080`                     | Listen-Adresse.                                         |
| `DATABASE_URL`    | –                           | **Erforderlich.** `postgres://user:pass@host:5432/db`.  |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | Basis-URL des KI-Endpunkts.                             |
| `OPENAI_API_KEY`  | –                           | Leer ⇒ KI-Funktionen deaktiviert.                       |
| `OPENAI_MODEL`    | `gpt-4o-mini`               | Modellname.                                             |
| `CSRF_KEY`        | ephemer (dev)               | Hex-32-Byte-Key. **In Produktion erforderlich.**        |

### KI-Endpunkt-Beispiele

```bash
# OpenAI
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_API_KEY=sk-...
OPENAI_MODEL=gpt-4o-mini

# Ollama (lokal)
OPENAI_BASE_URL=http://localhost:11434/v1
OPENAI_API_KEY=ollama
OPENAI_MODEL=llama3.1

# Azure OpenAI (OpenAI-kompatibler Pfad)
OPENAI_BASE_URL=https://<resource>.openai.azure.com/openai/deployments/<deployment>
OPENAI_API_KEY=<key>
```

## Sicherheit

- **CSRF**: zustandsloser, HMAC-signierter Double-Submit-Token; verpflichtend für alle
  verändernden Requests (`POST/PUT/PATCH/DELETE`).
- **Security-Header**: strikte `Content-Security-Policy` (self-hosted Skripte/Styles, nur
  OSM-Kacheln extern), `X-Content-Type-Options`, `X-Frame-Options: DENY`, `Referrer-Policy`,
  COOP/CORP, `Permissions-Policy`, HSTS (in Produktion).
- **Robustheit**: Rate-Limiting pro IP, Request-Body-Limit, Timeouts, Graceful Shutdown.
- **Container**: distroless, non-root, statisches Binary, keine Shell.
- **Ausgabe-Escaping**: `html/template` escaped automatisch – auch KI-generierte Inhalte werden
  nie als Roh-HTML gerendert (Schutz gegen XSS / Prompt-Injection-Auswirkungen).

> Hinweis: Es gibt (noch) keine Authentifizierung. Betreibe die App hinter einem
> Reverse-Proxy/Ingress mit TLS und – falls nötig – Zugriffsschutz.

## Tests & Qualität

```bash
go test -race ./...     # Unit-Tests inkl. Template-Rendering, CSRF, KI-Parsing
golangci-lint run       # Linter-Suite (inkl. gosec)
govulncheck ./...        # bekannte Schwachstellen in Abhängigkeiten
gofmt -l .              # Formatprüfung
```

## CI/CD (GitHub Actions)

- **CI** (`ci.yml`): Format, `go vet`, Build, Tests (Race + Coverage), golangci-lint,
  govulncheck, gosec (SARIF), Trivy-Dateiscan.
- **CodeQL** (`codeql.yml`): statische Sicherheitsanalyse.
- **Docker** (`docker-publish.yml`): multi-arch (`amd64` + `arm64`) Build & Push nach GHCR mit
  SBOM + Provenance, anschließend Trivy-Image-Scan.
- **Dependabot**: wöchentliche Updates für Go-Module, GitHub-Actions und Docker.

## Projektstruktur

```
cmd/server/            main + Health-Probe-Subcommand
internal/
  ai/                  OpenAI-kompatibler Client
  config/              Env-Konfiguration & Logger
  models/              Domänen-Typen
  server/              Routing, Middleware, CSRF, Rendering, Handler
  store/               PostgreSQL-Store + eingebettete Migrationen
web/
  templates/           Layout, Seiten, Partials
  static/              CSS, JS, vendored Leaflet/HTMX
.github/workflows/     CI, CodeQL, Docker
Dockerfile             Multi-Stage, multi-arch, distroless
docker-compose.yml     App + PostgreSQL
```

## Lizenz

Noch nicht festgelegt – bei Bedarf eine `LICENSE` ergänzen.