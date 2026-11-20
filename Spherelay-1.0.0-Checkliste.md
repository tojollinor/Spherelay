# Spherelay 1.0.0 – Änderungsliste

## Installer und Update

- [x] Vollinstaller für Windows x64 mit NSIS-Assistent, Original-Kopf- und Seitenbild aus Alpha.28 und eingebetteter Spherelay-Programmdatei.
- [x] Übernahme vorhandener Konfiguration durch den Assistenten. FFmpeg und MediaMTX werden bei Bedarf aus geprüften Quellen bezogen.
- [x] GitHub-Update unter **System → Komponenten**. Bei bestehenden Dienstinstallationen startet **Update installieren** das Update nach der Windows-Adminabfrage ohne Setup-Assistenten.
- [x] Ergebnis nach Update oder Installation mit erkannter Version, gestarteter Dienstinstanz und RTSP-Prüfung zuvor erreichbarer Streams. Auffällige Streams lassen sich aus dem Bericht in der Diagnose öffnen.

## Kameras und Suche

- [x] Einklappbare Netzwerksuche mit **Netzwerk durchsuchen** im Kopfbereich.
- [x] RTSP, ONVIF und Herstellersuche einzeln auswählbar. RTSP prüft den Standardport 554 und den Standardpfad.
- [x] Einheitliche Treffer mit Hersteller, Modell, IP-Adresse und verfügbarer Firmware. Axis-VAPIX und ONVIF-Geräteinformationen ergänzen die Herstellersuche.
- [x] Filter für Hersteller, IP, Verfahren und noch einzurichtende Geräte. Bekannte IP-Adressen bieten **Kamera öffnen**.
- [x] Roter Papierkorb ganz rechts in der Kamerazeile, mit Bestätigung.
- [x] Freiwilliger Kameratest für eine RTSP-Adresse im privaten IPv4-Netz. Das Anlegen ist auch ohne den Test möglich.
- [x] Fehler und nächster Wiederverbindungsversuch an der Kamera. Betroffene Streams verweisen auf die Quelle.

## Ansichten, Streams und Neustarts

- [x] **Online**, **Offline** und **Unhealthy** mit letztem Prüfergebnis, letzter erfolgreicher Prüfung und gegebenenfalls frühestem Wiederverbindungsversuch.
- [x] **Starten**, **Stoppen** und **Neu starten** je Ansicht in der Streamliste und im Ausgabe-Reiter. Bei geteilter Kamera-Pipeline kündigt der Dialog die Unterbrechung weiterer Ansichten an.
- [x] Checkbox **Aktiv** für die Ausgabefreigabe. Manuelles Stoppen bleibt auch nach einem Dienstneustart gespeichert und ändert die Freigabe nicht.
- [x] Sichtbare Arbeitszustände während Start, Stopp, Neustart und Speichern. Widersprüchliche Steueraktionen werden gesperrt.
- [x] Roter Papierkorb ganz rechts je Stream, mit Bestätigung.
- [x] **go1984-Kompatibilität** sauber ausgerichtet, mit aufklappbarer Hilfe zur Verwendung und zum einzelnen Encoder-Thread.
- [x] Nach restartpflichtigen Änderungen an Ansicht, Kamera, Netzwerk oder Logstufe wird der passende Neustart angeboten. **Später** lässt den ausstehenden Neustart sichtbar.

## Diagnose und Sicherung

- [x] Zeitraumfilter und Kopieraktionen bleiben in der oberen Werkzeugleiste. Die einzelnen Meldungen haben keine Kopierbuttons.
- [x] Wiederkehrende identische Warnungen und Fehler werden mit Anzahl sowie erstem und letztem Auftreten zusammengefasst.
- [x] **Diagnose** bei auffälligen Streams öffnet die gefilterten Ereignisse der Ansicht und ihrer Kamera.
- [x] JSON-Sicherung und Wiederherstellung mit Vorschau der Anzahl von Kameras und Ansichten. Login-Daten, Viewer-Passwort, Kamerapasswörter und sensible URL-Parameter werden nicht exportiert.

## Prüfung

- [x] Go-Tests, `go vet`, Windows-x64-Build und JavaScript-Syntax.
- [x] Entpacktes Setup geprüft. Beide Alpha.28-Grafiken sind bytegleich, das eingebettete Programm meldet 1.0.0 und der Uninstaller ist enthalten.
- [x] Installationsvorbereitung mit einer UTF-16-Passwortdatei in einem temporären Testverzeichnis.
- [ ] Vollständige Installation und Neustart des Windows-Diensts auf einem Testsystem.
- [ ] Online-Aktualisierung mit einem veröffentlichten GitHub-Release und Prüfung der Kameraausgaben nach dem Update.

## In 1.0.1 umgesetzt

- [x] Installer-Kopfbereich auf der Fortschrittsseite prüfen. Logo und Überschrift sind im Screenshot nach rechts verschoben. Ausrichtung mit Alpha.28 abgleichen.
- [x] Streamzeilen einheitlich ausrichten. Status, Aktiv-Checkbox und Aktionsbuttons sollen in jeder Zeile an derselben Position stehen. Für die kontextabhängige Aktion **Diagnose** einen festen Platz vorsehen, damit andere Buttons bei **Online** oder **Unhealthy** nicht verrutschen.
- [x] Im Kameradialog **Verbindung testen** links und **Abbrechen** sowie **Speichern** rechts in einer gemeinsamen Fußleiste anordnen. Das Testergebnis in einem eigenen Bereich oberhalb der Leiste anzeigen, mit gleichbleibenden Abständen vor und nach dem Test. Erfolgsbeispiel: „Kamera erreichbar. Eine Videospur wurde gefunden. Die Wiedergabe wurde noch nicht geprüft.“ Fehler verständlich und mit dem konkreten Grund anzeigen.
- [x] Den tatsächlich verwendeten Netzwerkbereich in der Netzwerksuche sichtbar und editierbar anzeigen. Bei mehreren Netzwerkschnittstellen die Bereiche eindeutig kennzeichnen.
- [x] Das globale 35-Sekunden-Limit für die Netzwerksuche entfernen. Alle Ziele im eingestellten Bereich abarbeiten oder auf **Abbrechen** beenden. Einzelne Geräteanfragen behalten ein begrenztes Zeitfenster. Fortschritt und bereits gefundene Geräte sollen auch beim Abbruch erhalten bleiben.
- [x] Suchfortschritt, Abschluss, Abbruch und Hinweise zum gewählten Bereich kompakt darstellen. Doppelte Zeitlimit-Hinweise aus dem bestehenden Ablauf vermeiden.
- [x] Filterleiste und Ergebnisdarstellung der Netzwerksuche anhand beider Screenshots überarbeiten. Filterhöhe und Abstände vereinheitlichen, **Nur neue Geräte** bündig ausrichten sowie Gerätekarten und weitere Webgeräte übersichtlicher gruppieren.
