package omr

import (
	"image"
	_ "image/png"
	"os"
	"testing"
)

// TestImagePage runs recognition on a page image (as if the user dropped a
// PNG). It is skipped when the sample scan is not present.
func TestImagePage(t *testing.T) {
	f, err := os.Open("/home/claude/scan/pg-004.png")
	if err != nil {
		t.Skip("sample page not available")
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	res := ReadPages([]*Bitmap{Binarize(img)}, "page", nil)
	if len(res.Songs) != 1 {
		t.Fatalf("want 1 piece, got %d", len(res.Songs))
	}
	s := res.Songs[0]
	if s.KeyFifths != -3 || len(s.Notes) < 400 {
		t.Fatalf("key %d notes %d", s.KeyFifths, len(s.Notes))
	}
	// the pickup is a single B-flat 4
	if s.Notes[0].Pitch != 70 {
		t.Fatalf("first note %d, want 70 (Bb4)", s.Notes[0].Pitch)
	}
}

func TestKeyOrder(t *testing.T) {
	if keyAlter(6, -1) != -1 || keyAlter(2, -1) != 0 || keyAlter(3, 1) != 1 || keyAlter(0, 2) != 1 {
		t.Fatal("key signature alteration table is wrong")
	}
}
