//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// settings liegen in %APPDATA%\home-map\einstellungen.json.
type settings struct {
	LastFile    string `json:"zuletzt"`
	AutoConnect bool   `json:"automatisch_verbinden"`
	MQTT        struct {
		Host    string `json:"host"`
		Port    int    `json:"port"`
		User    string `json:"benutzer"`
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

// Das MQTT-Passwort liegt in der Windows-Anmeldeinformationsverwaltung
// (Systemsteuerung > Anmeldeinformationsverwaltung > Windows-Anmeldeinformationen, „home-map/mqtt“).
const credTarget = "home-map/mqtt"

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredFree   = advapi32.NewProc("CredFree")
)

// CREDENTIALW (wincred.h)
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

const (
	credTypeGeneric      = 1
	credPersistLocalMach = 2
)

func (s *settings) setPassword(p string) {
	if p == "" {
		return
	}
	blob := []byte(p)
	t, _ := windows.UTF16PtrFromString(credTarget)
	u, _ := windows.UTF16PtrFromString(s.MQTT.User)
	c := credential{Type: credTypeGeneric, TargetName: t, UserName: u, Persist: credPersistLocalMach,
		CredentialBlobSize: uint32(len(blob)), CredentialBlob: &blob[0]}
	procCredWriteW.Call(uintptr(unsafe.Pointer(&c)), 0)
}

func (s *settings) password() string {
	t, _ := windows.UTF16PtrFromString(credTarget)
	var pc *credential
	r, _, _ := procCredReadW.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&pc)))
	if r == 0 || pc == nil {
		return ""
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pc)))
	if pc.CredentialBlobSize == 0 {
		return ""
	}
	return string(unsafe.Slice(pc.CredentialBlob, pc.CredentialBlobSize))
}

func (s *settings) hasPassword() bool { return s.password() != "" }
