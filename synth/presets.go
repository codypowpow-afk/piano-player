package synth

import "math"

// Preset is one playable sound: a sample set plus how it is played and processed.
type Preset struct {
	Name     string
	Category string // Grand, Upright, Character, Keys
	Desc     string
	Set      string // sample set key in assets/

	// Playing
	VelGamma  float64 // <1 = lighter touch, >1 = heavier
	VelRange  float64 // extra dB of dynamics on top of the velocity layers
	Release   float64 // damper time constant (s)
	HoldDecay float64 // >0: notes die away even while held (muted piano), s
	AttackMs  float64 // soften the attack
	Detune    float64 // cents between two detuned copies (honky-tonk)
	OctaveUp  float64 // level of a quiet octave-up layer
	Piano     bool    // top octave has no dampers, like a real piano
	GainDB    float64

	// Tone
	HighPass  float64 // Hz
	LowPass   float64 // Hz
	LowShelf  float64 // dB @ 180 Hz
	PeakHz    float64
	PeakDB    float64
	PeakQ     float64
	HighShelf float64 // dB @ 4 kHz
	Drive     float64 // 0..1 saturation
	Comp      float64 // 0..1 compression
	Wobble    float64 // 0..1 tape wow & flutter
	Crackle   float64 // 0..1 vinyl crackle + hiss
	TremHz    float64
	TremDepth float64
	Width     float64 // 0 mono, 1 as recorded, >1 wider

	// Room
	RoomSize float64
	RoomDamp float64
	RoomWet  float64
}

func (p *Preset) gainLin() float64 { return math.Pow(10, p.GainDB/20) }

// grand returns a natural grand piano preset to customise.
func grand(name, set, desc string) Preset {
	return Preset{Name: name, Category: "Grand", Desc: desc, Set: set, VelGamma: 1, VelRange: 10,
		Release: 0.28, Piano: true, Width: 1, RoomSize: 0.82, RoomDamp: 0.4, RoomWet: 0.15, PeakQ: 1}
}

