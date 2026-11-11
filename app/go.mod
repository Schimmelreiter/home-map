module github.com/Schimmelreiter/home-map/app

go 1.26.0

require (
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	golang.org/x/sys v0.50.0
)

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/net v0.44.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
)

// eigene Kopie ohne Laden der WebView2Loader.dll aus dem Speicher (siehe third_party/go-webview2/webviewloader/module.go)
replace github.com/jchv/go-webview2 => ./third_party/go-webview2
