//go:build windows

package midiin

// MIDI keyboard input through the Windows multimedia API (winmm.dll), which ships with every
// version of Windows, so no drivers, cgo or extra DLLs are needed. Every MIDI input device is
// opened; new devices are picked up while the app runs (hot-plug).

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	winmm                = syscall.NewLazyDLL("winmm.dll")
	procMidiInGetNumDevs = winmm.NewProc("midiInGetNumDevs")
	procMidiInGetDevCaps = winmm.NewProc("midiInGetDevCapsW")
	procMidiInOpen       = winmm.NewProc("midiInOpen")
	procMidiInStart      = winmm.NewProc("midiInStart")
	procMidiInStop       = winmm.NewProc("midiInStop")
	procMidiInReset      = winmm.NewProc("midiInReset")
	procMidiInClose      = winmm.NewProc("midiInClose")
)

const (
	callbackFunction = 0x30000
	mimData          = 0x3C3
	mmsysErrAlloc    = 4
)

type midiInCaps struct {
	Mid, Pid      uint16
	DriverVersion uint32
	Pname         [32]uint16
	Support       uint32
}

type winMIDI struct {
	mu      sync.Mutex
	handles []uintptr
	names   []string
	busy    []string // devices another program has open
	isOpen  map[uintptr]bool
	lastN   int
	sink    func(status, d1, d2 byte)
}

var (
	theMIDI    *winMIDI
	midiCbOnce sync.Once
	midiCb     uintptr
)

// midiInProc runs on a winmm thread for every incoming message.
func midiInProc(h, msg, instance, p1, p2 uintptr) uintptr {
	if msg == mimData && theMIDI != nil && theMIDI.sink != nil {
		theMIDI.sink(byte(p1), byte(p1>>8), byte(p1>>16))
	}
	return 0
}

func Start(sink func(status, d1, d2 byte)) Input {
	midiCbOnce.Do(func() { midiCb = syscall.NewCallback(midiInProc) })
	m := &winMIDI{sink: sink, lastN: -1, isOpen: map[uintptr]bool{}}
	theMIDI = m
	if winmm.Load() != nil {
		return m
	}
	m.Rescan()
	go func() {
		for range time.Tick(1500 * time.Millisecond) {
			n, _, _ := procMidiInGetNumDevs.Call()
			m.mu.Lock()
			changed := int(n) != m.lastN
			retry := len(m.busy) > 0
			m.mu.Unlock()
			if changed {
				m.Rescan()
			} else if retry {
				m.mu.Lock()
				m.busy = nil
				m.openMissing(n)
				m.mu.Unlock()
			}
		}
	}()
	return m
}

func (m *winMIDI) closeAll() {
	for _, h := range m.handles {
		procMidiInStop.Call(h)
		procMidiInReset.Call(h)
		procMidiInClose.Call(h)
	}
	m.handles, m.names, m.busy = nil, nil, nil
	m.isOpen = map[uintptr]bool{}
}

// Rescan closes and reopens every MIDI input device.
func (m *winMIDI) Rescan() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if winmm.Load() != nil {
		return
	}
	m.closeAll()
	// Devices were added or removed: release anything held on the old ones.
	if m.sink != nil {
		m.sink(0xB0, 123, 0)
		m.sink(0xB0, 64, 0)
	}
	n, _, _ := procMidiInGetNumDevs.Call()
	m.lastN = int(n)
	m.openMissing(n)
}

// openMissing opens every device id that is not open yet. Caller holds m.mu.
func (m *winMIDI) openMissing(n uintptr) {
	for id := uintptr(0); id < n; id++ {
		if m.isOpen[id] {
			continue
		}
		var caps midiInCaps
		r, _, _ := procMidiInGetDevCaps.Call(id, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		name := fmt.Sprintf("MIDI device %d", id+1)
		if r == 0 {
			name = syscall.UTF16ToString(caps.Pname[:])
		}
		var h uintptr
		r, _, _ = procMidiInOpen.Call(uintptr(unsafe.Pointer(&h)), id, midiCb, 0, callbackFunction)
		if r != 0 {
			if r == mmsysErrAlloc {
				m.busy = append(m.busy, name)
			}
			continue
		}
		procMidiInStart.Call(h)
		m.isOpen[id] = true
		m.handles = append(m.handles, h)
		m.names = append(m.names, name)
	}
}

func (m *winMIDI) Devices() (open []string, busy []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.names...), append([]string(nil), m.busy...)
}

func (m *winMIDI) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeAll()
}
