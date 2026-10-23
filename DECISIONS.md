# DECISIONS — bewusste Spec-Abweichungen

Stand: 2026-10-23. Bindende Reihenfolge bei Widerspruch:
`devproxy-final-plan.md` > `devproxy-spec.md` > `designvorschlag-caddy-devproxy.md`.
Wo die Spec technisch nicht erfüllbar war oder eine sicherere Variante
existiert, ist die Entscheidung hier dokumentiert.

## D1 — `/id/<id>` ist mit der geforderten ID-Form nicht adressierbar

**Spec:** `@id = devproxy/r/<uuidv7>`, Einzelzugriff bevorzugt über `/id/<id>`.
**Befund:** Caddy ≥ 2.10 wertet bei `/id/` nur das **erste** Pfadsegment als ID
(`admin.go`, `handleConfigID`: `id := parts[2]`). Eine ID mit `/` ist darüber
nicht erreichbar; `%2F` wird von `r.URL.Path` dekodiert.
**Entscheidung:** Die vorgegebene ID-Form bleibt. Routen werden über die volle
Routenliste des Servers gelesen und per `@id`-Scan gefunden; geschrieben wird
die Routenliste mit `If-Match` auf den Server-Scope. Das ist ohnehin nötig für
die DUP_ID-Prüfung und damit sicherer als ein Einzel-Lookup. Verifikation nach
dem Schreiben nutzt denselben Listen-Scan.

## D2 — Startpasswort: 128 Bit statt 20 Zeichen

**Spec:** 128 Bit Zufall, base32, 20 Zeichen.
**Befund:** 20 Base32-Zeichen tragen nur 100 Bit. 128 Bit benötigen 26 Zeichen.
**Entscheidung:** 16 Zufallsbytes → 26 Base32-Zeichen (Alphabet A–Z/2–7, keine
0/1). Entropie geht vor Zeichenzahl. Das Passwort wird einmal auf stdout
ausgegeben und nie geloggt.

## D3 — Bestehende Routenliste wird mit PATCH ersetzt

**Befund:** Caddy's `PUT` auf einen bereits existierenden Schlüssel liefert
409 (`key already exists`); nur `PATCH` ersetzt. `PUT` auf einen Array-Index
fügt ein.
**Entscheidung:** `internal/caddy.Client` bietet `PATCH`. Der Zustandseigentümer
liest den Server-Scope (Etag), baut die neue Routenliste und schreibt sie mit
`PATCH` + `If-Match`; existiert der `routes`-Schlüssel noch nicht, wird `PUT`
verwendet. Fremde Routen werden dabei unverändert mitgeschrieben.

## D4 — Preview/Apply-API statt REST-PATCH/DELETE

**Spec-Skizze:** `PATCH /api/v1/routes/{id}`, `DELETE ...`, `POST .../adopt`.
**Befund:** IDs enthalten `/`. Go's `ServeMux` unterstützt `{id...}` nur am
Ende eines Patterns; `POST /routes/{id}/adopt` ist damit nicht eindeutig.
**Entscheidung:** Alle Mutationen laufen über
`POST /api/v1/routes/preview` (dry run, liefert Diff + Bestätigungsgrundlage)
und `POST /api/v1/routes/apply` (führt die bestätigte Vorschau aus). Konflikte
über `POST /api/v1/conflicts/resolve` → gleiche Preview/Apply-Pipeline.
`GET /api/v1/routes/{id...}` bleibt für Lesezugriffe; die UI nutzt zusätzlich
`GET /api/v1/routes?id=...`, um kodierte Slashes robust zu vermeiden.
Die fachlichen Operationen und Sicherheitsgarantien sind identisch.

## D5 — OpJournal-Felder und Phase `unclear`

**Spec:** Phasen `prepared`/`caddy_written`; bei fehlendem Match
Routenstatus `OP_UNCLEAR`.
**Entscheidung:** Das Journal hat zusätzlich `local_only`, `clear_undo` und die
Persistenzphase `unclear`. `local_only` kennzeichnet Operationen ohne
Caddy-Schreibzugriff (take-current, unmanage); `clear_undo` lässt die Recovery
das Undo nach einer Undo-Ausführung löschen. `unclear` hält den Klärungsbedarf
über Neustarts fest.

