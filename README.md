# caddykit

Ein einzelnes Go-Binary, das ausgewählte Caddy-Proxy-Routen für lokale
Entwicklerdienste verwaltet – über die Caddy-Admin-API, mit CLI und Web-UI,
einem lokalen Sollzustand, Undo und sicherer Wiederherstellung.

Kein Caddyfile-Dateizugriff und kein Anwenden von Caddyfiles, aber eine
read-only Caddyfile-Vorschau; kein systemd, keine globalen
Caddy-Einstellungen, kein Mehrbenutzerbetrieb. Siehe
[Produktgrenzen](#produktgrenzen).

## Voraussetzungen

| Bereich | Anforderung |
|---|---|
| Caddy | **≥ 2.8.0 empfohlen** (Etag als Response-Header); 2.6/2.7 mit Etag als Trailer laufen im Kompatibilitätsmodus (getestet: 2.10/2.11) |
| Betrieb | Linux amd64 oder arm64 |
| Build | Go ≥ 1.23, Node ≥ 22.12, npm |
| Integrationstests | Docker (Caddy 2.10 und 2.11) |

`caddykit` spricht ausschließlich mit der Caddy-Admin-API. Caddy muss bereits
laufen und die gewünschte Domain per HTTPS bereitstellen; Zertifikate und
globale Caddy-Konfiguration werden nicht angefasst.

## Bauen und testen

```sh
make build        # Frontend (Svelte) + Go-Binary für amd64 und arm64 -> dist/
make test         # Go-Unit-Tests
make lint         # go vet, svelte-check, eslint (+ golangci-lint, falls installiert)
make integration  # echte Caddy-Container: 2.10 und 2.11
```

`make build` erzeugt:

```
dist/caddykit-linux-amd64
dist/caddykit-linux-arm64
```

Die Web-UI ist in das Binary eingebettet (`embed`); zur Laufzeit wird weder
Node noch ein separates Frontend benötigt.

## Schnellstart

```sh
./dist/caddykit-linux-amd64 web --domain proxy.dev.example.com
```

Beim Start:

1. exklusive Instanzsperre,
2. offenes OpJournal klären,
3. Caddy-Erreichbarkeit und Etag prüfen,
4. eigenes Startpasswort erzeugen,
5. Domain-Konflikt prüfen,
6. `caddykit/self` anlegen/reparieren,
7. Backend auf `127.0.0.1:<ephemeral>` binden,
8. URL und Passwort **einmal** auf stdout ausgeben,
9. initialer Abgleich, danach alle 10 s (±20 % Jitter).

```
https://proxy.dev.example.com
Startpasswort: XXXX…
```

Die UI läuft unter der angegebenen Domain über Caddy. Ein HTTP-Fallback gibt es
nicht. `caddykit/self` wird beim Beenden auf eine statische 503-Antwort
umgestellt; die Routen der Entwicklerdienste bleiben aktiv.

## Konfiguration

Priorität: **CLI-Argument > Umgebungsvariable > Standard**.

| Parameter | Argument | Env | Default |
|---|---|---|---|
| Caddy Admin-API | `--caddy-admin` | `CADDYKIT_CADDY_ADMIN` | `http://localhost:2019` |
| Web-UI-Domain | `--domain` | `CADDYKIT_DOMAIN` | — (Pflicht für `web`) |
| Datenverzeichnis | `--data-dir` | `CADDYKIT_DATA_DIR` | `$XDG_DATA_HOME/caddykit` |
| Template-Verzeichnis | `--template-dir` | `CADDYKIT_TEMPLATE_DIR` | `$XDG_CONFIG_HOME/caddykit/templates` |
| Abgleichintervall | `--reconcile-interval` | `CADDYKIT_RECONCILE_INTERVAL` | `10s` (2s–5m) |
| HTTP-Server | `--server` | `CADDYKIT_SERVER` | Autodetect (`:80`/`:443`, genau ein Kandidat) |
| Remote-Admin | `--allow-remote-admin` | — | aus; nur HTTPS oder Unix-Socket |
| Unsicheres Upstream-TLS | `--allow-insecure` | — | aus |

Remote-Admin-Endpunkte werden ohne `--allow-remote-admin` abgelehnt; mit Flag
ist nur HTTPS oder ein Unix-Socket erlaubt. Unix-Sockets verwenden die
Caddy-Notation `unix//run/caddy/admin.sock`.

### Environment-Variablen

Priorität: **CLI-Argument > Umgebungsvariable > Standard**. Variablen werden
nur beim Prozessstart gelesen; eine Änderung erfordert einen Neustart.
Leere Variablen gelten als nicht gesetzt.

#### Von caddykit selbst

| Variable | Gelesen von | Bedeutung | Standard / Fallback |
|---|---|---|---|
| `CADDYKIT_CADDY_ADMIN` | `web`, CLI | Caddy-Admin-API; `http(s)://…` oder `unix//pfad/zum.sock` | `http://localhost:2019` |
| `CADDYKIT_DOMAIN` | `web` | Öffentliche UI-Domain; Pflicht für `web` | — (dann `--domain` nötig) |
| `CADDYKIT_DATA_DIR` | `web`, CLI | Zustand, Undo, Journal, Locks, IPC-Token, Socket-Fallback | `$XDG_DATA_HOME/caddykit`, sonst `$HOME/.local/share/caddykit` |
| `CADDYKIT_TEMPLATE_DIR` | `web`, CLI | Lokale Templates (haben Vorrang vor eingebetteten) | `$XDG_CONFIG_HOME/caddykit/templates`, sonst `$HOME/.config/caddykit/templates` |
| `CADDYKIT_RECONCILE_INTERVAL` | `web` | Abgleichintervall als Go-Duration (`10s`, `2m`, `1h30m`) | `10s`; gültig 2s–5m |
| `CADDYKIT_SERVER` | `web`, CLI | Name des Caddy-HTTP-Servers | Autodetect (`:80`/`:443`, genau ein Kandidat); One-Shot-CLI nutzt sonst den in `state.json` gespeicherten Server |
| `XDG_DATA_HOME` | `web`, CLI | Basis für `CADDYKIT_DATA_DIR`, falls dieses nicht gesetzt ist | `$HOME/.local/share` |
| `XDG_CONFIG_HOME` | `web`, CLI | Basis für `CADDYKIT_TEMPLATE_DIR`, falls dieses nicht gesetzt ist | `$HOME/.config` |
| `XDG_RUNTIME_DIR` | `web`, CLI | Verzeichnis für den IPC-Socket; Socket-Datei Mode 0600 | Datenverzeichnis, wenn nicht gesetzt oder kein Verzeichnis |
| `HOME` | `web`, CLI | Fallback für die XDG-Pfade | —; fehlt `HOME`, werden relative Pfade (`.local/share/caddykit`, `.config/caddykit/templates`) benutzt |
| `EDITOR` | CLI | Editor für `routes edit --json-edit` | `vi` |
| `TMPDIR` | CLI | Temporärdatei für den JSON-Editor (Mode 0600, wird gelöscht) | Go-Standard, üblich `/tmp` |
| `PATH` | CLI | Auflösung des Editor-Kommandos aus `$EDITOR` | Prozess-PATH |
| `SSL_CERT_FILE` | `web`, CLI | Zusätzliche CA-Datei für **Remote-**HTTPS-Admin-Endpunkte (Go/x509) | System-Truststore |
| `SSL_CERT_DIR` | `web`, CLI | Zusätzliches CA-Verzeichnis für Remote-HTTPS-Admin-Endpunkte | System-Truststore |

Details und Stolperfallen:

- **`CADDYKIT_DOMAIN` gilt nur für `web`.** Bei `routes …` ist `--domain` der
  Hostname der Route; diese Befehle ignorieren `CADDYKIT_DOMAIN`.
- **Ungültiges `CADDYKIT_RECONCILE_INTERVAL`** (z. B. ohne Einheit) wird
  stillschweigend ignoriert; der Default `10s` greift. Ein syntaktisch gültiger,
  aber außerhalb 2s–5m liegender Wert führt zu einem Startfehler (Exit 2).
- **Pfade werden nicht expandiert.** `~` wird nicht aufgelöst; relative Pfade
  beziehen sich auf das Arbeitsverzeichnis des Prozesses. Das Datenverzeichnis
  wird mit Mode 0700 angelegt. Das Runtime-Verzeichnis muss existieren und
  beschreibbar sein; sonst wird der Socket im Datenverzeichnis angelegt.
- **`CADDYKIT_TEMPLATE_DIR` muss nicht existieren.** Fehlt es oder ist es leer,
  werden nur die eingebetteten Default-Templates (`mcphub`, `omniroute`)
  verwendet. Lokale YAML-Dateien gleicher ID haben Vorrang.
- **`XDG_RUNTIME_DIR` muss für Web-Prozess und CLI identisch sein**, sonst
  findet die CLI den IPC-Socket nicht. Sie prüft dann die Instanzsperre und
  endet mit Exit 6, wenn der Web-Prozess läuft.
- **Bei laufendem Web-Prozess leitet die CLI nur weiter.** Caddy-Admin,
  Server, Template-Verzeichnis und Abgleichintervall kommen dann vom
  Web-Prozess; die CLI-Flags/Env dieser Parameter ändern den laufenden
  Eigentümer nicht. `CADDYKIT_DATA_DIR` und `XDG_RUNTIME_DIR` müssen dagegen
  übereinstimmen, damit die CLI den Socket findet.
- **`EDITOR` wird an Leerzeichen getrennt**, nicht durch eine Shell. Argumente
  mit Leerzeichen/Quoting werden nicht unterstützt. Beispiel:
  `EDITOR="/usr/bin/nano -w"`.
- **Remote-Admin:** `SSL_CERT_FILE`/`SSL_CERT_DIR` wirken nur bei einem
  `https://`-Ziel. Für `http://localhost` oder Unix-Sockets sind sie irrelevant.
  `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` und `ALL_PROXY` werden **nicht**
  ausgewertet; der Admin-Client nutzt keinen Proxy.
- **Keine Env-Varianten** gibt es bewusst für `--allow-remote-admin` und
  `--allow-insecure`; beide sind sicherheitsrelevante, explizite CLI-Flags.
- `TZ` beeinflusst lediglich Zeitstempel in Logausgaben (Go-Standard), nicht
  die Fachlogik. Die UI zeigt Zeiten im Browser-lokalen Format.

Beispiel:

```sh
export CADDYKIT_CADDY_ADMIN=http://127.0.0.1:2019
export CADDYKIT_DATA_DIR="$HOME/.caddykit"
export CADDYKIT_TEMPLATE_DIR="$HOME/.config/caddykit/templates"
export CADDYKIT_DOMAIN=proxy.dev.example.com
export CADDYKIT_RECONCILE_INTERVAL=15s
export EDITOR="vi"

caddykit web
```

#### Nur für Build und Tests

Diese Variablen werden nicht vom `caddykit`-Binary gelesen, sondern von
`Makefile`, Integrationstests oder Playwright:

| Variable | Verwendet von | Bedeutung | Standard |
|---|---|---|---|
| `GO` | `Makefile` | Go-Kommando | `go` |
| `NPM` | `Makefile` | npm-Kommando | `npm` |
| `VERSION` | `Makefile` | Version via `-X main.version=…` | `git describe --tags --always --dirty`, sonst `dev` |
| `CADDY_IMAGE` | `test/integration` | Caddy-Container-Image | `caddy:2.10`; `make integration` setzt nacheinander `caddy:2.10` und `caddy:2.11` |
| `CADDYKIT_E2E_BASE_URL` | Playwright | Basis-URL einer laufenden Instanz | — (Tests werden übersprungen) |
| `CADDYKIT_E2E_PASSWORD` | Playwright | Startpasswort für den E2E-Login | — (Tests werden übersprungen) |
| `PATH` | Build/Tests | `go`, `npm`, `docker`, Editor | Prozess-PATH |

`make integration` verwendet die Docker-CLI; deren eigene Variablen (z. B.
`DOCKER_HOST`) gelten, werden aber nicht von caddykit ausgewertet.
`GOTOOLCHAIN` ist eine Go-Standardvariable; Hinweise zum golangci-lint-Mismatch
stehen in [TESTING.md](TESTING.md).

## CLI

```text
caddykit web --domain <d> [Optionen]

caddykit routes list [--all] [--json]
caddykit routes show <route> [--json]
caddykit routes add --domain <d> --upstream <host:port> [Optionen]
caddykit routes add --template <name> --param k=v ...
caddykit routes edit <route> [--json-edit | Formular-Flags]
caddykit routes adopt <route|host> [--yes]
caddykit routes unmanage <route> [--yes]
caddykit routes delete <route> [--yes]

caddykit conflicts list [--json]
caddykit conflicts resolve <route> --action take-current|apply-desired|unmanage [--yes]

caddykit undo [--yes] [--json]
caddykit templates list [--json]
caddykit templates show <name> [--json]
```

Formular-Flags für `add`/`edit`: `--path-prefix`, `--strip-prefix`,
`--upstream-tls`, `--tls-server-name`, `--insecure-skip-verify`,
`--set-request-header K=V`, `--del-request-header K`,
`--set-response-header K=V`, `--del-response-header K`.
`--json-edit` öffnet das Routenobjekt in `$EDITOR` (Default `vi`) und öffnet
bei Validierungsfehlern erneut.

### Selektor

`<route>` ist ein UUID-Präfix (≥ 8 Zeichen, eindeutig), ein exaktes `@id` oder
ein Hostname (nur wenn eindeutig; sonst Kandidatenliste).

### Exit-Codes

| Code | Bedeutung |
|---|---|
| 0 | ok |
| 1 | interner Fehler |
| 2 | Usage/Validierung |
| 3 | Konflikt, Bestätigung nötig, Undo blockiert |
| 4 | Caddy nicht erreichbar |
| 5 | Auth/Lock/IPC |
| 6 | zweite Instanz |

Nicht-interaktiv ohne `--yes` endet mit 3. `--json` liefert maschinenlesbare
Ausgaben inklusive Diff; mit `--json` und ohne `--yes` wird nur die Vorschau
ausgegeben.

Läuft der Web-Prozess, leitet die CLI alle Aktionen über den Unix-Socket an den
Zustandseigentümer weiter. Läuft er nicht, führt die CLI einen einmaligen
Vorgang unter `caddykit.lock` und `state.lock` aus – ohne Abgleich und ohne UI.

## Web-UI

Dark-first, dicht, Keyboard-First. Seiten: Login, Dashboard, Route anlegen
(Quick + Advanced), Template-Wizard, Route-Detail (strukturierter
Handler-Editor/JSON/Caddyfile), Konfliktcenter, Undo, Templates, Settings.

- `⌘K`/`Ctrl+K`: Command-Palette
- `g d`, `g c`, `g u`: Dashboard, Konflikte, Undo
- `/`: Filter fokussieren
- Status-Polling alle 5 s und bei `visibilitychange`
- JSON-Tab lädt CodeMirror 6 erst bei Bedarf
- `caddykit/self` ist als Systemroute geschützt
- 409 `PREVIEW_STALE` → Vorschau wird erneuert und erneut bestätigt

### Handler-Editor

Der Form-Tab der Route-Detailseite bearbeitet den vollständigen Routenbaum:

- Match: Host, Pfad und Header-Matcher; weitere Matcher über „Advanced“.
- Handler hinzufügen, entfernen und umsortieren: `reverse_proxy`, `headers`,
  `rewrite`, `encode`, `subroute` (rekursiv mit eigenen Routen),
  `static_response`, `file_server` und freie Handler.
- Unbekannte Handler und Felder bleiben erhalten und sind über die
  Advanced-Felder pro Objekt editierbar; der JSON-Tab bleibt der vollständige
  Fallback.
- Fremde Routen werden beim Speichern adoptiert; `caddykit/self` bleibt
  gesperrt.

### Caddyfile-Vorschau

Der Caddyfile-Tab und der Preview-Dialog rendern die Route als lesbare
Caddyfile-Näherung. Caddy kann JSON nicht offiziell nach Caddyfile
exportieren; unbekannte Handler und nicht abbildbare Felder erscheinen als
Kommentar und Warnung. Die Ausgabe ist **read-only** und wird nie an Caddy
gesendet oder angewendet. Sie kann sensible Headerwerte sichtbar machen.

Gemessen: ca. **206 KB gzip JS + 6 KB gzip CSS + 87 KB Fonts** (Summe ≈ 299 KB,
Budget 400 KB). Die frühere FCP-Messung lag bei ca. **76 ms** (Chrome, warmer
Server); nach dem Handler-Editor wurde sie nicht neu gemessen. Details in
[TESTING.md](TESTING.md).

## Wie es funktioniert

- **Sollzustand** (`state.json`): nur ausdrücklich verwaltete Routen
  (`create`, `adopt`, `template`) mit stabilem `@id`, Snapshot und Anker.
- **Identität**: neue Routen `caddykit/r/<uuidv7>`; fremde `@id` werden bei
  Übernahme nie überschrieben.
- **Schreiben**: Fresh-Read + Etag → Diff → Bestätigung → `If-Match`-Write →
  Verifikation. 412 wird bis zu 3× mit frischem Read wiederholt; ändert sich
  das Ziel zwischenzeitlich, wird die Vorschau ungültig.
- **OpJournal**: `prepared → caddy_written`; nach Absturz entscheidet ein
  Vergleich von Caddy-Ist mit Vorher/Nachher-Bild. Kein eindeutiger Match ⇒
  `OP_UNCLEAR` und Schreibsperre bis zur Klärung.
- **Abgleich**: nur im Web-Prozess. Fehlende verwaltete Routen werden bei
  sicherem Kontext automatisch wiederhergestellt; fremde Routen werden nie
  gelöscht. Externe Änderungen, Kollisionen, doppelte `@id` und unklare
  Operationen erzeugen Konflikte statt Überschreiben.
- **Undo**: genau ein persistenter Benutzerschritt, überlebt Neustarts, prüft
  vor der Rücknahme den aktuellen Ist-Zustand. Automatische Restores,
  Eigenroute-Lifecycle und Passwortrotation überschreiben Undo nicht. Kein Redo.
- **Konfliktauflösung**: `take-current`, `apply-desired`, `unmanage` – jeweils
  mit frischer Vorschau und Bestätigung; nicht anwendbare Aktionen tragen einen
  maschinenlesbaren `block_reason`.

## Sicherheit

- Datenverzeichnis `0700`, alle Zustandsdateien `0600`; atomare Writes
  (Temp + fsync + rename).
- Exklusive `flock`-Instanzsperre; zweiter Web-Prozess ⇒ Exit 6.
- Startpasswort: 128 Bit Zufall, einmal auf stdout, nie im Log; konstante
  Vergleichszeit; Rate-Limit 5 Fehlversuche/Minute/IP mit exponentiellem
  Backoff bis 15 min.
- Sessions serverseitig, Cookie `Secure; HttpOnly; SameSite=Strict`, Idle 30 min,
  absolut 12 h, Prozessende ⇒ ungültig.
- CSRF: `caddykit_csrf` (nicht HttpOnly) + `X-CSRF-Token`, serverseitig geprüft.
- IPC: Unix-Socket `0600`, Token-Datei `0600`, Header `X-Devproxy-Token` für
  Schreibzugriffe.
- Sensible Headerwerte werden in Vorschauen/Diffs redigiert (Anzeige, kein
  Speicherschutz). Der JSON-Editor warnt vor sichtbaren Werten.
- Keine Startpasswörter, Session-Tokens oder IPC-Tokens in regulären Logs.

## Dateien

Im Datenverzeichnis:

| Datei | Inhalt |
|---|---|
| `state.json` | Sollzustand (Server + verwaltete Routen) |
| `undo.json` | genau ein Undo-Schritt |
| `opjournal.json` | max. eine offene Operation |
| `caddykit.lock` | Prozesssperre |
| `state.lock` | kurze Dateitransaktionen |
| `ipc.token` | Token für die lokale CLI-Schnittstelle |
| `caddykit.sock` | Unix-Socket (bevorzugt `$XDG_RUNTIME_DIR`) |

## Templates

YAML-Dateien, eine pro Template. Eingebettete Defaults: `mcphub` (Port 3000)
und `omniroute` (Port 20128). Ein lokales Verzeichnis (`--template-dir`) hat
Vorrang. Kein `exec`, kein Skripting: reine `{{param}}`-Substitution nach
Typvalidierung (`string`, `domain`, `hostport`, `port`, `bool`, `enum(...)`).
`sensitive: true`-Werte werden in Vorschauen redigiert. Eine erzeugte Route ist
danach vollständig unabhängig; Template-Änderungen wirken nie auf bestehende
Routen.

## Produktgrenzen

Kein Caddyfile-Dateizugriff, kein Caddyfile-Import/-Apply, kein systemd, kein
Multi-Server, keine Benutzerverwaltung, kein Container-/Prozessmanagement,
keine Remote-Registry, kein Redo, kein permanenter Hintergrunddienst ohne
`web`, keine globalen Caddy-Änderungen, kein HTTP-Fallback für die UI.
Quick-Create deckt eine Domain, einen Upstream, optional einen Pfad und die
dokumentierten Header-/TLS-Felder ab. Der strukturierte Handler-Editor im
Route-Detail bearbeitet beliebige Handler und Verschachtelung; unbekannte
Felder bleiben über Advanced-Felder und den JSON-Tab erhalten. Die
Caddyfile-Ansicht ist eine read-only Näherung und wird nie angewendet.

## Dokumentation

- [TESTING.md](TESTING.md) – Abnahme-Matrix der 20 Kriterien
- [DECISIONS.md](DECISIONS.md) – bewusste Spec-Abweichungen und Begründungen
- `devproxy-final-plan.md`, `devproxy-spec.md`,
  `designvorschlag-caddy-devproxy.md` – bindende Planungsdokumente (Projektinput)
