//go:build windows

package main

import (
	"encoding/json"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	comdlg32             = windows.NewLazySystemDLL("comdlg32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileNameW = comdlg32.NewProc("GetSaveFileNameW")
	user32               = windows.NewLazySystemDLL("user32.dll")
	procShowWindow       = user32.NewProc("ShowWindow")
)

// OPENFILENAMEW (commdlg.h)
type openFileName struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        unsafe.Pointer
	dwReserved        uint32
	flagsEx           uint32
}

const (
	ofnOverwritePrompt = 0x2
	ofnNoChangeDir     = 0x8
	ofnPathMustExist   = 0x800
	ofnFileMustExist   = 0x1000
	ofnExplorer        = 0x80000
)

// filter im Format "Text|Muster|Text|Muster"
func filterUTF16(f string) *uint16 {
	u := utf16.Encode([]rune(strings.ReplaceAll(f, "|", "\x00") + "\x00\x00"))
	return &u[0]
}

func fileDialog(save bool, title, filter, dir, name, defExt string) (string, bool) {
	buf := make([]uint16, 4096)
	copy(buf, utf16.Encode([]rune(name)))
	t, _ := windows.UTF16PtrFromString(title)
	ofn := openFileName{
		lpstrFilter:  filterUTF16(filter),
		nFilterIndex: 1,
		lpstrFile:    &buf[0],
		nMaxFile:     uint32(len(buf)),
		lpstrTitle:   t,
		flags:        ofnExplorer | ofnNoChangeDir | ofnPathMustExist,
	}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	if wv != nil {
		ofn.hwndOwner = uintptr(wv.Window())
	}
	if dir != "" && dir != "." {
		ofn.lpstrInitialDir, _ = windows.UTF16PtrFromString(dir)
	}
	if defExt != "" {
		ofn.lpstrDefExt, _ = windows.UTF16PtrFromString(defExt)
	}
	proc := procGetOpenFileNameW
	if save {
		proc = procGetSaveFileNameW
		ofn.flags |= ofnOverwritePrompt
	} else {
		ofn.flags |= ofnFileMustExist
	}
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return "", false
	}
	return windows.UTF16ToString(buf), true
}

func openFileDialog(title, filter, dir string) (string, bool) {
	return fileDialog(false, title, filter, dir, "", "")
}

func saveFileDialog(title, filter, dir, name, defExt string) (string, bool) {
	return fileDialog(true, title, filter, dir, name, defExt)
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	// U+2028/2029 sind in JSON erlaubt, in JS-Quelltext aber Zeilenumbrueche
	s := strings.ReplaceAll(string(b), " ", ` `)
	return strings.ReplaceAll(s, " ", ` `)
}
