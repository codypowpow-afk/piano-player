# Piano Lessons — Project Notes & Handoff

A second app in the Piano Player repo. It turns sheet music into falling notes over the 88-key
piano and waits for you to play each note. Written at the end of the build session on 2026-11-05.

- **Build:** `GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui" -o dist/PianoLessons.exe ./lessons`
- **Size:** ~48 MB exe (same samples as Piano Player plus a ~0.5 MB sample song file)
- **New code:** ~6,300 lines of Go (incl. tests and tools): OMR reader, song model and importers, lesson engine, UI
- **Unchanged:** `synth/`, `assets/`, and the Piano Player app itself

---

## 1. What it does

1. **Import**: scanned PDF, PNG/JPG page images, MIDI, MusicXML (`.musicxml`, `.xml`, `.mxl`), or
   the app's own JSON. Drag and drop, **Open file…** (Windows dialog), or a command-line argument
   (so "Open with…" and dropping a file onto the exe both work).
2. **Read the music** (PDF/images only): built-in optical music recognition (OMR), see §4.
   A PDF holding several pieces is split into pieces (Chopin Op. 9 → No. 1, 2, 3).
3. **Learn**: notes fall onto the keys. Right hand teal, left hand amber; the app's own notes
   are dimmed. The current step is outlined and its keys are lit on the keyboard.
   - **Wait mode**: the song stops at each step until every key of that step has been pressed
     (a fresh press is needed, so repeated notes count).
   - **Hands**: each hand is **You** / **Auto** / **Off** (F1 right, F5 left). Off hands are
     silent and drawn as outlines, so melody and chords can be learned separately.
   - **Small keyboards**: MIDI octave shift −4…+4 (Shift+←/→; a key released after a shift
     still stops the note it started) and **Any octave** (F6), which matches by note name.
   - **Listen**: the app plays everything.
   - **Loop** 1/2/4/8 bars, **tempo** 25–150 %, **Skip** (Tab) a note the reader got wrong.
   - Scoring: right notes, wrong notes (only counted near a step), skipped, accuracy.
