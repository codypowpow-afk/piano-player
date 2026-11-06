// Package score holds the note data a lesson is built from, plus readers for
// MIDI and MusicXML files.
package score

import (
	"encoding/json"
	"math"
	"os"
	"sort"
)

const (
	Right = 0
	Left  = 1
)

// Note is one note to play. Times are in beats (quarter notes).
type Note struct {
	Pitch   int     `json:"p"`
	Start   float64 `json:"s"`
	Dur     float64 `json:"d"`
	Hand    int     `json:"h"`
	Measure int     `json:"m"`
	Vel     float64 `json:"v,omitempty"`
	// where the note came from on the page (OMR only), for review overlays
	Page int     `json:"pg,omitempty"`
	X    float32 `json:"x,omitempty"`
	Y    float32 `json:"y,omitempty"`
}

// Song is a playable piece.
type Song struct {
	Title     string    `json:"title"`
	Source    string    `json:"source,omitempty"`
	Tempo     float64   `json:"tempo"`              // suggested quarter-note BPM
	BeatsPer  float64   `json:"beats"`              // beats per measure (display)
	KeyFifths int       `json:"key"`                // -7..7
	Measures  []float64 `json:"measures,omitempty"` // start beat of each measure
	Notes     []Note    `json:"notes"`
	Warnings  []string  `json:"warnings,omitempty"`
}

// Step is a group of notes that start together.
type Step struct {
	Start float64
	Notes []int // indices into Song.Notes
}

// Sort orders notes by start then pitch.
func (s *Song) Sort() {
	sort.SliceStable(s.Notes, func(i, j int) bool {
		a, b := s.Notes[i], s.Notes[j]
		if math.Abs(a.Start-b.Start) > 1e-6 {
			return a.Start < b.Start
		}
		return a.Pitch < b.Pitch
	})
}

// Steps groups notes of the given hands (mask bit 0 = right, bit 1 = left)
// that start within eps beats of each other.
func (s *Song) Steps(handMask int, eps float64) []Step {
	var out []Step
	for i, n := range s.Notes {
		if handMask&(1<<n.Hand) == 0 {
			continue
		}
		if len(out) > 0 && n.Start-out[len(out)-1].Start <= eps {
			out[len(out)-1].Notes = append(out[len(out)-1].Notes, i)
			continue
		}
		out = append(out, Step{Start: n.Start, Notes: []int{i}})
	}
	return out
}

// End returns the time the last note ends.
func (s *Song) End() float64 {
	e := 0.0
	for _, n := range s.Notes {
		e = math.Max(e, n.Start+n.Dur)
	}
	return e
}

// MeasureAt returns the measure index containing beat t.
func (s *Song) MeasureAt(t float64) int {
	i := sort.SearchFloat64s(s.Measures, t+1e-9) - 1
	if i < 0 {
		return 0
	}
	return i
}

// PitchRange returns the lowest and highest pitch.
func (s *Song) PitchRange() (lo, hi int) {
	lo, hi = 127, 0
	for _, n := range s.Notes {
		lo = min(lo, n.Pitch)
		hi = max(hi, n.Pitch)
	}
	return
}

func (s *Song) Save(path string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func LoadJSON(b []byte) ([]*Song, error) {
	var many []*Song
	if err := json.Unmarshal(b, &many); err == nil && len(many) > 0 {
		return many, nil
	}
	var one Song
	if err := json.Unmarshal(b, &one); err != nil {
		return nil, err
	}
	return []*Song{&one}, nil
}

var noteNames = []string{"C", "C#", "D", "Eb", "E", "F", "F#", "G", "Ab", "A", "Bb", "B"}
var sharpNames = []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
var flatNames = []string{"C", "Db", "D", "Eb", "E", "F", "Gb", "G", "Ab", "A", "Bb", "B"}

// NoteName gives e.g. "C4" for MIDI 60.
func NoteName(p int) string {
	return noteNames[((p%12)+12)%12] + itoa(p/12-1)
}

// PitchClassName names a pitch without octave, spelled for the key
// (flats in flat keys, sharps in sharp keys).
func PitchClassName(p, keyFifths int) string {
	pc := ((p % 12) + 12) % 12
	switch {
	case keyFifths < 0:
		return flatNames[pc]
	case keyFifths > 0:
		return sharpNames[pc]
	}
	return noteNames[pc]
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// KeyName names a key signature (major).
func KeyName(fifths int) string {
	names := map[int]string{-7: "C♭", -6: "G♭", -5: "D♭", -4: "A♭", -3: "E♭", -2: "B♭", -1: "F", 0: "C", 1: "G", 2: "D", 3: "A", 4: "E", 5: "B", 6: "F♯", 7: "C♯"}
	minor := map[int]string{-7: "a♭", -6: "e♭", -5: "b♭", -4: "f", -3: "c", -2: "g", -1: "d", 0: "a", 1: "e", 2: "b", 3: "f♯", 4: "c♯", 5: "g♯", 6: "d♯", 7: "a♯"}
	return names[fifths] + " major / " + minor[fifths] + " minor"
}
