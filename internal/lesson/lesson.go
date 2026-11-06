// Package lesson is the play-along state machine behind Piano Lessons.
package lesson

import (
	"math"
	"sort"

	"github.com/codypowpow-afk/piano-player/score"
)

// Hands the learner plays.
const (
	HandsBoth = iota
	HandsRight
	HandsLeft
)

var HandsNames = []string{"Both hands", "Right hand", "Left hand"}

// Sounder is what a lesson needs from the audio engine.
type Sounder interface {
	NoteOn(midi int, vel float64)
	NoteOff(midi int)
}

// Lesson is the play-along state machine: song position, which notes the
// learner must play next, and auto-playing everything else.
type Lesson struct {
	Song   *score.Song
	Hands  int
	Wait   bool // stop at each step until the right keys are pressed
	Listen bool // demo: the app plays everything
	Tempo  float64
	Pos    float64 // beats
	Play   bool

	Steps   []score.Step // the learner's steps
	K       int          // next step to play
	Waiting bool

	LoopOn         bool
	LoopA, LoopB   float64
	LoopBars       int

	hit     [128]int64 // sequence number of the last press of each key
	seq     int64
	armSeq  int64
	StepHit []bool       // per learner step: completed correctly

	autoIdx  int
	sounding map[int]float64 // auto-played pitch -> end beat
	sound    Sounder

	Correct, Wrong, Skipped int
	WrongFlash               [128]float64 // time left to show a wrong key
	GoodFlash                [128]float64
	Finished                 bool
}

func NewLesson(s *score.Song, snd Sounder) *Lesson {
	l := &Lesson{Song: s, Wait: true, Tempo: s.Tempo, sound: snd, sounding: map[int]float64{}, LoopBars: 4}
	if l.Tempo <= 0 {
		l.Tempo = 72
	}
	l.Rebuild()
	return l
}

func (l *Lesson) handMask() int {
	switch l.Hands {
	case HandsRight:
		return 1 << score.Right
	case HandsLeft:
		return 1 << score.Left
	}
	return 3
}

// Learner reports whether note n is the learner's to play.
func (l *Lesson) Learner(n *score.Note) bool {
	if l.Listen {
		return false
	}
	return l.handMask()&(1<<n.Hand) != 0
}

// Rebuild recomputes the learner steps (after changing hands).
func (l *Lesson) Rebuild() {
	l.Steps = l.Song.Steps(l.handMask(), 0.02)
	l.StepHit = make([]bool, len(l.Steps))
	l.Seek(l.Pos)
}

func (l *Lesson) SetHands(h int) {
	l.Hands = h
	l.Rebuild()
}

func (l *Lesson) SetListen(on bool) {
	l.Listen = on
	l.Seek(l.Pos)
}

// Seek moves to beat t and resets which step is next.
func (l *Lesson) Seek(t float64) {
	l.Silence()
	end := l.Song.End()
	l.Pos = math.Max(-1, math.Min(t, end))
	l.K = sort.Search(len(l.Steps), func(i int) bool { return l.Steps[i].Start >= l.Pos-1e-6 })
	l.autoIdx = sort.Search(len(l.Song.Notes), func(i int) bool { return l.Song.Notes[i].Start >= l.Pos-1e-6 })
	l.Waiting = false
	l.armSeq = l.seq
	l.Finished = false
}

func (l *Lesson) Restart() {
	l.Correct, l.Wrong, l.Skipped = 0, 0, 0
	for i := range l.StepHit {
		l.StepHit[i] = false
	}
	start := 0.0
	if len(l.Song.Notes) > 0 {
		start = l.Song.Notes[0].Start
	}
	if l.LoopOn {
		start = l.LoopA
	}
	l.Seek(start - 1)
}

// Silence stops all auto-played notes.
func (l *Lesson) Silence() {
	for p := range l.sounding {
		l.sound.NoteOff(p)
	}
	l.sounding = map[int]float64{}
}

// SetLoop loops LoopBars measures starting at the measure containing Pos.
func (l *Lesson) SetLoop(on bool) {
	l.LoopOn = on
	if !on {
		return
	}
	ms := l.Song.Measures
	if len(ms) == 0 {
		l.LoopOn = false
		return
	}
	m := l.Song.MeasureAt(math.Max(l.Pos, 0))
	l.LoopA = ms[m]
	if m+l.LoopBars < len(ms) {
		l.LoopB = ms[m+l.LoopBars]
	} else {
		l.LoopB = l.Song.End()
	}
}

// Current returns the step the learner should play now (or -1).
func (l *Lesson) Current() int {
	if l.Listen || l.K >= len(l.Steps) {
		return -1
	}
	return l.K
}