var Presets = func() []Preset {
	var ps []Preset
	add := func(p Preset, f func(p *Preset)) {
		if f != nil {
			f(&p)
		}
		ps = append(ps, p)
	}
	// ---------------- Grand pianos
	add(grand("Concert Grand", "yc5", "Yamaha C5, natural, medium hall"), nil)
	add(grand("Studio Grand", "yc5", "Close and dry, ready for a mix"), func(p *Preset) {
		p.RoomWet, p.RoomSize = 0.05, 0.5
		p.PeakHz, p.PeakDB = 3000, 1.5
		p.Comp = 0.25
	})
	add(grand("Bright Pop Grand", "yc5", "Sparkly, compressed, cuts through"), func(p *Preset) {
		p.HighShelf, p.LowShelf = 5, -2
		p.PeakHz, p.PeakDB = 2500, 2
		p.Comp, p.RoomWet, p.RoomSize = 0.5, 0.09, 0.6
		p.GainDB = -1
	})
	add(grand("Warm Jazz Grand", "yc5", "Dark and round, club room"), func(p *Preset) {
		p.HighShelf = -5
		p.PeakHz, p.PeakDB = 250, 2
		p.VelGamma, p.RoomWet, p.RoomSize = 0.9, 0.12, 0.7
		p.GainDB = 1.5
	})
	add(grand("Concert Hall", "yc5", "Big hall, long natural tail"), func(p *Preset) {
		p.RoomWet, p.RoomSize, p.RoomDamp = 0.3, 0.93, 0.3
		p.Width = 1.15
	})
	add(grand("Cathedral Grand", "yc5", "Huge stone space"), func(p *Preset) {
		p.RoomWet, p.RoomSize, p.RoomDamp = 0.5, 0.975, 0.2
		p.Release, p.GainDB = 0.5, -3
	})
	add(grand("Ballad Grand", "yc5", "Light touch, lush and wide"), func(p *Preset) {
		p.VelGamma, p.Release = 0.8, 0.45
		p.RoomWet, p.RoomSize = 0.25, 0.9
		p.Width, p.HighShelf = 1.25, -1.5
	})
	add(grand("Steinway B", "stb", "Steinway Model B, natural room"), nil)
	add(grand("Steinway Intimate", "stb", "Steinway B, close and personal"), func(p *Preset) {
		p.RoomWet, p.RoomSize = 0.05, 0.45
		p.PeakHz, p.PeakDB = 220, 1.5
		p.HighShelf = -1
	})
	// ---------------- Uprights
	up := func(name, set, desc string) Preset {
		p := grand(name, set, desc)
		p.Category = "Upright"
		p.RoomWet, p.RoomSize, p.RoomDamp = 0.1, 0.55, 0.55
		p.Release = 0.22
		return p
	}
	add(up("Yamaha Upright", "yup", "Classic home upright"), nil)
	add(up("Knight Upright", "kup", "Vintage British upright, woody"), nil)
	add(up("Honky-Tonk", "kup", "Out-of-tune saloon piano"), func(p *Preset) {
		p.Detune = 24
		p.HighShelf = 3
		p.PeakHz, p.PeakDB = 1200, 2
		p.Comp, p.RoomWet, p.RoomSize = 0.3, 0.08, 0.45
	})
	add(up("Felt Upright", "yup", "Felt mute down, soft and close"), func(p *Preset) {
		p.LowPass, p.AttackMs = 1800, 6
		p.PeakHz, p.PeakDB = 300, 2
		p.VelRange, p.RoomWet = 14, 0.12
		p.GainDB = 4
	})
	// ---------------- Character
	ch := func(name, set, desc string) Preset {
		p := grand(name, set, desc)
		p.Category = "Character"
		return p
	}
	add(ch("Felt Piano", "yc5", "Cinematic felt grand, very soft"), func(p *Preset) {
		p.LowPass, p.AttackMs = 1500, 9
		p.PeakHz, p.PeakDB = 280, 2.5
		p.VelRange, p.VelGamma = 14, 0.85
		p.RoomWet, p.RoomSize = 0.16, 0.75
		p.GainDB = 5
	})
	add(ch("Dream Piano", "yc5", "Octave shimmer and endless reverb"), func(p *Preset) {
		p.OctaveUp, p.Detune = 0.3, 6
		p.RoomWet, p.RoomSize, p.RoomDamp = 0.45, 0.96, 0.25
		p.Wobble, p.LowPass, p.Width = 0.15, 9000, 1.3
		p.Release, p.GainDB = 0.6, -3
	})
	add(ch("Muted Piano", "yc5", "Palm-muted, short and percussive"), func(p *Preset) {
		p.HoldDecay, p.Release = 0.32, 0.12
		p.LowPass, p.PeakHz, p.PeakDB = 5000, 400, 2
		p.RoomWet = 0.08
		p.GainDB = 2
	})
	add(ch("Lo-Fi Piano", "yup", "Warped tape, dusty and warm"), func(p *Preset) {
		p.Wobble, p.Crackle, p.Drive = 0.8, 0.45, 0.35
		p.LowPass, p.HighPass = 3800, 110
		p.Width, p.RoomWet, p.RoomSize = 0.6, 0.14, 0.6
		p.Comp, p.GainDB = 0.3, 1
	})
	add(ch("Old Record", "yc5", "1940s 78 rpm record"), func(p *Preset) {
		p.HighPass, p.LowPass = 320, 3300
		p.PeakHz, p.PeakDB = 1500, 3
		p.Crackle, p.Drive, p.Wobble = 1, 0.3, 0.3
		p.Width, p.RoomWet = 0, 0.1
		p.GainDB = 3
	})
	// ---------------- Other keyboards
	key := func(name, set, desc string) Preset {
		return Preset{Name: name, Category: "Keys", Desc: desc, Set: set, VelGamma: 1, VelRange: 8,
			Release: 0.3, Width: 1, RoomSize: 0.75, RoomDamp: 0.45, RoomWet: 0.15, PeakQ: 1}
	}
	add(key("Harpsichord", "hpd", "Flemish harpsichord"), func(p *Preset) {
		p.VelRange, p.Release = 3, 0.2
		p.RoomWet, p.RoomSize = 0.2, 0.8
	})
	add(key("FM Electric Piano", "fmp", "Yamaha TX81Z, 80s ballad EP"), func(p *Preset) {
		p.Width, p.Wobble = 1.2, 0.1
		p.RoomWet = 0.13
	})
	add(key("Vibraphone", "vib", "Motor on, soft tremolo"), func(p *Preset) {
		p.TremHz, p.TremDepth = 5.2, 0.35
		p.Release, p.RoomWet, p.RoomSize = 0.9, 0.2, 0.82
	})
	add(key("Marimba", "mar", "Rosewood bars, warm mallets"), func(p *Preset) {
		p.Release, p.RoomWet = 0.6, 0.14
		p.GainDB = 1
	})
	add(key("Pipe Organ", "org", "Church organ, open diapason"), func(p *Preset) {
		p.VelRange, p.Release, p.AttackMs = 0, 0.12, 15
		p.RoomWet, p.RoomSize, p.RoomDamp = 0.45, 0.96, 0.25
		p.GainDB = -8
	})
	add(key("Glockenspiel", "glk", "Music-box bells"), func(p *Preset) {
		p.Release, p.RoomWet, p.RoomSize = 1.5, 0.25, 0.8
		p.GainDB = -2
	})
	return ps
}()
