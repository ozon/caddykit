# Handler-Editor und Caddyfile-Preview — Design

Datum: 2026-10-23
Status: im Chat freigegeben

## Ziel

Bestehende Caddy-Routen sollen im UI vollständig sichtbar und als
verschachtelter Handler-Baum editierbar sein. Zusätzlich soll eine
read-only Caddyfile-Vorschau verfügbar sein.

## Nicht-Ziele

- Kein Caddyfile-Dateizugriff, kein Caddyfile-Import, kein Apply eines
  Caddyfiles.
- Keine globale Caddy-Konfiguration (apps, TLS, storage) bearbeiten.
- Keine neuen Frontend-Abhängigkeiten.
- Kein Ersatz des JSON-Editors; er bleibt der vollständige Fallback.

## UI

Der Form-Tab der Routen-Detailseite wird ein rekursiver strukturierter
Editor:

- Match: Host, Pfad, Header-Matcher; weitere Matcher über „Advanced“.
- Handler-Liste: hinzufügen, entfernen, umsortieren.
- Typisierte Felder für `reverse_proxy`, `headers`, `rewrite`, `encode`,
  `subroute`, `static_response`; unbekannte Handler und Restfelder über
  einen generischen Schlüssel/Wert-Editor bzw. Advanced JSON.
- `subroute.routes` wird rekursiv als verschachtelte Routen gerendert
  (Match + Handler + `terminal`).
- Der gesamte Routen-JSON-Baum bleibt die einzige Datenquelle. Unbekannte
  Felder werden nicht verworfen.
- Fremde Routen werden beim Speichern adoptiert (bestehendes Verhalten);
  `devproxy/self` bleibt gesperrt.
- Der JSON-Tab bleibt unverändert. Ein dritter Tab „Caddyfile“ zeigt die
  Vorschau des aktuellen Entwurfs.

Der Preview-Dialog bekommt einen Umschalter „Diff / Caddyfile“. Der
Quick-Create-Flow bleibt; zusätzlich gibt es einen Advanced-Create-Flow
mit demselben Editor.

## Backend

- `PreviewCreateJSON`: erzeugt aus einem vollständigen Routenbaum eine
  Create-Preview, vergibt die `devproxy/r/<uuidv7>`-ID und prüft
  Kollisionen, Insecure-Gate und ETag wie der bestehende Create-Pfad.
- `ValidateJSONRoute` validiert verschachtelte `subroute.routes`
  rekursiv (Tiefenlimit, nicht-leere Handler, Handler-Name).
- `POST /api/v1/render/caddyfile`: nimmt `{route}` und liefert
  `{caddyfile, warnings}`. Read-only, authentifiziert, ohne
  Caddy-Mutation.

## Caddyfile-Renderer

Caddy kann JSON nicht offiziell nach Caddyfile exportieren (`/adapt` ist
nur Caddyfile → JSON). Der Renderer ist deshalb eine semantische
Näherung:

- Site-Block aus Host-Matchern, `route`-Blöcke für Reihenfolge und
  Verschachtelung, benannte Matcher für Pfad/Header.
- Unterstützt `reverse_proxy`, `headers`, `rewrite` (Strip-Prefix),
  `encode`, `subroute`, `static_response`.
- Unbekannte Handler/Matcher/Felder werden als Kommentar ausgegeben und
  als Warning gemeldet.
- Tiefenlimit gegen pathologische Rekursion.
- Die UI kennzeichnet die Ausgabe sichtbar als Näherung; sie wird nie
  angewendet.

## Tests

- Model: verschachtelte gültige/ungültige Routen.
- Core: Create-Preview aus JSON, Apply, State-Snapshot.
- Render: einfache Route, verschachtelte Route mit mehreren Handlern,
  unbekannter Handler, fehlender Host, Tiefenlimit.
- Integration: Create via `route`, Render-Endpoint, generiertes
  Caddyfile gegen Caddy `/adapt`.
- UI: `svelte-check`, ESLint, Build, manueller Browser-Test.

## Risiken

- Caddyfile-Ausgabe ist nicht byte-identisch und kann bei exotischen
  Handlern unvollständig sein; sie ist ausdrücklich nur Vorschau.
- Der strukturierte Editor muss unbekannte Felder zuverlässig erhalten.
- Fremde Routen zu speichern adoptiert sie; das bleibt sichtbar.
