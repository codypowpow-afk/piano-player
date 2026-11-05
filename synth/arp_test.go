package synth

import (
	"fmt"
	"strings"
	"testing"
)

func run(s ArpSettings, notes []int, steps int) (string, []int64) {
	a := newArp()
	a.s = s
	a.s.On = true
	for _, n := range notes {
		a.press(n, 0.7)
	}
	var seq []string
	var times []int64
	var cur []string
	lastT := int64(-1)
	t := int64(0)
	for len(times) < steps+1 && t < SampleRate*60 {
		a.tick(blockSize, func(n int, v float64) {
			if t != lastT {
				if cur != nil {
					seq = append(seq, strings.Join(cur, "+"))
				}
				cur = nil
				times = append(times, t)
				lastT = t
			}
			cur = append(cur, fmt.Sprint(n))
		}, func(int) {})
		t += blockSize
	}
	return strings.Join(seq, " "), times
}

func TestArpPatterns(t *testing.T) {
	ceg := []int{60, 64, 67}
	for m, name := range ArpModes {
		s := DefaultArp()
		s.Mode, s.Octaves = m, 1
		if name == "Up" || name == "Alberti" {
			s.Octaves = 2
		}
		seq, _ := run(s, ceg, 9)
		fmt.Printf("%-10s %s\n", name, seq)
	}
	// Timing: 120 BPM 1/16 = 6000 samples; swing 0.3 -> 7800 / 4200.
	s := DefaultArp()
	s.BPM, s.Rate, s.Mode = 120, 3, 0
	_, tm := run(s, ceg, 4)
	fmt.Println("straight 1/16 @120 step starts:", tm[:4])
	s.Swing = 0.3
	_, tm = run(s, ceg, 4)
	fmt.Println("swing 30%:                     ", tm[:4])
}
