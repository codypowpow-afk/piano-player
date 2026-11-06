// sheet2song reads a scanned sheet-music PDF and writes lesson songs as JSON.
//
//	go run ./tools/sheet2song -o songs.json score.pdf
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codypowpow-afk/piano-player/omr"
	"github.com/codypowpow-afk/piano-player/score"
)

func main() {
	out := flag.String("o", "", "output JSON file")
	show := flag.Int("show", 0, "print the first N measures of each piece")
	title := flag.String("title", "", "title (default: file name)")
	flag.Parse()
	path := flag.Arg(0)
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *title == "" {
		*title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	t := time.Now()
	res, err := omr.ReadPDF(data, *title, func(d, n int, msg string) { fmt.Fprintf(os.Stderr, "\r%s   ", msg) })
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "done in %.1fs\n", time.Since(t).Seconds())
	for i, s := range res.Songs {
		lo, hi := s.PitchRange()
		fmt.Printf("%s: key %d, %d measures, %d notes, range %s-%s, starts page %d\n", s.Title, s.KeyFifths, len(s.Measures), len(s.Notes), score.NoteName(lo), score.NoteName(hi), res.Pieces[i].FirstPage+1)
		for m := 0; m < *show && m < len(s.Measures); m++ {
			for hand := 0; hand < 2; hand++ {
				var parts []string
				var last float64 = -1
				for _, n := range s.Notes {
					if n.Measure != m || n.Hand != hand {
						continue
					}
					sep := " "
					if n.Start-last < 1e-6 {
						sep = "+"
					}
					last = n.Start
					parts = append(parts, sep+score.NoteName(n.Pitch))
				}
				fmt.Printf("  m%-3d %s:%s\n", m+1, map[int]string{0: "R", 1: "L"}[hand], strings.Join(parts, ""))
			}
		}
	}
	if *out != "" {
		b, _ := json.Marshal(res.Songs)
		os.WriteFile(*out, b, 0o644)
	}
}
