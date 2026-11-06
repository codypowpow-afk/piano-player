package lesson

import (
	"testing"

	"github.com/codypowpow-afk/piano-player/score"
)

type fakeSound struct {
	on    map[int]bool
	count int
}

func (f *fakeSound) NoteOn(m int, v float64) { f.on[m] = true; f.count++ }
func (f *fakeSound) NoteOff(m int)           { delete(f.on, m) }

func testSong() *score.Song {
	s := &score.Song{Tempo: 60, BeatsPer: 4, Measures: []float64{0, 4}}
	s.Notes = []score.Note{
		{Pitch: 60, Start: 0, Dur: 1, Hand: score.Right},
		{Pitch: 48, Start: 0, Dur: 2, Hand: score.Left},
		{Pitch: 64, Start: 1, Dur: 1, Hand: score.Right},
		{Pitch: 67, Start: 1, Dur: 1, Hand: score.Right},
		{Pitch: 64, Start: 2, Dur: 1, Hand: score.Right},
		{Pitch: 43, Start: 2, Dur: 2, Hand: score.Left},
	}
	return s
}

func run(l *Lesson, secs float64) {
	for t := 0.0; t < secs; t += 0.01 {
		l.Advance(0.01)
	}
}

func TestWaitMode(t *testing.T) {
	snd := &fakeSound{on: map[int]bool{}}
	l := NewLesson(testSong(), snd)
	l.SetHands(HandsRight)
	l.Restart()
	l.Play = true
	run(l, 3)
	if !l.Waiting || l.Pos != 0 || l.K != 0 {
		t.Fatalf("should wait at the first step: waiting=%v pos=%v k=%d", l.Waiting, l.Pos, l.K)
	}
	if !snd.on[48] {
		t.Fatalf("left hand should be auto-played")
	}
	l.Press(62) // wrong key
	if l.Wrong != 1 {
		t.Fatalf("wrong key not counted")
	}
	l.Press(60)
	if l.K != 1 || l.Correct != 1 {
		t.Fatalf("step 1 not completed: k=%d", l.K)
	}
	run(l, 2)
	if l.Pos != 1 || !l.Waiting {
		t.Fatalf("should wait at beat 1, pos=%v", l.Pos)
	}
	l.Press(64)
	if l.K != 1 {
		t.Fatalf("chord needs both notes")
	}
	l.Press(67)
	if l.K != 2 {
		t.Fatalf("chord not completed")
	}
	// same pitch again (E4) needs a fresh press
	run(l, 2)
	if l.K != 2 || l.Pos != 2 {
		t.Fatalf("expected wait at beat 2: k=%d pos=%v", l.K, l.Pos)
	}
	l.Press(64)
	if l.K != 3 {
		t.Fatalf("repeated note not accepted")
	}
	run(l, 5)
	if !l.Finished {
		t.Fatalf("song should finish, pos=%v", l.Pos)
	}
}

func TestSkipAndLoop(t *testing.T) {
	snd := &fakeSound{on: map[int]bool{}}
	l := NewLesson(testSong(), snd)
	l.Restart()
	l.Play = true
	run(l, 2)
	l.Skip()
	if l.K != 1 || l.Skipped != 1 {
		t.Fatal("skip failed")
	}
	l.SetLoop(true)
	if l.LoopA != 0 || l.LoopB < 3.9 {
		t.Fatalf("loop range %v-%v", l.LoopA, l.LoopB)
	}
	l.Wait = false
	run(l, 6)
	if l.Pos > 4 || l.Finished {
		t.Fatalf("loop should wrap, pos=%v", l.Pos)
	}
}

func TestListen(t *testing.T) {
	snd := &fakeSound{on: map[int]bool{}}
	l := NewLesson(testSong(), snd)
	l.SetListen(true)
	l.Restart()
	l.Play = true
	run(l, 6)
	if snd.count != 6 || !l.Finished {
		t.Fatalf("listen should play all 6 notes, got %d finished=%v", snd.count, l.Finished)
	}
}
