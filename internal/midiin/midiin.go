// Package midiin opens every MIDI input device (Windows: winmm.dll) and hands
// raw messages to a callback.
package midiin

// Input is a platform MIDI input backend.
type Input interface {
	Rescan()
	Devices() (open []string, busy []string)
	Close()
}