4. **Library**: every import is saved as JSON in `%AppData%\PianoLessons\library\`.
   **Export MIDI** writes `Documents\Piano Lessons\<title>.mid` (right hand on track 1,
   left on track 2), so a misread score can be fixed in MuseScore and re-imported.

---

## 2. File map

```
lessons/main.go             UI (Ebiten): layout, buttons, waterfall, keyboard, overlays, input
lessons/library.go          Import dispatch by file type, library folder, embedded sample
lessons/dialog_windows.go   Native Open dialog (comdlg32 GetOpenFileNameW, no cgo)
lessons/dialog_other.go     No dialog elsewhere (drag and drop still works)
lessons/sample/chopin_op9.json   The three Op. 9 nocturnes as read from the scan
lessons/rsrc_windows_amd64.syso  Icon + version info ("Piano Lessons")
internal/lesson/            Lesson state machine (wait mode, steps, auto-play, loop, scoring) + tests
internal/midiin/            The winmm MIDI input, shared copy of midi_windows.go / midi_other.go
score/score.go              Song / Note / Step model, JSON, note names
score/midi.go               MIDI reader (tracks/channels -> hands, tempo, time signature) + writer
score/musicxml.go           MusicXML reader (partwise, staves, chords, backup/forward, ties, .mxl)
omr/pdf.go                  Tiny PDF reader: object streams, page tree, CCITT G3/G4, JPEG, Flate
omr/imgops.go               Downscale, run stats, integral image, labelling, hole filling
omr/staff.go                Staff-line detection (strip-wise, warp tolerant), line removal
omr/recognize.go            Clefs, key signatures, noteheads, stems, barlines, accidentals
omr/ottava.go               "8 . . . . loco" detection
omr/build.go                Pages -> systems -> pieces -> measures -> timed notes
tools/sheet2song/           CLI: PDF -> songs JSON, prints the first bars of each piece
tools/omrdebug/             CLI: draws everything the reader found on one page into a PNG
tools/winres/               now takes -name/-desc/-exe/-o (defaults unchanged)
```

---

## 3. Song model (`score`)

```go
type Note struct { Pitch int; Start, Dur float64; Hand, Measure int; Vel float64; Page int; X, Y float32 }
type Song struct { Title string; Tempo, BeatsPer float64; KeyFifths int; Measures []float64; Notes []Note }
song.Steps(handMask, eps)   // notes grouped by onset = what the learner must press together
```
Times are in beats (quarter notes). For OMR songs every bar is 4 "beats" (the time signature
is not read), so the tempo is "bars feel"; MIDI/MusicXML keep their real meter and tempo.

---

## 4. The sheet-music reader (`omr`)

Classical, rule-based OMR. Each page is scaled so a staff space is 20 px, then:

1. **Staves**: the page is cut into vertical strips; in each strip, rows with a long horizontal
   ink run are line candidates. Groups of five evenly spaced lines are scored by line strength,
   linked across strips into chains, broken chains are merged, and the lines are tracked
   outward to the real staff ends. Lines are stored per strip, so warped scans are fine.
   Staves are paired into piano systems.
2. **Staff-line removal**: line pixels are erased where the vertical run is no thicker than a line.
3. **Noteheads**: two passes on the cleaned page: hollow heads (holes filled first) and filled
   heads. Each uses directional openings (keep horizontal runs ≥ 0.32 sp, then vertical runs
   ≥ 0.38 sp) that remove stems, ledger lines and thin curves. Blobs are split into chord heads
   using the row-width profile. Small (cue/cadenza) heads are accepted too.
4. **Stems**: a thin vertical line touching the head, traced with gap and 1-px wobble tolerance.
   A short stem is accepted when it ends in a beam. A "head" whose stem leaves on the wrong
   side is a flag unless a chord partner explains it. Blobs that run on sideways are beams.
   Stem direction decides the voice, the onset column, and which staff a note in the gap
   between the staves belongs to.
5. **Barlines**: columns covering the whole system top to bottom (strong), or both staves with
   no notehead next to them.
6. **Clef**: glyph pieces at the staff start (tall = treble, upper half = bass; otherwise
   treble on top, bass below). Mid-staff clef changes are looked for too.
7. **Key signature**: vertical strokes after the clef. One stroke per flat (longest run per
   column), close pairs per sharp (all runs, linked across columns). Each accidental must sit
   at the next position of the standard order. Per system, the best staff reading wins, and
   readings are smoothed across the piece (they fail by stopping early, never by inventing).
8. **Accidentals**: component classification plus a local stroke test just left of each head
   (natural = two offset strokes, sharp = two equal strokes, flat = one stroke + bowl). They
   apply for the rest of the bar at that staff position.
9. **8va**: an "8" followed by ≥ 6 evenly spaced dots above a staff.
10. **Timing**: within a bar, onset columns are clustered across both staves, and time is
    proportional to x position. Pickup bars are shortened, and very dense bars (cadenzas) are
    lengthened. A note lasts until the next onset in the same staff and stem voice.
11. **Pieces**: an indented first system starts a new piece.

### How good is it (Chopin Op. 9 scan)
- Staves 144/144, systems 72/72, pieces 3/3, and each piece's key right (5♭, 3♭, 5♯).
  Per-system key readings are smoothed; No. 3's 2-sharp middle section is only partly caught.
- Bars: No. 2 reads as 35 bars, which matches the score (pickup + 34). No. 1 reads as 81 and
  No. 3 as 150; a few barlines are missed there, which merges two bars into one.
- Melody and chord noteheads: the large majority. No. 2's opening bars read exactly
  (B♭4 | G5 G5 F5 G5 F5 E♭5 B♭4 | … | F5 G5 D5 E♭5 C5).
- Weak spots: accidentals that touch other symbols (many are still missed), noteheads fused
  with beams, ties (a tied note is asked for twice), triplets/irregular rhythms (timing is by
  spacing), mid-line clef changes, grace-note timing.
- Vector PDFs (exported from notation software) have no page images: the app says so and
  suggests MIDI/MusicXML. JBIG2 and JPEG 2000 images are not decoded yet (export pages as PNG).

### Tuning workflow
`go run ./tools/omrdebug -o page.png score.pdf 5` draws every detection on page 5.
`go run ./tools/sheet2song -show 8 score.pdf` prints the first bars of each piece as note names.

---

## 5. Lesson engine (`internal/lesson`)

- `Advance(dt)` moves the position at `Tempo`. In wait mode it clamps at the next learner step.
- `Press(p)` records a key press with a sequence number. A step is done when every pitch was
  pressed after the previous step finished. Presses up to 0.6 beat early count.
- Auto-play plays every note that isn't the learner's (all of them in Listen mode) on time,
  releasing each one at its end.
- Fully unit tested without a window: `go test ./internal/lesson`.

---

## 6. Testing done in this session

- Unit tests: lesson engine (wait, chords, repeated notes, skip, loop, listen), MIDI round trip,
  MusicXML (chords, staves, backup, ties), OMR on a page image, key table.
- The UI was compiled to **WebAssembly** and run in headless Chrome. Screenshots confirmed
  layout, the waiting prompt, a correct key press advancing the song, the library overlay,
  hands and listen buttons, and playback. The real Windows window and a real MIDI keyboard
  were **not** tested (same as Piano Player); the MIDI code is the proven Piano Player backend.

## 7. Ideas / next steps
- Show the scanned page above the waterfall with the current notes boxed (notes already
  carry page/x/y).
- Tie detection; time-signature digits; triplet brackets.
- A small note editor (fix a misread pitch in place) instead of Export → edit → re-import.
- Optional Audiveris integration for users who have it installed.
- Remember settings (tempo, hands, last song) between runs.

## 8. Credits
Same as Piano Player (Salamander Grand Piano CC BY 3.0, VCSL CC0, Ebitengine, oggvorbis, winres,
Go fonts). The Chopin sample is read from a public-domain engraving.
