#!/usr/bin/env python3
"""Builds the embedded sample sets in assets/ from the source libraries.

Sources (download separately, see README "Credits"):
  Salamander Grand Piano v3 (Yamaha C5), Alexander Holm, CC-BY 3.0
      https://github.com/sfzinstruments/SalamanderGrandPiano     -> --salamander DIR (its Samples/ folder)
  Versilian Community Sample Library (VCSL), CC0
      https://github.com/sgossner/VCSL                          -> --vcsl DIR (repo root)

For every sample this script: decodes to 48 kHz float, verifies the pitch against the file name
(fixing octave-numbering differences between libraries), trims leading silence and inaudible tails,
caps the length per note, levels each note so the keyboard is even while keeping the natural step
between velocity layers, bakes seamless crossfade loops for sustaining instruments (organ), and
encodes to Ogg Vorbis. Writes assets/<set>/index.json describing the zones.

    python3 tools/samples/build.py --salamander /path/Samples --vcsl /path/VCSL [--only yc5,stb]
"""
import argparse, glob, json, math, os, re, subprocess, sys, tempfile
import numpy as np

SR = 48000
PC = {'C': 0, 'C#': 1, 'D': 2, 'D#': 3, 'E': 4, 'F': 5, 'F#': 6, 'G': 7, 'G#': 8, 'A': 9, 'A#': 10, 'B': 11}


def piano_len(m):
    return float(np.clip(14.0 - (m - 21) * 0.11, 3.5, 14.0))


SETS = {
    'yc5': dict(name='Yamaha C5 Grand (Salamander)', src='salamander', glob='*.flac',
                rx=r'^(?P<n>[A-G]#?)(?P<o>-?\d)v(?P<l>\d+)\.flac$', layers=['4', '8', '12', '15'],
                stereo=True, length=piano_len, q=4),
    'stb': dict(name='Steinway B Grand (VCSL)', src='vcsl', glob='Chordophones/Zithers/Grand Piano, Steinway B/NoSus/*.wav',
                rx=r'Close_(?P<n>[A-G]#?)(?P<o>-?\d)_vl(?P<l>\d)_rr1\.wav$', layers=['2', '4'],
                stereo=True, length=piano_len, q=4),
    'yup': dict(name='Yamaha Upright (VCSL)', src='vcsl', glob='Chordophones/Zithers/Upright Piano, Yamaha/Sustains/*_rr1.wav',
                rx=r'Upright1_Sus_(?P<n>[A-G]#?)(?P<o>-?\d)_vl(?P<l>\d)_rr1\.wav$', layers=['1', '2', '3'],
                stereo=True, length=lambda m: piano_len(m) * 0.8, q=4,
                tune={24: -175}),  # lowest sample is recorded ~1.75 semitones flat
    'kup': dict(name='Knight Upright (VCSL)', src='vcsl', glob='Chordophones/Zithers/Upright Piano, Knight/Sustains/*.wav',
                rx=r'Player_vl(?P<l>\d)_rr1_(?P<n>[A-G]#?)(?P<o>-?\d)\.wav$', layers=None,
                stereo=True, length=lambda m: piano_len(m) * 0.8, q=4),
    'hpd': dict(name='Flemish Harpsichord (VCSL)', src='vcsl', glob='Chordophones/Zithers/Harpsichord, Flemish/Sustains/Low/*.wav',
                rx=r'HarpsiRH_Low_Far_(?P<n>[A-G]#?)(?P<o>-?\d)_rr1\.wav$', layers=None,
                stereo=False, length=lambda m: 6.0, q=3),
    'vib': dict(name='Vibraphone (VCSL)', src='vcsl', glob='Idiophones/Struck Idiophones/Vibraphone/*/*.wav',
                rx=r'Vibes_(?P<l>soft|hard)_(?P<n>[A-G]#?)(?P<o>-?\d)_v\d_rr\d_Main\.wav$', layers=['soft', 'hard'],
                stereo=False, length=lambda m: 7.0, q=3, offset=12),
    'mar': dict(name='Marimba (VCSL)', src='vcsl', glob='Idiophones/Struck Idiophones/Marimba/*.wav',
                rx=r'Outrigger_(?P<n>[A-G]#?)(?P<o>-?\d)_(?P<l>soft|med|loud)_\d+\.wav$', layers=['soft', 'med', 'loud'],
                stereo=False, length=lambda m: 3.0, q=3, offset=12),
    'fmp': dict(name='FM Electric Piano (VCSL TX81Z)', src='vcsl', glob='Electrophones/TX81Z/FM Piano/*.wav',
                rx=r'FMPiano_(?P<n>[A-G]#?)(?P<o>-?\d)_vl(?P<l>\d)\.wav$', layers=['1', '3'],
                stereo=False, length=lambda m: 7.0, q=3),
    'org': dict(name='Pipe Organ (VCSL)', src='vcsl', glob='Aerophones/Edge-blown Aerophones/Pipe Organ/Loud/Rode_Man3Open_*.wav',
                rx=r'Rode_Man3Open_(?P<n>[A-G]#?)(?P<o>-?\d)\.wav$', layers=None,
                stereo=False, length=lambda m: 8.0, q=3, loop=True),
    'glk': dict(name='Glockenspiel (VCSL)', src='vcsl', glob='Idiophones/Struck Idiophones/Glockenspiel/glock_*.wav',
                rx=r'glock_(?P<l>soft|medium|loud)_(?P<n>[A-G]#?)(?P<o>-?\d)_\d+\.wav$', layers=['soft', 'medium', 'loud'],
                stereo=False, length=lambda m: 4.0, q=3, offset=12),
}