// Press records a key the learner played.
func (l *Lesson) Press(p int) {
	if p < 0 || p > 127 {
		return
	}
	l.seq++
	l.hit[p] = l.seq
	k := l.Current()
	if k < 0 {
		return
	}
	st := l.Steps[k]
	// is it part of the current (or the next) step?
	ok := false
	for _, i := range st.Notes {
		if l.Song.Notes[i].Pitch == p {
			ok = true
		}
	}
	if !ok && k+1 < len(l.Steps) && l.Steps[k+1].Start-st.Start < 0.5 {
		for _, i := range l.Steps[k+1].Notes {
			if l.Song.Notes[i].Pitch == p {
				ok = true
			}
		}
	}
	near := st.Start-l.Pos < 1.0 // only judge presses close to the step
	if ok {
		l.GoodFlash[p] = 0.25
	} else if near && l.Play {
		l.Wrong++
		l.WrongFlash[p] = 0.4
	}
	l.tryComplete()
}

// stepSatisfied: every note of step k was pressed since it was armed.
func (l *Lesson) stepSatisfied(k int) (bool, int64) {
	var last int64
	for _, i := range l.Steps[k].Notes {
		h := l.hit[l.Song.Notes[i].Pitch]
		if h <= l.armSeq {
			return false, 0
		}
		if h > last {
			last = h
		}
	}
	return true, last
}

func (l *Lesson) tryComplete() {
	for {
		k := l.Current()
		if k < 0 {
			return
		}
		st := l.Steps[k]
		if st.Start-l.Pos > 0.6 { // too early to count
			return
		}
		ok, last := l.stepSatisfied(k)
		if !ok {
			return
		}
		l.StepHit[k] = true
		l.Correct++
		l.armSeq = last
		l.K++
		l.Waiting = false
	}
}

// Skip passes over the current step (e.g. a misread note).
func (l *Lesson) Skip() {
	if k := l.Current(); k >= 0 {
		l.Skipped++
		l.K++
		l.Waiting = false
		l.armSeq = l.seq
		if l.Pos < l.Steps[k].Start {
			l.Pos = l.Steps[k].Start
		}
	}
}

// Advance moves the song forward by dt seconds of real time.
func (l *Lesson) Advance(dt float64) {
	for p := range l.WrongFlash {
		if l.WrongFlash[p] > 0 {
			l.WrongFlash[p] -= dt
		}
		if l.GoodFlash[p] > 0 {
			l.GoodFlash[p] -= dt
		}
	}
	if !l.Play {
		return
	}
	next := l.Pos + dt*l.Tempo/60
	// wait for the learner
	if l.Wait {
		if k := l.Current(); k >= 0 && next >= l.Steps[k].Start {
			next = l.Steps[k].Start
			l.Waiting = true
		}
	} else {
		// play-along: steps that scroll past unplayed count as missed
		for {
			k := l.Current()
			if k < 0 || next < l.Steps[k].Start+0.35 {
				break
			}
			l.K++
			l.armSeq = l.seq
		}
	}
	if l.LoopOn && next >= l.LoopB {
		l.Seek(l.LoopA)
		l.Play = true
		return
	}
	l.Pos = next
	l.tryComplete()
	l.autoPlay()
	if end := l.Song.End(); l.Pos >= end && l.Current() < 0 {
		l.Silence()
		l.Play = false
		l.Finished = true
	}
}

// autoPlay sounds the notes that are not the learner's.
func (l *Lesson) autoPlay() {
	notes := l.Song.Notes
	for l.autoIdx < len(notes) && notes[l.autoIdx].Start <= l.Pos {
		n := &notes[l.autoIdx]
		l.autoIdx++
		if l.Learner(n) {
			continue
		}
		if _, on := l.sounding[n.Pitch]; on {
			l.sound.NoteOff(n.Pitch)
		}
		v := n.Vel
		if v <= 0 {
			v = 0.6
		}
		l.sound.NoteOn(n.Pitch, v*0.85)
		l.sounding[n.Pitch] = n.Start + math.Max(n.Dur*0.95, 0.1)
	}
	for p, end := range l.sounding {
		if l.Pos >= end {
			l.sound.NoteOff(p)
			delete(l.sounding, p)
		}
	}
}

// Accuracy is correct steps / judged steps.
func (l *Lesson) Accuracy() float64 {
	tot := l.Correct + l.Wrong
	if tot == 0 {
		return 1
	}
	return float64(l.Correct) / float64(tot)
}

// SoundingHand reports whether the app is auto-playing pitch p, and for which hand.
func (l *Lesson) SoundingHand(p int) (int, bool) {
	if _, on := l.sounding[p]; !on {
		return 0, false
	}
	for i := l.autoIdx - 1; i >= 0 && i > l.autoIdx-96; i-- {
		if n := l.Song.Notes[i]; n.Pitch == p {
			return n.Hand, true
		}
	}
	return 0, true
}
