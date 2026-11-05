# Piano Player

A standalone acoustic piano for Windows that you play with a USB MIDI keyboard, your computer keyboard, or the mouse.
One `.exe` with the instrument recordings built in, no installer, works offline.

**Download:** [`download/PianoPlayer.exe`](../../raw/main/download/PianoPlayer.exe), then double-click it.
(Windows SmartScreen may warn because the exe isn't code-signed: click **More info → Run anyway**.)

## Sounds (24 presets)

Real recorded instruments, sampled at several velocity layers so soft playing sounds soft
and dark, and hard playing sounds bright, like the real thing.

| Category | Presets |
|----------|---------|
| **Grand** | Concert Grand · Studio Grand · Bright Pop Grand · Warm Jazz Grand · Concert Hall · Cathedral Grand · Ballad Grand · Steinway B · Steinway Intimate |
| **Upright** | Yamaha Upright · Knight Upright · Honky-Tonk · Felt Upright |
| **Character** | Felt Piano · Dream Piano · Muted Piano · Lo-Fi Piano · Old Record |
| **Keys** | Harpsichord · FM Electric Piano · Vibraphone · Marimba · Pipe Organ · Glockenspiel |

Click a sound, or use `Tab` / `Shift+Tab` (or `PgDn` / `PgUp`) to step through them, `F1`–`F4`
to jump to a category, or send a **Program Change** from your MIDI keyboard (program 1 = Concert
Grand, 2 = Studio Grand, …). Each instrument loads in a second or two the first time you pick it.

## Playing

```
 Upper row (one octave up):   2 3   5 6 7   9 0   =
                             Q W E R T Y U I O P [ ]
 Lower row:                   S D   G H J   L ;
                             Z X C V B N M , . /
```

Black keys sit on the row above their white keys, like most DAWs. The keys are labelled on the
on-screen keyboard, and the gold bar above it shows which part of the 88 keys you're playing.

| Control | Action |
|---------|--------|
| `←` / `→` (or PgDn / PgUp) | Octave down / up |
| `↑` / `↓` | Touch (how hard you play: ppp … fff). Harder = louder **and** brighter |
| hold `Shift` / `Ctrl` | Accent (harder) / soft for the notes you press |
| hold `Space` | Sustain pedal |
| `Enter` or `Caps Lock` | Latch the sustain pedal on/off |
| `Tab` / `PgUp` `PgDn` / `F1`–`F4` | Next / previous sound, jump to category |
| `F5` / `F6` | MIDI velocity curve / rescan MIDI devices |
| `Esc` | All notes off |
| Mouse | Click keys to play (lower on the key = louder), drag for glissando |

Sliders set volume, room (reverb) amount and touch.

## MIDI keyboard

Plug in any USB MIDI keyboard or digital piano, before or after starting the app. It's found
automatically (no drivers or settings needed for class-compliant keyboards) and its name shows in
the MIDI bar, with a light that flashes as you play. Several keyboards can be connected at once.

- **Velocity sensitive**: how hard you play sets both loudness and tone, like a real piano.
- **Sustain pedal** (CC 64) and **soft pedal / una corda** (CC 67) work.
- **F5** (or click the velocity label) switches the velocity curve: **Light** (loud comes
  easily), **Normal**, or **Heavy** (you have to dig in for forte).
- **F6** (or click the MIDI bar) rescans devices.
- If the bar says a keyboard is *in use by another program*, close your DAW or other MIDI app:
  Windows only lets one program use a MIDI device at a time.

## How the sound works

- **Grand pianos**: a Yamaha C5 concert grand (Salamander Grand Piano, 4 velocity layers,
  sampled every minor third) and a Steinway Model B (2 layers).
- **Uprights**: a Yamaha upright (3 layers) and a vintage Knight upright (2 layers).
- Velocity **crossfades smoothly** between the recorded layers, and notes between sampled keys
  are pitch-shifted from the nearest recording with high-quality interpolation.
- **Dampers** stop notes when you lift the key (the top octave has no dampers, as on a real
  piano), and the **sustain pedal** lets everything ring. Re-striking a key replaces the old note
  smoothly.
- Each preset has its own tone and **room**: EQ, felt filter, compression, tape wobble, vinyl
  crackle, tremolo, stereo width and reverb.
- A peak limiter keeps big pedalled chords clean.

## Credits

The instrument recordings are by other people, used under their free licenses:

- **Salamander Grand Piano V3** by Alexander Holm, licensed
  [CC BY 3.0](https://creativecommons.org/licenses/by/3.0/)
  ([sfzinstruments/SalamanderGrandPiano](https://github.com/sfzinstruments/SalamanderGrandPiano)).
  Changes: 4 of the 16 velocity layers, trimmed, level-matched and re-encoded as Ogg Vorbis.
- **Versilian Community Sample Library (VCSL)** by Versilian Studios and contributors,
  [CC0](https://creativecommons.org/publicdomain/zero/1.0/)
  ([sgossner/VCSL](https://github.com/sgossner/VCSL)): Steinway B, Yamaha and Knight uprights,
  Flemish harpsichord, TX81Z FM piano, vibraphone, marimba, pipe organ, glockenspiel.

## Build it yourself

Needs Go 1.22+. The exe is cross-compiled; no Windows machine needed.

```
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui" -o dist/PianoPlayer.exe .
```

The samples in `assets/` are built from the original libraries by
`python3 tools/samples/build.py --salamander <SalamanderGrandPiano>/Samples --vcsl <VCSL>`
(needs numpy and ffmpeg). It checks each sample's pitch, trims it, matches levels, makes the
organ loops and encodes Ogg Vorbis.

`rsrc_windows_amd64.syso` (icon + version info) is generated by `go run ./tools/winres`.
`go run ./tools/demo out/` renders a demo passage on every preset to WAV and prints CPU use.

Built with [Ebitengine](https://ebitengine.org/).