## D6 — Eine unklare Operation blockiert alle Mutationen

**Spec:** Abgleich für die betroffene Route gesperrt.
**Befund:** Es gibt genau einen Journal-Slot. Ein neuer Write würde den alten
unklaren Zustand überschreiben.
**Entscheidung:** Solange `phase=unclear` gilt, werden alle Mutationen mit
`JOURNAL_OPEN` abgelehnt außer den drei Konfliktaktionen für genau diese Route.
Der Abgleich überspringt die Route. Das ist strenger als die Spec, verhindert
aber Datenverlust.

## D7 — Keine IDNA-/Punycode-Konvertierung

**Spec:** `domain`-Parameter IDNA-valide.
**Entscheidung:** Aus Gründen der festen Dependency-Liste wird eine
konservative ASCII-Hostnamen-Prüfung verwendet (Labels, Länge, Zeichen). Wer
internationale Domains braucht, kann Punycode (`xn--…`) eingeben. Upgrade-Pfad:
`golang.org/x/net/idna` ergänzen.

## D8 — `--domain` bei Routen-Befehlen

**Spec:** `web --domain` (UI-Domain) und `routes add --domain` (Routen-Host).
**Entscheidung:** `web` registriert `--domain`; Routen-/Konflikt-/Undo-/
Template-Befehle registrieren gemeinsame Optionen ohne `--domain` und verwenden
`--domain` für den Routen-Host. `DEVPROXY_DOMAIN` wird bei Routen-Befehlen
ignoriert.

## D9 — Restore-Kollision: nur frühere terminale Routen blockieren

**Spec:** Host-/Pfad-Kollision blockiert Insert/Restore; zusätzlich blockiert
eine frühere fremde terminale Route vor dem Anker.
**Entscheidung:** Neuanlage/Edit/Adopt blockieren bei **jeder** Kollision mit
einer anderen Route. Ein Restore an seinem alten Anker blockiert nur durch
**frühere** terminale Routen; spätere Kollisionen sind die ursprüngliche
Reihenfolge und werden nicht als Konflikt gemeldet. Das entspricht dem
Anker-Modell und verhindert, dass eine legitime Wiederherstellung an einer
späteren Fremdroute scheitert.

## D10 — Redaction ist reine Anzeige

**Spec:** Redaction in UI/CLI, kein Speicherschutz.
**Entscheidung:** Diffs und Logs redigieren Headerwerte anhand der Default-
Globs. Die vollständige Route bleibt im JSON-Editor sichtbar (mit Warnbanner)
und wird im Preview-Objekt an den lokalen Client zurückgegeben, damit Apply
nicht auf redigierten Daten arbeitet. `state.json`, `undo.json` und
`opjournal.json` sind nicht verschlüsselt (wie in der Spec als nicht zugesagt
markiert).

## D11 — Caddy-Version ist über die Admin-API nicht abrufbar

**Spec-UI:** Status zeigt „Serverversion“.
**Befund:** Die Admin-API liefert keine Caddy-Version; `caddy version` ist per
Produktgrenze verboten.
**Entscheidung:** Angezeigt werden devproxy-Version, Server-Name und
Caddy-Erreichbarkeit. Die geprüfte Mindestversion ist 2.8.0 (Etag-Header).

## D12 — SvelteKit 3: `$lib`-Alias ist deprecated

**Befund:** SvelteKit 3 (installiert 3.1) entfernt `$lib` zugunsten von
`#lib`-Subpath-Imports mit Dateiendungen. Für `.svelte.ts`-Module und
`.svelte`-Komponenten ist die Abbildung über `package.json#imports` nicht
einheitlich möglich.
**Entscheidung:** `alias: { $lib: 'src/lib' }` im `sveltekit`-Plugin. Es warnt
beim Build, funktioniert aber vollständig. Upgrade: Importe auf `#lib` mit
expliziten Endungen umstellen, sobald SvelteKit eine einheitliche Regel für
`.svelte`/`.svelte.ts` dokumentiert.

## D13 — Secure-Cookies und direkter Backend-Zugriff

**Spec:** Session-Cookie immer `Secure`.
**Folge:** Über reines HTTP ist die UI nur auf `localhost` (Browser-Ausnahme)
direkt am Backend nutzbar; der reguläre Weg ist HTTPS über Caddy. Es gibt
bewusst keinen `--insecure-cookie`-Schalter. Integrationstests setzen den
Session-Cookie-Header manuell, um die Sicherheitsattribute nicht abzuschwächen.

