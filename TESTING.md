# TESTING — Abnahme-Matrix

Stand: 2026-10-23. Jedes der 20 Abnahmekriterien aus
`designvorschlag-caddy-devproxy.md` §18 ist mindestens einem automatisierten
Test zugeordnet. Integrationstests laufen gegen **echte Caddy-Container**
(2.10 und 2.11), nicht gegen Mocks.

## Ausführen

```sh
make test         # Unit-Tests (Go)
make test-ui      # svelte-check + eslint
make integration  # Docker: caddy:2.10 und caddy:2.11
make lint         # go vet + Frontend + optional golangci-lint
```

Voraussetzungen: Go ≥ 1.23, Node ≥ 22.12, Docker für `integration`.
Ein einzelner Integrationstest:

```sh
CADDY_IMAGE=caddy:2.11 go test -tags=integration ./test/integration -run TestSequence -v
```

### golangci-lint

`make lint` führt `golangci-lint` aus, wenn es im `PATH` liegt. Ein mit Go 1.26
gebautes golangci-lint kann mit einem lokalen Go-1.27-Toolchain beim Type-Check
panicken („file requires newer Go version“). In diesem Fall den Linter mit der
passenden Toolchain starten, z. B.:

```sh
GOTOOLCHAIN=go1.26.5 golangci-lint run ./cmd/... ./internal/...
# 0 issues
```

Das ist ein Toolchain-Mismatch des Linters, kein Befund im Code; `go vet` und
die Tests laufen unabhängig davon.

## Matrix

| # | Kriterium (Design §18) | Automatisierter Test |
|---|---|---|
| 1 | CLI und Web-UI nutzen dieselben fachlichen Operationen | `TestSequence` (CLI `routes add/list` über IPC, UI/API über denselben Owner), `TestCreateEditDeleteUndo` |
| 2 | API-Standard und Konfigurationspriorität | `TestEnvThenFlagPrecedence`, `TestSecurityAndStartup` (explizite Admin-URL/Server), `TestDomainValidation` |
| 3 | Keine Caddyfile-Dateizugriffe/-Apply, keine Systemdienst-Zugriffe; read-only Vorschau erlaubt | `TestNoCaddyfileOrServiceControl`, `TestNoProcessExecutionOutsideEditor` |
| 4 | Zweiter Web-Prozess abgewiesen; CLI über Zustandseigentümer | `TestSequence` (Exit 6 + IPC), `TestInstanceLock` |
| 5 | Fremd belegte UI-Domain wird nicht übernommen | `TestSecurityAndStartup/self_domain_is_not_overwritten` |
| 6 | Nach SIGTERM liefert die UI-Route 503; Dev-Routen bleiben aktiv | `TestSequence` (Selbstroute `static_response 503`, zweite Route weiter 200) |
| 7 | Prozessende macht Passwort/Sessions ungültig | `TestSequence` (alte Session nach Neustart → 401), `TestSessionIdleAndAbsoluteTTL` |
| 8 | Bestehende Routen nur nach ausdrücklicher Übernahme verwaltet | `TestAdoptForeignIDAndRestore`, `TestSequence` (Adopt-Aktion) |
| 9 | Unsichere Kontexte verhindern Übernahme/Wiederherstellung | `TestAdoptBlockedWhenShadowed`, `TestForeignCollisionBlocksRestoreAtAnchor` |
| 10 | Formular erhält unbekannte Felder; JSON bleibt routebezogen | `TestUnknownTLSFieldsPreserved`, `TestExtractBuildRoundtrip`, `TestNormalizationIdempotent` |
| 11 | Fehlende verwaltete Routen werden sicher wiederhergestellt | `TestSequence` (extern löschen → Restore), `TestExternalDeleteRestoreAndTombstone` |
| 12 | Externe Änderungen/Kollisionen erzeugen Konflikte ohne Überschreiben | `TestSequence`, `TestExternalModifyConflictAndThreeResolutions`, `TestDuplicateIDConflict` |
| 13 | Drei Konfliktaktionen inkl. `block_reason` | `TestSequence` (alle drei), `TestDuplicateIDConflict`, `TestForeignCollisionBlocksRestoreAtAnchor` |
| 14 | Tool-Delete/Unmanage verhindern Wiederherstellung | `TestExternalDeleteRestoreAndTombstone`, `TestAdoptForeignIDAndRestore` |
| 15 | Undo überlebt Neustart, gemeinsamer Zustand | `TestSequence` (Undo nach Neustart), `TestCreateEditDeleteUndo` |
| 16 | Systemoperationen überschreiben Undo nicht | `TestSequence` (Undo-Beschreibung vor/nach automatischem Restore identisch) |
| 17 | Teilfehler zwischen Caddy und Speicher werden geklärt | `TestJournalRecoveryMatrix` (4 Fälle), `TestUnclearJournalStartup`, `TestPreviewStaleOnModifiedTarget`, `TestPreconditionRetry` |
| 18 | Templates ohne ausführbare Skripte; Änderungen wirken nicht auf Routen | `TestLocalOverrideAndUnknownPlaceholder` (`exec`-Feld wird abgelehnt), `TestRenderMCPHub` (erzeugte Route ist ein Snapshot) |
| 19 | Ohne Web-Prozess kein dauerhafter Abgleich | `TestSequence` (einmalige CLI nach externem Delete stellt nicht wieder her) |
| 20 | Keine globalen Einstellungen; fehlende Voraussetzungen verständlich | `TestSecurityAndStartup` (fehlender Etag, Domain-Konflikt), `TestNoCaddyfileOrServiceControl` |

