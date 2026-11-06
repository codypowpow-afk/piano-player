package score

import "testing"

func TestMIDIRoundTrip(t *testing.T) {
	s := &Song{Tempo: 100, BeatsPer: 4}
	s.Notes = []Note{{Pitch: 60, Start: 0, Dur: 1, Hand: Right, Vel: .7}, {Pitch: 48, Start: 0, Dur: 2, Hand: Left, Vel: .7}, {Pitch: 64, Start: 1, Dur: 1, Hand: Right, Vel: .7}}
	b := WriteMIDI(s)
	r, err := ReadMIDI(b, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Notes) != 3 || r.Notes[0].Pitch != 48 || r.Notes[0].Hand != Left || r.Notes[2].Start != 1 || int(r.Tempo+0.5) != 100 {
		t.Fatalf("bad round trip: %+v tempo %v", r.Notes, r.Tempo)
	}
}

const xmlDoc = `<?xml version="1.0"?>
<score-partwise><work><work-title>Test</work-title></work>
<part-list><score-part id="P1"><part-name>Piano</part-name></score-part></part-list>
<part id="P1">
<measure number="1"><attributes><divisions>2</divisions><key><fifths>-3</fifths></key><time><beats>3</beats><beat-type>4</beat-type></time><staves>2</staves></attributes>
<note><pitch><step>E</step><alter>-1</alter><octave>5</octave></pitch><duration>2</duration><staff>1</staff><tie type="start"/></note>
<note><chord/><pitch><step>G</step><octave>4</octave></pitch><duration>2</duration><staff>1</staff></note>
<note><pitch><step>E</step><alter>-1</alter><octave>5</octave></pitch><duration>4</duration><staff>1</staff><tie type="stop"/></note>
<backup><duration>6</duration></backup>
<note><pitch><step>E</step><alter>-1</alter><octave>2</octave></pitch><duration>6</duration><staff>2</staff></note>
</measure>
<measure number="2"><note><rest/><duration>6</duration><staff>1</staff></note></measure>
</part></score-partwise>`

func TestMusicXML(t *testing.T) {
	s, err := ReadMusicXML([]byte(xmlDoc), "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Test" || len(s.Notes) != 3 || s.BeatsPer != 3 || len(s.Measures) != 2 || s.Measures[1] != 3 {
		t.Fatalf("%+v", s)
	}
	// tied Eb5 lasts 3 beats; Eb2 in the left hand
	for _, n := range s.Notes {
		if n.Pitch == 75 && n.Dur != 3 {
			t.Fatalf("tie not merged: %+v", n)
		}
		if n.Pitch == 39 && n.Hand != Left {
			t.Fatalf("hand wrong %+v", n)
		}
	}
}
