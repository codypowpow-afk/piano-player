package omr

func DebugStaves(full *Bitmap) (*Bitmap, []*Staff, int, int) {
	_, sp := runStats(full)
	f := float64(sp) / 20
	b := Downscale(full, f)
	thick, space := runStats(b)
	return b, findStaves(b, thick, space), thick, space
}

func (s *Staff) Debug() ([]float64, [5][]float64) { return s.xs, s.ys }
