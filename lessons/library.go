package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/codypowpow-afk/piano-player/omr"
	"github.com/codypowpow-afk/piano-player/score"
)

//go:embed sample/chopin_op9.json
var sampleJSON []byte

// LibEntry is one document in the song library (it may hold several pieces).
type LibEntry struct {
	Title   string
	Path    string // saved JSON (empty for built-in)
	Builtin bool
	Songs   []*score.Song
}

func libDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "PianoLessons", "library")
}

func exportDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(h, "Documents", "Piano Lessons")
}

// loadLibrary returns the built-in sample plus previously imported songs.
func loadLibrary() []*LibEntry {
	var out []*LibEntry
	if songs, err := score.LoadJSON(sampleJSON); err == nil {
		out = append(out, &LibEntry{Title: "Chopin — Nocturnes Op. 9 (sample)", Builtin: true, Songs: songs})
	}
	files, _ := filepath.Glob(filepath.Join(libDir(), "*.json"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		songs, err := score.LoadJSON(b)
		if err != nil || len(songs) == 0 {
			continue
		}
		t := strings.TrimSuffix(filepath.Base(f), ".json")
		out = append(out, &LibEntry{Title: t, Path: f, Songs: songs})
	}
	return out
}

func saveToLibrary(title string, songs []*score.Song) (string, error) {
	dir := libDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := sanitize(title)
	p := filepath.Join(dir, name+".json")
	var buf bytes.Buffer
	buf.WriteString("[")
	for i, s := range songs {
		if i > 0 {
			buf.WriteString(",")
		}
		b, err := json.Marshal(s)
		if err != nil {
			return "", err
		}
		buf.Write(b)
	}
	buf.WriteString("]")
	return p, os.WriteFile(p, buf.Bytes(), 0o644)
}

func sanitize(s string) string {
	r := strings.NewReplacer(`\`, "_", "/", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_")
	s = strings.TrimSpace(r.Replace(s))
	if s == "" {
		s = "song"
	}
	return s
}

// importProgress is shared between the loader goroutine and the UI.
type importProgress struct {
	mu     sync.Mutex
	active bool
	msg    string
	done   int
	total  int
}

func (p *importProgress) set(active bool, msg string, done, total int) {
	p.mu.Lock()
	p.active, p.msg, p.done, p.total = active, msg, done, total
	p.mu.Unlock()
}

func (p *importProgress) get() (bool, string, float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := 0.0
	if p.total > 0 {
		f = float64(p.done) / float64(p.total)
	}
	return p.active, p.msg, f
}

type inFile struct {
	name string
	data []byte
}

type loadResult struct {
	entry *LibEntry
	err   error
}

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true}

// importFiles turns dropped/opened files into songs. Several image files are
// treated as the pages of one score, in file-name order.
func importFiles(files []inFile, prog *importProgress) (*LibEntry, error) {
	if len(files) == 0 {
		return nil, errors.New("no file")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	first := files[0]
	ext := strings.ToLower(filepath.Ext(first.name))
	title := strings.TrimSuffix(filepath.Base(first.name), filepath.Ext(first.name))
	var songs []*score.Song
	var err error
	cb := func(d, n int, msg string) { prog.set(true, msg, d, n) }
	switch {
	case ext == ".pdf":
		prog.set(true, "Reading PDF pages…", 0, 1)
		var res *omr.Result
		res, err = omr.ReadPDF(first.data, title, cb)
		if res != nil {
			songs = res.Songs
		}
	case imageExt[ext]:
		var pages []*omr.Bitmap
		for i, f := range files {
			if !imageExt[strings.ToLower(filepath.Ext(f.name))] {
				continue
			}
			prog.set(true, fmt.Sprintf("Decoding image %d of %d", i+1, len(files)), i, len(files))
			img, _, e := image.Decode(bytes.NewReader(f.data))
			if e != nil {
				return nil, fmt.Errorf("%s: %v", f.name, e)
			}
			pages = append(pages, omr.Binarize(img))
		}
		res := omr.ReadPages(pages, title, cb)
		songs = res.Songs
	case ext == ".mid" || ext == ".midi":
		var s *score.Song
		s, err = score.ReadMIDI(first.data, title)
		songs = []*score.Song{s}
	case ext == ".musicxml" || ext == ".xml" || ext == ".mxl":
		var s *score.Song
		s, err = score.ReadMusicXML(first.data, title)
		songs = []*score.Song{s}
	case ext == ".json":
		songs, err = score.LoadJSON(first.data)
	default:
		return nil, fmt.Errorf("can't open %s files — use PDF, PNG/JPG, MIDI or MusicXML", ext)
	}
	if err != nil {
		return nil, err
	}
	var keep []*score.Song
	for _, s := range songs {
		if s != nil && len(s.Notes) > 0 {
			if s.Title == "" {
				s.Title = title
			}
			keep = append(keep, s)
		}
	}
	if len(keep) == 0 {
		return nil, errors.New("no notes could be read from " + filepath.Base(first.name))
	}
	e := &LibEntry{Title: title, Songs: keep}
	if ext != ".json" {
		if p, err := saveToLibrary(title, keep); err == nil {
			e.Path = p
		}
	}
	return e, nil
}

func readPaths(paths []string) ([]inFile, error) {
	var out []inFile
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, inFile{p, b})
	}
	return out, nil
}

func readDropped(fsys fs.FS) []inFile {
	var out []inFile
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(fsys, e.Name())
		if err == nil {
			out = append(out, inFile{e.Name(), b})
		}
	}
	return out
}
