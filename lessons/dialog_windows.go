//go:build windows

package main

// Native "Open file" dialog through comdlg32.dll (no cgo).

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	ole32                = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetActiveWindow  = user32.NewProc("GetActiveWindow")
)

type openFileNameW struct {
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
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

const (
	ofnExplorer         = 0x00080000
	ofnFileMustExist    = 0x00001000
	ofnPathMustExist    = 0x00000800
	ofnNoChangeDir      = 0x00000008
	ofnAllowMultiSelect = 0x00000200
)

func utf16z(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r == '|' {
			out = append(out, 0)
			continue
		}
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			continue
		}
		out = append(out, uint16(r))
	}
	return append(out, 0, 0)
}

// openFileDialog shows the system Open dialog and returns the chosen paths
// (several when multiple page images are selected). It blocks.
func openFileDialog() ([]string, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	hwnd, _, _ := procGetActiveWindow.Call()
	filter := utf16z("Sheet music, MIDI, MusicXML|*.pdf;*.png;*.jpg;*.jpeg;*.mid;*.midi;*.musicxml;*.xml;*.mxl;*.json|PDF sheet music|*.pdf|Page images|*.png;*.jpg;*.jpeg|MIDI|*.mid;*.midi|MusicXML|*.musicxml;*.xml;*.mxl|All files|*.*")
	buf := make([]uint16, 32*1024)
	title, _ := syscall.UTF16PtrFromString("Open sheet music")
	ofn := openFileNameW{
		hwndOwner:    hwnd,
		lpstrFilter:  &filter[0],
		nFilterIndex: 1,
		lpstrFile:    &buf[0],
		nMaxFile:     uint32(len(buf)),
		lpstrTitle:   title,
		flags:        ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir | ofnAllowMultiSelect,
	}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return nil, false
	}
	// single file: "C:\dir\file.pdf\0"; multiple: "C:\dir\0a.png\0b.png\0\0"
	var parts []string
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] == 0 {
			if i == start {
				break
			}
			parts = append(parts, syscall.UTF16ToString(buf[start:i]))
			start = i + 1
		}
	}
	if len(parts) <= 1 {
		return parts, len(parts) == 1
	}
	dir := parts[0]
	var out []string
	for _, p := range parts[1:] {
		out = append(out, dir+`\`+p)
	}
	return out, true
}

const hasDialog = true
