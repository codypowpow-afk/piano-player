package score

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strings"
)

type mxScore struct {
	Title    string    `xml:"work>work-title"`
	MovTitle string    `xml:"movement-title"`
	Parts    []mxPart  `xml:"part"`
	PartList []mxSPart `xml:"part-list>score-part"`
}
type mxSPart struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"part-name"`
}
type mxPart struct {
	ID       string      `xml:"id,attr"`
	Measures []mxMeasure `xml:"measure"`
}
type mxMeasure struct {
	Items []mxItem `xml:",any"`
}
type mxItem struct {
	XMLName  xml.Name
	Chord    *struct{} `xml:"chord"`
	Rest     *struct{} `xml:"rest"`
	Grace    *struct{} `xml:"grace"`
	Cue      *struct{} `xml:"cue"`
	Step     string    `xml:"pitch>step"`
	Alter    float64   `xml:"pitch>alter"`
	Octave   int       `xml:"pitch>octave"`
	Duration int       `xml:"duration"`
	Staff    int       `xml:"staff"`
	Voice    string    `xml:"voice"`
	Ties     []struct {
		Type string `xml:"type,attr"`
	} `xml:"tie"`
	Divisions int  `xml:"divisions"`
	Beats     int  `xml:"time>beats"`
	BeatType  int  `xml:"time>beat-type"`
	Fifths    *int `xml:"key>fifths"`
	Staves    int  `xml:"staves"`
	Sound     *struct {
		Tempo float64 `xml:"tempo,attr"`
	} `xml:"sound"`
	Tempo float64 `xml:"tempo,attr"`
}

// ReadMusicXML parses .musicxml / .xml or compressed .mxl data.
func ReadMusicXML(data []byte, title string) (*Song, error) {
	if len(data) > 2 && data[0] == 'P' && data[1] == 'K' {
		var err error
		data, err = unzipMXL(data)
		if err != nil {
			return nil, err
		}
	}
	var sc mxScore
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(label string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&sc); err != nil {
		return nil, err
	}
	if len(sc.Parts) == 0 {
		return nil, errors.New("no parts in MusicXML (only score-partwise is supported)")
	}
	if t := strings.TrimSpace(sc.Title); t != "" {
		title = t
	} else if t := strings.TrimSpace(sc.MovTitle); t != "" {
		title = t
	}
	s := &Song{Title: title, Tempo: 90, BeatsPer: 4, Source: "MusicXML"}
	steps := map[string]int{"C": 0, "D": 2, "E": 4, "F": 5, "G": 7, "A": 9, "B": 11}
	for pi, part := range sc.Parts {
		if pi > 1 {
			break // keyboard music: first part right hand, second left
		}
		div := 1
		t := 0.0 // beats
		mstart := 0.0
		lastStart := 0.0
		tied := map[int]int{} // pitch -> note index awaiting a tie stop
		for mi, m := range part.Measures {
			if pi == 0 {
				s.Measures = append(s.Measures, t)
			}
			mstart = t
			mlen := 0.0
			for _, it := range m.Items {
				switch it.XMLName.Local {
				case "attributes":
					if it.Divisions > 0 {
						div = it.Divisions
					}
					if it.Beats > 0 && it.BeatType > 0 && mi == 0 && pi == 0 {
						s.BeatsPer = float64(it.Beats) * 4 / float64(it.BeatType)
					}
					if it.Fifths != nil && mi == 0 && pi == 0 {
						s.KeyFifths = *it.Fifths
					}
				case "sound":
					if it.Tempo > 0 && s.Tempo == 90 {
						s.Tempo = it.Tempo
					}
				case "direction":
					if it.Sound != nil && it.Sound.Tempo > 0 && s.Tempo == 90 {
						s.Tempo = it.Sound.Tempo
					}
				case "backup":
					t -= float64(it.Duration) / float64(div)
				case "forward":
					t += float64(it.Duration) / float64(div)
				case "note":
					d := float64(it.Duration) / float64(div)
					start := t
					if it.Chord != nil {
						start = lastStart
					} else {
						lastStart = t
						t += d
					}
					if it.Grace != nil {
						d = 0.125
					}
					mlen = maxf(mlen, t-mstart)
					if it.Rest != nil || it.Step == "" || it.Cue != nil {
						continue
					}
					p := 12*(it.Octave+1) + steps[strings.ToUpper(it.Step)] + int(it.Alter)
					hand := Right
					if pi == 1 || it.Staff >= 2 {
						hand = Left
					}
					stop, startTie := false, false
					for _, ti := range it.Ties {
						if ti.Type == "stop" {
							stop = true
						}
						if ti.Type == "start" {
							startTie = true
						}
					}
					if stop {
						if idx, ok := tied[p]; ok {
							s.Notes[idx].Dur = start + d - s.Notes[idx].Start
							if !startTie {
								delete(tied, p)
							}
							continue
						}
					}
					s.Notes = append(s.Notes, Note{Pitch: p, Start: start, Dur: d, Hand: hand, Vel: 0.7})
					if startTie {
						tied[p] = len(s.Notes) - 1
					}
				}
			}
			t = mstart + mlen
		}
	}
	if len(s.Notes) == 0 {
		return nil, errors.New("no notes found in MusicXML")
	}
	s.Sort()
	for i := range s.Notes {
		s.Notes[i].Measure = s.MeasureAt(s.Notes[i].Start)
	}
	return s, nil
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func unzipMXL(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	read := func(name string) ([]byte, error) {
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 64<<20))
			}
		}
		return nil, errors.New("missing " + name)
	}
	if c, err := read("META-INF/container.xml"); err == nil {
		var cont struct {
			Files []struct {
				Path string `xml:"full-path,attr"`
			} `xml:"rootfiles>rootfile"`
		}
		if xml.Unmarshal(c, &cont) == nil && len(cont.Files) > 0 {
			return read(cont.Files[0].Path)
		}
	}
	for _, f := range zr.File {
		ext := strings.ToLower(path.Ext(f.Name))
		if (ext == ".xml" || ext == ".musicxml") && !strings.HasPrefix(f.Name, "META-INF") {
			return read(f.Name)
		}
	}
	return nil, errors.New("no score inside .mxl")
}
