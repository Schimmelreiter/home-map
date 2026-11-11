//go:build windows

// home-map – Zigbee-Hausplan als Windows-Programm.
//
// Die Oberflaeche (ui/index.html) laeuft in WebView2 (in Windows 10/11 enthalten).
// Das Programm liefert ihr Dateizugriff (Plan-Datei laden/speichern) und eine
// Live-Verbindung per MQTT zu Zigbee2MQTT.
package main

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed ui/index.html
var indexHTML string

var version = "dev" // wird beim Bauen gesetzt: -ldflags "-X main.version=…"

var (
	wv     webview2.WebView
	cfg    *settings
	live   *liveConn
	backed = map[string]bool{} // je Sitzung einmal .bak anlegen
)

func main() {
	cfg = loadSettings()

	dataDir := filepath.Join(appDataDir(true), "WebView2")
	wv = webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     os.Getenv("HOMEMAP_DEBUG") != "",
		AutoFocus: true,
		DataPath:  dataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "Zigbee-Hausplan",
			Width:  1500,
			Height: 950,
			IconId: 1,
			Center: true,
		},
	})
	if wv == nil {
		hinweis := "Bitte die „Microsoft Edge WebView2 Runtime“ installieren:\nhttps://go.microsoft.com/fwlink/p/?LinkId=2124703"
		if exe, err := os.Executable(); err == nil {
			if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "WebView2Loader.dll")); err != nil {
				hinweis = "WebView2Loader.dll fehlt. Sie muss im selben Ordner wie home-map.exe liegen."
			}
		}
		msgBox("Zigbee-Hausplan", "WebView2 konnte nicht gestartet werden.\n\n"+hinweis, 0x10)
		os.Exit(1)
	}
	defer wv.Destroy()
	maximize()

	// Datei, die beim Start uebergeben wurde (Doppelklick auf *.zplan.json, „Oeffnen mit“)
	if len(os.Args) > 1 {
		if p, err := filepath.Abs(os.Args[1]); err == nil {
			if _, err := os.Stat(p); err == nil {
				cfg.LastFile = p
			}
		}
	}

	live = newLiveConn(func(ev any) { emit(ev) })
	bind()
	wv.SetHtml(indexHTML)
	wv.Run()
	live.disconnect()
}

// emit schickt ein Ereignis an die Oberflaeche (window.hpEvent).
func emit(ev any) {
	js := "window.hpEvent && window.hpEvent(" + jsonString(ev) + ")"
	wv.Dispatch(func() { wv.Eval(js) })
}

func bind() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(wv.Bind("hp_settings", func() map[string]any {
		return map[string]any{
			"host": cfg.MQTT.Host, "port": cfg.MQTT.Port, "user": cfg.MQTT.User, "base": cfg.MQTT.Base,
			"hasPass": cfg.hasPassword(), "autoConnect": cfg.AutoConnect,
			"lastFile": cfg.LastFile, "version": "home-map " + version,
		}
	}))
	must(wv.Bind("hp_load", func(path string) (string, error) {
		b, err := os.ReadFile(path)
		return string(b), err
	}))
	must(wv.Bind("hp_open", func(kind string) (map[string]string, error) {
		title, filter := "Plan-Datei öffnen", "Plan-Datei (*.zplan.json)|*.zplan.json|JSON (*.json)|*.json|Alle Dateien|*.*"
		if kind == "netz" {
			title, filter = "Netzwerkkarte laden", "Netzwerkkarte (*.json)|*.json|Alle Dateien|*.*"
		}
		p, ok := openFileDialog(title, filter, filepath.Dir(cfg.LastFile))
		if !ok {
			return nil, nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		return map[string]string{"path": p, "content": string(b)}, nil
	}))
	must(wv.Bind("hp_save", func(path, content string) (bool, error) {
		return true, writeFile(path, content)
	}))
	must(wv.Bind("hp_saveAs", func(suggest, content string) (string, error) {
		dir := filepath.Dir(cfg.LastFile)
		if filepath.IsAbs(suggest) {
			dir = filepath.Dir(suggest)
		}
		p, ok := saveFileDialog("Plan speichern unter", "Plan-Datei (*.zplan.json)|*.zplan.json", dir, filepath.Base(suggest), "json")
		if !ok {
			return "", nil
		}
		if !strings.HasSuffix(strings.ToLower(p), ".json") {
			p += ".zplan.json"
		}
		return p, writeFile(p, content)
	}))
	must(wv.Bind("hp_setLast", func(path string) bool {
		cfg.LastFile = path
		cfg.save()
		return true
	}))
	must(wv.Bind("hp_mqttConnect", func(c mqttForm) (bool, error) {
		if c.Host == "" {
			return false, errors.New("kein Broker angegeben")
		}
		if c.Port == 0 {
			c.Port = 1883
		}
		if c.Base == "" {
			c.Base = "zigbee2mqtt"
		}
		pass := c.Pass
		if pass == "" && c.Host == cfg.MQTT.Host && c.User == cfg.MQTT.User {
			pass = cfg.password()
		}
		cfg.MQTT.Host, cfg.MQTT.Port, cfg.MQTT.User, cfg.MQTT.Base = c.Host, c.Port, c.User, c.Base
		if c.Pass != "" {
			cfg.setPassword(c.Pass)
		}
		cfg.AutoConnect = true
		cfg.save()
		go live.connect(c.Host, c.Port, c.User, pass, c.Base)
		return true, nil
	}))
	must(wv.Bind("hp_mqttDisconnect", func() bool {
		cfg.AutoConnect = false
		cfg.save()
		go live.disconnect()
		return true
	}))
	must(wv.Bind("hp_networkmap", func(routes bool) (bool, error) {
		return true, live.requestNetworkMap(routes)
	}))
}

type mqttForm struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
	Base string `json:"base"`
}

// writeFile schreibt atomar (temporaere Datei + Umbenennen). Beim ersten Speichern
// einer Sitzung bleibt der vorige Stand als <name>.bak erhalten.
func writeFile(path, content string) error {
	if !backed[path] {
		if old, err := os.ReadFile(path); err == nil {
			_ = os.WriteFile(path+".bak", old, 0o644)
		}
		backed[path] = true
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // ersetzt unter Windows eine vorhandene Datei
}

func appDataDir(local bool) string {
	base := os.Getenv("APPDATA")
	if local {
		base = os.Getenv("LOCALAPPDATA")
	}
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	d := filepath.Join(base, "home-map")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func msgBox(title, text string, flags uint32) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	_, _ = windows.MessageBox(0, m, t, flags)
}

func maximize() {
	const swMaximize = 3
	h := uintptr(wv.Window())
	if h != 0 {
		procShowWindow.Call(h, swMaximize)
	}
}