STORE_DB = -14.0  # headroom in storage; the engine scales back up


def decode(path, ch):
    out = subprocess.run(['ffmpeg', '-v', 'error', '-i', path, '-f', 'f32le', '-ac', str(ch), '-ar', str(SR), '-'],
                         capture_output=True, check=True).stdout
    return np.frombuffer(out, dtype=np.float32).reshape(-1, ch).astype(np.float64)


def detect_midi(mono):
    """Fundamental via harmonic product spectrum on a 0.5 s window."""
    n = 1 << 16
    seg = mono[:n] if len(mono) >= n else np.pad(mono, (0, n - len(mono)))
    spec = np.abs(np.fft.rfft(seg * np.hanning(n)))
    hps = np.log(spec + 1e-9)
    for h in (2, 3, 4):
        d = spec[::h]
        hps[:len(d)] += np.log(d + 1e-9)
    freqs = np.fft.rfftfreq(n, 1 / SR)
    lo, hi = np.searchsorted(freqs, 25), np.searchsorted(freqs, 4500)
    k = lo + int(np.argmax(hps[lo:hi]))
    return 69 + 12 * math.log2(freqs[k] / 440)


def build(key, cfg, srcdir, outroot):
    files = sorted(glob.glob(os.path.join(srcdir, cfg['glob'])))
    rx = re.compile(cfg['rx'])
    items = []
    for f in files:
        mt = rx.search(os.path.basename(f))
        if not mt:
            continue
        g = mt.groupdict()
        m = (int(g['o']) + 1) * 12 + PC[g['n']]
        items.append((f, m, g.get('l') or '1'))
    if not items:
        print(f'{key}: no files matched', file=sys.stderr)
        return
    layers = cfg['layers'] or sorted({it[2] for it in items}, key=lambda s: int(s) if s.isdigit() else s)
    items = [it for it in items if it[2] in layers]
    ch = 2 if cfg['stereo'] else 1

    decoded, diffs = [], []
    for f, m, l in items:
        x = decode(f, ch)
        mono = x.mean(axis=1)
        pk = np.max(np.abs(mono)) + 1e-12
        on = int(np.argmax(np.abs(mono) > pk * 0.01))
        on = max(0, on - int(0.003 * SR))
        x, mono = x[on:], mono[on:]
        a = int(0.06 * SR)
        det = detect_midi(mono[a:a + int(0.6 * SR)])
        diffs.append(det - m)
        decoded.append((f, m, l, x, det))
    off = int(round(np.median(diffs) / 12)) * 12
    if 'offset' in cfg:  # mallet overtones fool pitch detection; VCSL names these an octave low
        off = cfg['offset']
    print(f'{key}: {len(decoded)} samples, {len(layers)} layers {layers}, octave offset {off:+d}')

    outdir = os.path.join(outroot, key)
    os.makedirs(outdir, exist_ok=True)
    for old in glob.glob(os.path.join(outdir, '*')):
        os.remove(old)
    nl = len(layers)
    targets = np.linspace(-24, -9, nl) if nl > 1 else np.array([-12.0])
    zones = []
    for f, m, l, x, det in decoded:
        root = m + off
        dev = det - root
        if abs(dev - 12 * round(dev / 12)) > 0.6 and 'offset' not in cfg:
            print(f'   ! {os.path.basename(f)}: detected {det:.2f}, using {root} (check)', file=sys.stderr)
        li = layers.index(l)
        mono = x.mean(axis=1)
        # Cut inaudible tail (-70 dB below peak, 50 ms envelope), then cap length.
        env = np.convolve(np.abs(mono), np.ones(2400) / 2400, mode='same')
        pk = env.max() + 1e-12
        above = np.nonzero(env > pk * 10 ** (-70 / 20))[0]
        end = above[-1] if len(above) else len(mono)
        end = min(end, int(cfg['length'](root) * SR))
        loop = None
        if cfg.get('loop'):
            end = min(len(mono), int(cfg['length'](root) * SR))
            ls, le = int(end * 0.3), int(end * 0.92)
            n = int(0.25 * SR)
            w = np.sin(np.linspace(0, np.pi / 2, n)) ** 2
            seg = x[le - n:le] * (1 - w)[:, None] + x[ls - n:ls] * w[:, None]
            x = x.copy()
            x[le - n:le] = seg
            end = le
            loop = (ls, le)
        x = x[:end].copy()
        fade = int(0.0015 * SR)
        x[:fade] *= np.linspace(0, 1, fade)[:, None]
        if loop is None:
            fo = min(int(0.6 * SR), len(x) // 3)
            x[-fo:] *= (np.cos(np.linspace(0, np.pi, fo)) * 0.5 + 0.5)[:, None]
        # Level: RMS of the first 0.4 s -> layer target.
        rms = np.sqrt(np.mean(x[:int(0.4 * SR)] ** 2)) + 1e-12
        gain = 10 ** ((targets[li] + STORE_DB) / 20) / rms
        x *= gain
        if np.max(np.abs(x)) > 0.98:
            x *= 0.98 / np.max(np.abs(x))
        name = f'm{root:03d}_l{li}.ogg'
        with tempfile.NamedTemporaryFile(suffix='.wav') as tmp:
            raw = x.astype(np.float32)
            import wave  # noqa
            # Write float WAV via ffmpeg from raw f32.
            subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'f32le', '-ac', str(ch), '-ar', str(SR), '-i', '-',
                            '-c:a', 'libvorbis', '-q:a', str(cfg['q']), os.path.join(outdir, name)],
                           input=raw.tobytes(), check=True)
        z = dict(file=name, root=root, layer=li, frames=len(x))
        if root in cfg.get('tune', {}):
            z['tune'] = cfg['tune'][root]
        if loop:
            z['loopStart'], z['loopEnd'] = loop
        zones.append(z)
    zones.sort(key=lambda z: (z['layer'], z['root']))
    idx = dict(name=cfg['name'], channels=ch, layers=nl, zones=zones)
    with open(os.path.join(outdir, 'index.json'), 'w') as fh:
        json.dump(idx, fh, indent=1)
    size = sum(os.path.getsize(p) for p in glob.glob(os.path.join(outdir, '*.ogg')))
    secs = sum(z['frames'] for z in zones) / SR
    print(f'   -> {len(zones)} zones, {secs:.0f} s audio, {size / 1e6:.1f} MB, roots {sorted({z["root"] for z in zones})}')


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--salamander')
    ap.add_argument('--vcsl')
    ap.add_argument('--out', default=os.path.join(os.path.dirname(__file__), '..', '..', 'assets'))
    ap.add_argument('--only')
    a = ap.parse_args()
    only = set(a.only.split(',')) if a.only else None
    for key, cfg in SETS.items():
        if only and key not in only:
            continue
        src = a.salamander if cfg['src'] == 'salamander' else a.vcsl
        if not src:
            continue
        build(key, cfg, src, a.out)


if __name__ == '__main__':
    main()
