package score

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// ReadMIDI parses a Standard MIDI File. Notes from the first note-bearing
// track are the right hand and the second the left; with a single track the
// hands are split at middle C.
func ReadMIDI(data []byte, title string) (*Song, error) {
	if len(data) < 14 || string(data[:4]) != "MThd" {
		return nil, errors.New("not a MIDI file")
	}
	hlen := int(binary.BigEndian.Uint32(data[4:8]))
	ntrk := int(binary.BigEndian.Uint16(data[10:12]))
	div := int(binary.BigEndian.Uint16(data[12:14]))
	if div&0x8000 != 0 || div == 0 {
		return nil, errors.New("SMPTE-timed MIDI files are not supported")
	}
	pos := 8 + hlen
	type ev struct {
		tick  int
		on    bool
		pitch int
		vel   int
		track int
		ch    int
	}
	var evs []ev
	type tsig struct{ tick, num, den int }
	var sigs []tsig
	tempo := 0.0
	trackNotes := map[int]int{}
	for t := 0; t < ntrk && pos+8 <= len(data); t++ {
		if string(data[pos:pos+4]) != "MTrk" {
			return nil, fmt.Errorf("bad track header at %d", pos)
		}
		l := int(binary.BigEndian.Uint32(data[pos+4 : pos+8]))
		trk := data[pos+8 : min(len(data), pos+8+l)]
		pos += 8 + l
		i, tick := 0, 0
		var status byte
		readVar := func() int {
			v := 0
			for i < len(trk) {
				b := trk[i]
				i++
				v = v<<7 | int(b&0x7f)
				if b&0x80 == 0 {
					break
				}
			}
			return v
		}
		for i < len(trk) {
			tick += readVar()
			if i >= len(trk) {
				break
			}
			b := trk[i]
			if b&0x80 != 0 {
				status = b
				i++
			}
			switch {
			case status == 0xFF:
				if i >= len(trk) {
					break
				}
				typ := trk[i]
				i++
				l := readVar()
				if i+l > len(trk) {
					i = len(trk)
					break
				}
				d := trk[i : i+l]
				i += l
				switch typ {
				case 0x51:
					if len(d) == 3 && tempo == 0 {
						us := int(d[0])<<16 | int(d[1])<<8 | int(d[2])
						tempo = 60e6 / float64(us)
					}
				case 0x58:
					if len(d) >= 2 {
						sigs = append(sigs, tsig{tick, int(d[0]), 1 << d[1]})
					}
				case 0x03:
					if title == "" && t == 0 && len(d) > 0 {
						title = string(d)
					}
				}
			case status == 0xF0 || status == 0xF7:
				l := readVar()
				i += l
			default:
				hi := status & 0xF0
				n := 2
				if hi == 0xC0 || hi == 0xD0 {
					n = 1
				}
				if i+n > len(trk) {
					i = len(trk)
					break
				}
				d1 := int(trk[i])
				d2 := 0
				if n == 2 {
					d2 = int(trk[i+1])
				}
				i += n
				ch := int(status & 0x0F)
				if hi == 0x90 && d2 > 0 {
					evs = append(evs, ev{tick, true, d1, d2, t, ch})
					trackNotes[t]++
				} else if hi == 0x80 || (hi == 0x90 && d2 == 0) {
					evs = append(evs, ev{tick, false, d1, 0, t, ch})
				}
			}
		}
	}
	if len(evs) == 0 {
		return nil, errors.New("no notes in MIDI file")
	}
	// tracks (or channels in a format-0 file) in order of appearance
	var noteTracks []int
	for t := 0; t < ntrk; t++ {
		if trackNotes[t] > 0 {
			noteTracks = append(noteTracks, t)
		}
	}
	useChannels := len(noteTracks) == 1
	chanNotes := map[int]bool{}
	var chans []int
	for _, e := range evs {
		if e.on && !chanNotes[e.ch] && e.ch != 9 {
			chanNotes[e.ch] = true
			chans = append(chans, e.ch)
		}
	}
	sort.Ints(chans)
	splitByPitch := (useChannels && len(chans) < 2) || len(noteTracks) == 0
	handOf := func(e ev) int {
		if useChannels {
			if len(chans) >= 2 && e.ch == chans[1] {
				return Left
			}
			return Right
		}
		if len(noteTracks) >= 2 && e.track == noteTracks[1] {
			return Left
		}
		if len(noteTracks) >= 3 && e.track != noteTracks[0] {
			return Left
		}
		return Right
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].tick < evs[j].tick })
	s := &Song{Title: title, Tempo: 90, BeatsPer: 4, Source: "MIDI"}
	if tempo > 0 {
		s.Tempo = tempo
	}
	type key struct{ p, tr, ch int }
	open := map[key][]ev{}
	for _, e := range evs {
		if e.ch == 9 {
			continue // drums
		}
		k := key{e.pitch, e.track, e.ch}
		if e.on {
			open[k] = append(open[k], e)
			continue
		}
		if st := open[k]; len(st) > 0 {
			on := st[0]
			open[k] = st[1:]
			h := handOf(on)
			if splitByPitch {
				h = Right
				if on.pitch < 60 {
					h = Left
				}
			}
			s.Notes = append(s.Notes, Note{
				Pitch: on.pitch, Start: float64(on.tick) / float64(div),
				Dur: float64(e.tick-on.tick) / float64(div), Hand: h, Vel: float64(on.vel) / 127,
			})
		}
	}
	// measures from time signatures
	if len(sigs) == 0 || sigs[0].tick > 0 {
		sigs = append([]tsig{{0, 4, 4}}, sigs...)
	}
	s.BeatsPer = float64(sigs[0].num) * 4 / float64(sigs[0].den)
	s.Sort()
	end := s.End()
	t, si := 0.0, 0
	for t < end+1e-9 {
		for si+1 < len(sigs) && float64(sigs[si+1].tick)/float64(div) <= t+1e-9 {
			si++
		}
		s.Measures = append(s.Measures, t)
		t += float64(sigs[si].num) * 4 / float64(sigs[si].den)
	}
	for i := range s.Notes {
		s.Notes[i].Measure = s.MeasureAt(s.Notes[i].Start)
	}
	return s, nil
}