## D14 — Absolute Session-Frist inklusiv

Die absolute TTL von 12 h beendet die Session bei `>= 12h`; die Idle-TTL von
30 min bei `> 30min`. Damit ist „spätestens nach 12 h“ exakt erfüllt.

## D15 — Rate-Limit konkretisiert

5 Fehlversuche innerhalb von 60 s pro Quell-IP. Danach Sperre 30 s,
verdoppelnd pro weiterer Runde, maximal 15 min. Erfolgreicher Login setzt den
Zähler zurück. Die Sperre gilt auch für das richtige Passwort, bis sie abläuft.

## D16 — Template `strip_prefix` ist boolesch

Das Template-Feld `strip_prefix: true` entfernt den `path_prefix` vor dem
Upstream. Die gerenderte Route setzt `rewrite.strip_path_prefix` auf den Präfix
ohne `*`. Das entspricht dem Wizard-Modell; das CLI-Formular nutzt weiterhin
`--strip-prefix` zusammen mit `--path-prefix`.

## D17 — Endpunkt `GET /api/v1/routes?id=`

Wegen D1/D4 nutzt die UI für die Detailansicht den Query-Parameter. Der
Pfad-Endpunkt `GET /api/v1/routes/{id...}` existiert ebenfalls und funktioniert
mit kodierten Slashes; der Query-Parameter ist die robustere Variante für
Browser-Routing.

## D18 — Integrationsbuilds

Integrationstests tragen `//go:build integration` und werden von `make test`
nicht ausgeführt. `make integration` startet Caddy-Container mit
`--network host` auf freien Ports und prüft 2.10 sowie 2.11. `webui/go.mod`
existiert nur, damit `go test ./...` nicht in `node_modules` absteigt.

## D19 — Apply prüft das Ziel, nicht blind die Preview-Etag

**Spec:** Bestätigung bindet an die Etag der Vorschau; die Mutation sendet
genau diese Etag.
**Entscheidung:** Apply liest ohnehin frisch (Schritt 4.1) und sendet die
aktuelle Etag als `If-Match`. Zusätzlich wird das **Zielobjekt** gegen das
`Base`-Bild der Vorschau geprüft. Ändert sich nur eine fremde Route, wird nach
frischem Read sicher weitergeschrieben (bis 3×). Ändert sich das Ziel, endet
Apply sofort mit `PREVIEW_STALE`; UI/CLI erzeugen eine neue Vorschau und
verlangen erneut Bestätigung. Das verhindert blindes Überschreiben und erfüllt
die Absicht der Etag-Bindung ohne unnötige Blockade durch unbeteiligte
Änderungen.

## D20 — Abgleichintervall in der UI nur lesbar

Das Intervall ist Startparameter (`--reconcile-interval`) und wird in den
Settings angezeigt. Eine Laufzeitänderung ist nicht implementiert; sie würde
einen Neustart des Loop-Tickers und eine zusätzliche API benötigen. Änderung
per Neustart mit Flag ist die sichere, kleinste Variante.

## D21 — Formular-Edit erhält unbekannte TLS-Felder über `extra_tls`

Unbekannte TLS-Felder werden beim Lesen in `Spec.ExtraTLS` gesammelt und über
die JSON-API (`extra_tls`) zurückgeschickt. Damit gehen sie beim Formular-Edit
über CLI oder UI nicht verloren. Andere unbekannte Handler-/Matcher-Felder
führen im kanonischen `Spec`-Formular weiterhin zu `JSON_ONLY` (nicht
editierbar, verlustfrei sichtbar). Für den strukturierten UI-Editor gilt D24;
er bearbeitet diese Felder über generische Advanced-Felder.

## D22 — Formular nur für `terminal: true`

Die kanonische Form (§2.3) verlangt `terminal: true`. Fehlt das Feld oder ist es
`false`, wird die Route als `JSON_ONLY` klassifiziert (`TERMINAL_FALSE`). Ein
Formular-Edit würde sonst stillschweigend die Priorität gegenüber Folgerouten
ändern. Übernahme und exakte Wiederherstellung bleiben möglich; editiert wird
über das Routen-JSON. Für den strukturierten UI-Editor gilt D24; dort ist
`terminal: false` sichtbar und bearbeitbar.

