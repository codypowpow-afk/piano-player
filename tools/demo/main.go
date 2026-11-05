// Command demo renders a short passage on every preset to WAV files and reports CPU cost.
//
//	go run ./tools/demo [outdir]
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codypowpow-afk/piano-player/assets"
	"github.com/codypowpow-afk/piano-player/synth"
)

type ev struct {
	t    float64
	note int
	vel  float64
	dur  float64
}

func passage() ([]ev, []float64) {
	var evs []ev
	// Arpeggiated chords with the pedal, then a melody, then a low chord and a scale run.
	chords := [][]int{{48, 55, 60, 64, 67}, {45, 52, 57, 60, 64}, {41, 48, 53, 57, 60}, {43, 50, 55, 59, 62}}
	t := 0.0
	for _, c := range chords {
		for i, n := range c {
			evs = append(evs, ev{t + float64(i)*0.12, n, 0.45 + 0.08*float64(i), 1.6})
		}
		t += 1.6
	}
	mel := []int{72, 74, 76, 79, 77, 76, 74, 72}
	for i, n := range mel {
		evs = append(evs, ev{t + float64(i)*0.3, n, 0.7, 0.28})
	}
	t += 2.6
	for _, n := range []int{24, 36, 43, 48, 52, 55} {
		evs = append(evs, ev{t, n, 0.95, 2.5})
	}
	t += 3
	for i := 0; i < 15; i++ {
		evs = append(evs, ev{t + float64(i)*0.09, 84 + []int{0, 2, 4, 5, 7, 9, 11}[i%7] + 12*(i/7), 0.6, 0.08})
	}
	// Pedal: down during the chord section only.
	pedal := []float64{0.0, 6.35}
	return evs, pedal
}

func main() {
	out := "."
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	evs, pedal := passage()
	const length = 17.0
	only := map[string]bool{}
	for _, a := range os.Args[2:] {
		only[a] = true
	}
	for pi, p := range synth.Presets {
		if len(only) > 0 && !only[p.Name] {
			continue
		}
		e := synth.NewEngine(assets.FS)
		lt := time.Now()
		if err := e.SetPreset(pi); err != nil {
			panic(err)
		}
		loadT := time.Since(lt)
		total := int(length * synth.SampleRate)
		data := make([]float32, total*2)
		chunk := 256
		start := time.Now()
		maxVoices := 0
		for f := 0; f < total; f += chunk {
			t := float64(f) / synth.SampleRate
			t2 := float64(f+chunk) / synth.SampleRate
			if pedal[0] >= t && pedal[0] < t2 {
				e.SetPedal(true)
			}
			if pedal[1] >= t && pedal[1] < t2 {
				e.SetPedal(false)
			}
			for _, x := range evs {
				if x.t >= t && x.t < t2 {
					e.NoteOn(x.note, x.vel)
				}
				if x.t+x.dur >= t && x.t+x.dur < t2 {
					e.NoteOff(x.note)
				}
			}
			end := f + chunk
			if end > total {
				end = total
			}
			e.RenderF32(data[f*2 : end*2])
			if v := e.ActiveVoices(); v > maxVoices {
				maxVoices = v
			}
		}
		el := time.Since(start)
		peak, sq := 0.0, 0.0
		for _, s := range data {
			a := math.Abs(float64(s))
			if a > peak {
				peak = a
			}
			sq += float64(s) * float64(s)
		}
		rms := math.Sqrt(sq / float64(len(data)))
		name := filepath.Join(out, strings.ReplaceAll(strings.ToLower(p.Name), " ", "-")+".wav")
		writeWav(name, data)
		fmt.Printf("%-18s load %5.2fs  cpu %4.1f%%  peak %5.1f dBFS  rms %5.1f dBFS  voices %d\n",
			p.Name, loadT.Seconds(), 100*el.Seconds()/length, 20*math.Log10(peak), 20*math.Log10(rms), maxVoices)
		_ = name
	}
}

func writeWav(name string, data []float32) {
	f, err := os.Create(name)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	n := len(data)
	w := func(v any) { binary.Write(f, binary.LittleEndian, v) }
	f.WriteString("RIFF")
	w(uint32(36 + n*2))
	f.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(2))
	w(uint32(synth.SampleRate))
	w(uint32(synth.SampleRate * 4))
	w(uint16(4))
	w(uint16(16))
	f.WriteString("data")
	w(uint32(n * 2))
	buf := make([]int16, n)
	for i, s := range data {
		buf[i] = int16(math.Max(-1, math.Min(1, float64(s))) * 32767)
	}
	w(buf)
}