// WriteMIDI writes a two-track SMF (right hand, left hand).
func WriteMIDI(s *Song) []byte {
	const div = 480
	var out []byte
	out = append(out, "MThd"...)
	out = binary.BigEndian.AppendUint32(out, 6)
	out = binary.BigEndian.AppendUint16(out, 1)
	out = binary.BigEndian.AppendUint16(out, 2)
	out = binary.BigEndian.AppendUint16(out, div)
	for hand := 0; hand < 2; hand++ {
		type e struct {
			tick int
			on   bool
			p, v int
		}
		var es []e
		for _, n := range s.Notes {
			if n.Hand != hand {
				continue
			}
			v := int(n.Vel * 127)
			if v <= 0 {
				v = 80
			}
			a := int(n.Start * div)
			b := int((n.Start + n.Dur) * div)
			es = append(es, e{a, true, n.Pitch, min(v, 127)}, e{max(b, a+1), false, n.Pitch, 0})
		}
		sort.SliceStable(es, func(i, j int) bool {
			if es[i].tick != es[j].tick {
				return es[i].tick < es[j].tick
			}
			return !es[i].on && es[j].on
		})
		var trk []byte
		putVar := func(v int) {
			var buf [5]byte
			n := 0
			buf[4] = byte(v & 0x7f)
			n = 1
			for v >>= 7; v > 0; v >>= 7 {
				buf[4-n] = byte(v&0x7f) | 0x80
				n++
			}
			trk = append(trk, buf[5-n:]...)
		}
		if hand == 0 {
			us := int(60e6 / s.Tempo)
			putVar(0)
			trk = append(trk, 0xFF, 0x51, 3, byte(us>>16), byte(us>>8), byte(us))
		}
		last := 0
		for _, x := range es {
			putVar(x.tick - last)
			last = x.tick
			if x.on {
				trk = append(trk, byte(0x90|hand), byte(x.p), byte(x.v))
			} else {
				trk = append(trk, byte(0x80|hand), byte(x.p), 0)
			}
		}
		putVar(0)
		trk = append(trk, 0xFF, 0x2F, 0)
		out = append(out, "MTrk"...)
		out = binary.BigEndian.AppendUint32(out, uint32(len(trk)))
		out = append(out, trk...)
	}
	return out
}