## D23 — Caddy 2.6/2.7: Etag als HTTP-Trailer

**Spec:** Caddy ≥ 2.8.0 ist Pflicht; der Start wird verweigert, wenn
`GET /config/` keinen `Etag`-**Header** liefert.
**Befund:** Der Dev-Server läuft mit Caddy **2.6.2**. Dort wird derselbe
Etag-Wert als HTTP-Trailer gesendet (`Trailer: ETag`, Wert nach dem Body). Die
Admin-API und `If-Match` funktionieren unverändert.
**Entscheidung:** Der Client akzeptiert den Etag aus dem Response-Header
**oder** aus dem Trailer (nach vollständigem Lesen des Bodys). Nur wenn beides
fehlt, schlägt der Start weiterhin fehl. Die optimistische
Nebenläufigkeitsprüfung bleibt identisch: Der Trailer-Wert wird als
`If-Match` verwendet und von Caddy gegen den Server-Scope geprüft. `README`
nennt 2.8+ als empfohlen und 2.6/2.7 als Kompatibilitätsmodus; die
Integrationstests laufen weiterhin gegen 2.10 und 2.11. Upgrade auf ≥ 2.8
bleibt der unterstützte Pfad.

## D24 — Strukturierter Handler-Editor im Web-UI

**Spec:** §2.3/§7.2 beschreiben eine kanonische Form (eine Domain, ein
Upstream, optionale Header/TLS) und `JSON_ONLY` für alles andere. D21/D22
setzen das für das alte `Spec`-Formular und die CLI um.
**Erweiterung:** Der Form-Tab der Route-Detailseite bearbeitet jetzt den
vollständigen Routenbaum über `PreviewEditJSON`: typisierte Felder für
`reverse_proxy`, `headers`, `rewrite`, `encode`, `subroute` und
`static_response`, ein generischer Schlüssel/Wert-Editor für unbekannte
Handler/Felder und rekursive `subroute.routes`. `terminal: false` ist damit
im strukturierten Editor ebenfalls bearbeitbar.
**Sicherheit:** Das Routen-JSON bleibt die einzige Datenquelle; unbekannte
Felder werden nicht verworfen. Der Server validiert den Baum rekursiv
(`ValidateJSONRoute`, Tiefenlimit 50), das Insecure-Gate und die
Kollisionsprüfung gelten unverändert. Fremde Routen werden beim Speichern
adoptiert; `devproxy/self` bleibt gesperrt. Die kanonische `Spec`-Form und
`JSON_ONLY` bleiben für CLI, Quick-Create und die Listenklassifikation
bestehen.
**Kosten:** Der Editor kann Routen erzeugen, die Caddy erst beim Apply
ablehnt; die verpflichtende Vorschau und Caddys Validierung fangen das ab.
Das ist nicht unsicherer als der bereits vorhandene JSON-Editor.

## D25 — Caddyfile-Vorschau ist eine read-only Näherung

**Befund:** Caddy kann JSON nicht offiziell nach Caddyfile exportieren; der
Admin-Endpunkt `/adapt` und `caddy adapt` gehen nur Caddyfile → JSON. Die
Caddy-Dokumentation führt die Caddyfile ausdrücklich als „cannot be
exported“.
**Entscheidung:** Ein eigenes, rein lesendes Renderer-Paket erzeugt aus einer
Route eine Caddyfile-Näherung. Bekannte Handler werden gerendert; unbekannte
Handler, Matcher und Felder erscheinen als Kommentar und Warnung. Ein
Tiefenlimit (32) verhindert pathologische Rekursion. Die Ausgabe wird nie
gelesen, gespeichert oder angewendet; es gibt keinen Caddyfile-Import.
**Tests:** Unit-Tests für einfache, verschachtelte und unbekannte Handler
sowie das Tiefenlimit; ein Integrationstest schickt die erzeugte Caddyfile
an Caddys `/adapt`, um die Syntax zu prüfen.
**Kosten:** Die Vorschau ist semantisch, nicht byte-identisch, und kann
sensible Headerwerte sichtbar machen. Die UI kennzeichnet sie entsprechend.
