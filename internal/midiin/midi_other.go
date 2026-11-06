//go:build !windows

package midiin

// MIDI input is only implemented for Windows (winmm). Other platforms get a no-op.

type noMIDI struct{}

func Start(sink func(status, d1, d2 byte)) Input { return noMIDI{} }

func (noMIDI) Rescan()                                 {}
func (noMIDI) Devices() (open []string, busy []string) { return nil, nil }
func (noMIDI) Close()                                  {}