## Weitere Tests

| Bereich | Test |
|---|---|
| Etag/If-Match, 412 | `TestEtagRoundtripAndPrecondition`, `TestEtagTrailer`, `TestCheckRequiresEtag`, `TestPreconditionRetry` |
| Normalisierung/Kollisionen | `TestNormalizationIdempotent`, `TestNormalizeIdempotentRandom`, `TestHostCollision`, `TestPathOverlap`, `TestRoutesCollide`, `TestListensPublic` |
| Klassifikation/Diff/Redaction | `TestClassifyJSONOnly`, `TestTerminalFalseIsJSONOnly`, `TestRedactWholeRoute`, `TestDiffAndRedaction` |
| Zustandsdateien/Locks | `TestStoreRoundtripAndModes`, `TestNewerSchemaRejected`, `TestInstanceLock`, `TestStateLockSerializes`, `TestTokenCreated0600` |
| Undo-Identität | `TestUndoAdoptAssignedID`, `TestUndoBlockedByExternalChange`, `TestCreateEditDeleteUndo` |
| Auth/Rate-Limit/CSRF | `TestWrongPasswordAndRateLimit`, `TestSessionIdleAndAbsoluteTTL`, `TestRotateInvalidatesOtherSessions`, plus CSRF-Check in `TestSequence` |
| Templates | `TestLoadEmbedded`, `TestRenderMCPHub`, `TestLocalOverrideAndUnknownPlaceholder` |
| TLS-Sicherheitsgate | `TestJSONEditCannotBypassAllowInsecure` |
| Eigenroute/Shutdown | `TestSequence`, `TestSecurityAndStartup` |

## Manuelle/E2E-Prüfungen

- **Playwright-Smoke** (optional, benötigt laufende Instanz):
  `DEVPROXY_E2E_BASE_URL=http://localhost:<backend-port> DEVPROXY_E2E_PASSWORD=<startpw> npm run test:e2e`
- **Performance** (Chrome, lokal, Produktionsbuild):
  - JS gesamt ≈ 200 KB gzip, CSS ≈ 6 KB gzip, Fonts ≈ 88 KB → ≈ 293 KB < 400 KB.
  - First Contentful Paint: 104-108 ms (Median 108 ms aus 5 Läufen) < 500 ms, gemessen am Produktionsbuild vom 2026-10-23.
  - Messung: `performance.getEntriesByName('first-contentful-paint')[0].startTime`.
  - Messumgebung: headless Chromium (Playwright 1.64.1), `/login`, `networkidle`, Loopback, je Lauf frischer Kontext; 22 Requests, 244 KB übertragen.
  - CodeMirror 6 wird als eigener Chunk erst beim JSON-Tab geladen.

## Nicht automatisiert / bewusst offen

- Ein echter Browser-E2E-Lauf ist nicht Teil von `make integration` (benötigt
  eine laufende Caddy-/devproxy-Instanz und Browser-Binaries). Der Test ist
  vorhanden und überspringt sich ohne `DEVPROXY_E2E_*`-Variablen.
  Zuletzt geprüft: 2/2 grün gegen eine Wegwerf-Caddy-Instanz (2026-10-23).
- `golangci-lint` läuft nur, wenn installiert; `go vet` ist Pflicht.
- Last-/Mehrbenutzertests sind kein MVP-Ziel (ein Entwickler, ein Server).

## Handler-Editor und Caddyfile

| Bereich | Test |
|---|---|
| Rekursive Routen-Validierung | `TestValidateJSONRouteNested`, `TestValidateJSONRouteRejectsNestedWithoutHandler` |
| Create aus vollständigem Routenbaum | `TestPreviewCreateJSON`, `TestPreviewCreateJSONRejectsBadNestedHandler`, `TestSequence` (nested create) |
| Caddyfile-Renderer | `TestRenderSimpleRoute`, `TestRenderNestedHandlers`, `TestRenderHeaders`, `TestRenderUnknownHandler`, `TestRenderNoHost`, `TestRenderEmptyHost`, `TestRenderDepthLimit`, `TestRenderTerminalFalseWarns` |
| Caddyfile ist syntaktisch gültig | `TestSequence` (generierte Caddyfile gegen Caddy `/adapt`) |
| UI-Editor | `cd webui && npm run check && npm run lint && npm run build`; manuell: verschachtelte Subroute anlegen/umsortieren, Form → JSON-Sync, Caddyfile-Tab, Preview-Dialog, Advanced-Create |
