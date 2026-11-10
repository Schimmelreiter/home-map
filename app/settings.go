//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// settings liegen in %APPDATA%\home-map\einstellungen.json.
// Das MQTT-Passwort wird mit DPAPI fuer das angemeldete Windows-Konto verschluesselt.
type settings struct {
	LastFile    string `json:"zuletzt"`
	AutoConnect bool   `json:"automatisch_verbinden"`
	MQTT        struct {
		Host    string `json:"host"`
		Port    int    `json:"port"`
		User    string `json:"benutzer"`
		PassEnc string `json:"passwort_dpapi,omitempty"`
		Base    string `json:"topic"`
	} `json:"mqtt"`
}

func settingsPath() string { return filepath.Join(appDataDir(false), "einstellungen.json") }

func loadSettings() *settings {
	s := &settings{}
	s.MQTT.Port, s.MQTT.Base = 1883, "zigbee2mqtt"
	if b, err := os.ReadFile(settingsPath()); err == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}

func (s *settings) save() {
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(settingsPath(), b, 0o600)
}

func (s *settings) setPassword(p string) {
	enc, err := dpapi([]byte(p), true)
	if err == nil {
		s.MQTT.PassEnc = base64.StdEncoding.EncodeToString(enc)
	}
}

func (s *settings) password() string {
	raw, err := base64.StdEncoding.DecodeString(s.MQTT.PassEnc)
	if err != nil || len(raw) == 0 {
		return ""
	}
	dec, err := dpapi(raw, false)
	if err != nil {
		return ""
	}
	return string(dec)
}

func dpapi(data []byte, protect bool) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	const uiForbidden = 0x1
	if protect {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, uiForbidden, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, uiForbidden, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
