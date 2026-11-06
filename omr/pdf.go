package omr

// A small, tolerant PDF reader whose only job is to pull the scanned page
// images out of a sheet-music PDF, in page order. It understands classic
// xref files and compressed object streams, and decodes CCITT fax (G3/G4),
// JPEG (DCT) and Flate images. Vector PDFs (no page images) are reported as
// such: they need a rasterizer, which this package does not have.

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"io"
	"regexp"
	"sort"
	"strconv"

	"golang.org/x/image/ccitt"
)

// Bitmap is a binary page image: Pix[y*W+x] is 1 for ink, 0 for paper.
type Bitmap struct {
	W, H int
	Pix  []uint8
}

func (b *Bitmap) At(x, y int) uint8 {
	if x < 0 || y < 0 || x >= b.W || y >= b.H {
		return 0
	}
	return b.Pix[y*b.W+x]
}

// ---- PDF object model -------------------------------------------------------

type pdfName string
type pdfRef struct{ num, gen int }
type pdfDict map[string]any
type pdfStream struct {
	dict pdfDict
	raw  []byte
}

type pdfFile struct {
	data []byte
	objs map[int]any // object number -> value (pdfDict, pdfStream, ...)
}

var objRe = regexp.MustCompile(`(\d+)\s+(\d+)\s+obj\b`)

func parsePDF(data []byte) (*pdfFile, error) {
	if !bytes.HasPrefix(bytes.TrimLeft(data, "\x00\r\n\t "), []byte("%PDF")) {
		return nil, errors.New("not a PDF file")
	}
	f := &pdfFile{data: data, objs: map[int]any{}}
	for _, m := range objRe.FindAllSubmatchIndex(data, -1) {
		num, _ := strconv.Atoi(string(data[m[2]:m[3]]))
		p := &parser{b: data, i: m[1]}
		v, err := p.value()
		if err != nil {
			continue
		}
		if d, ok := v.(pdfDict); ok {
			p.skipWS()
			if bytes.HasPrefix(data[p.i:], []byte("stream")) {
				p.i += 6
				if p.i < len(data) && data[p.i] == '\r' {
					p.i++
				}
				if p.i < len(data) && data[p.i] == '\n' {
					p.i++
				}
				start := p.i
				end := -1
				if n, ok := d["Length"].(int); ok && start+n <= len(data) {
					// verify endstream follows
					rest := bytes.TrimLeft(data[start+n:min(len(data), start+n+20)], "\r\n ")
					if bytes.HasPrefix(rest, []byte("endstream")) {
						end = start + n
					}
				}
				if end < 0 {
					k := bytes.Index(data[start:], []byte("endstream"))
					if k < 0 {
						continue
					}
					end = start + k
					for end > start && (data[end-1] == '\n' || data[end-1] == '\r') {
						end--
					}
				}
				v = &pdfStream{dict: d, raw: data[start:end]}
			}
		}
		f.objs[num] = v // later definitions (incremental updates) win
	}
	// expand object streams
	for _, v := range f.objs {
		s, ok := v.(*pdfStream)
		if !ok || f.name(s.dict["Type"]) != "ObjStm" {
			continue
		}
		body, err := f.decodeStream(s, true)
		if err != nil {
			continue
		}
		n := f.int(s.dict["N"])
		first := f.int(s.dict["First"])
		hp := &parser{b: body}
		type pair struct{ num, off int }
		var pairs []pair
		for k := 0; k < n; k++ {
			a, e1 := hp.value()
			b, e2 := hp.value()
			if e1 != nil || e2 != nil {
				break
			}
			ai, _ := a.(int)
			bi, _ := b.(int)
			pairs = append(pairs, pair{ai, bi})
		}
		for _, pr := range pairs {
			if _, exists := f.objs[pr.num]; exists {
				continue
			}
			op := &parser{b: body, i: first + pr.off}
			if val, err := op.value(); err == nil {
				f.objs[pr.num] = val
			}
		}
	}
	if len(f.objs) == 0 {
		return nil, errors.New("no PDF objects found")
	}
	return f, nil
}

func (f *pdfFile) resolve(v any) any {
	for i := 0; i < 32; i++ {
		r, ok := v.(pdfRef)
		if !ok {
			return v
		}
		v = f.objs[r.num]
	}
	return v
}

func (f *pdfFile) dict(v any) pdfDict {
	switch t := f.resolve(v).(type) {
	case pdfDict:
		return t
	case *pdfStream:
		return t.dict
	}
	return nil
}

func (f *pdfFile) name(v any) string {
	n, _ := f.resolve(v).(pdfName)
	return string(n)
}

func (f *pdfFile) int(v any) int {
	switch t := f.resolve(v).(type) {
	case int:
		return t
	case float64:
		return int(t)
	}
	return 0
}

func (f *pdfFile) array(v any) []any {
	switch t := f.resolve(v).(type) {
	case []any:
		return t
	case nil:
		return nil
	default:
		return []any{t}
	}
}

// pages returns the page dictionaries in document order.
func (f *pdfFile) pages() []pdfDict {
	var root pdfDict
	for _, v := range f.objs {
		if d := f.dict(v); d != nil && f.name(d["Type"]) == "Catalog" {
			root = d
		}
	}
	var out []pdfDict
	seen := map[*pdfDict]bool{}
	var walk func(node pdfDict, inherited pdfDict, depth int)
	walk = func(node pdfDict, inherited pdfDict, depth int) {
		if node == nil || depth > 64 || seen[&node] {
			return
		}
		res := inherited
		if r := f.dict(node["Resources"]); r != nil {
			res = r
		}
		if f.name(node["Type"]) == "Page" || node["Kids"] == nil {
			pd := pdfDict{}
			for k, v := range node {
				pd[k] = v
			}
			pd["Resources"] = res
			out = append(out, pd)
			return
		}
		for _, k := range f.array(node["Kids"]) {
			walk(f.dict(k), res, depth+1)
		}
	}
	if root != nil {
		walk(f.dict(root["Pages"]), nil, 0)
	}
	return out
}

// pageImages finds image XObjects used by a page (recursing into forms).
func (f *pdfFile) pageImages(res pdfDict, depth int) []*pdfStream {
	if res == nil || depth > 8 {
		return nil
	}
	var out []*pdfStream
	xo := f.dict(res["XObject"])
	keys := make([]string, 0, len(xo))
	for k := range xo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, ok := f.resolve(xo[k]).(*pdfStream)
		if !ok {
			continue
		}
		switch f.name(s.dict["Subtype"]) {
		case "Image":
			out = append(out, s)
		case "Form":
			out = append(out, f.pageImages(f.dict(s.dict["Resources"]), depth+1)...)
		}
	}
	return out
}

// PageSource yields page bitmaps one at a time so that a long score never
// has to be held in memory all at once.
type PageSource struct {
	f    *pdfFile
	imgs []*pdfStream
}

// Len is the number of pages.
func (ps *PageSource) Len() int { return len(ps.imgs) }

// Page decodes page i (0-based).
func (ps *PageSource) Page(i int) (*Bitmap, error) { return ps.f.decodeImage(ps.imgs[i]) }

// OpenPDF finds the scanned page images of a PDF without decoding them.
func OpenPDF(data []byte) (*PageSource, error) {
	f, err := parsePDF(data)
	if err != nil {
		return nil, err
	}
	var imgs []*pdfStream
	for _, pg := range f.pages() {
		var best *pdfStream
		for _, s := range f.pageImages(f.dict(pg["Resources"]), 0) {
			if best == nil || f.int(s.dict["Width"])*f.int(s.dict["Height"]) > f.int(best.dict["Width"])*f.int(best.dict["Height"]) {
				best = s
			}
		}
		if best != nil {
			imgs = append(imgs, best)
		}
	}
	if len(imgs) == 0 { // page tree unreadable: fall back to all big images in file order
		var nums []int
		for n := range f.objs {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		for _, n := range nums {
			if s, ok := f.objs[n].(*pdfStream); ok && f.name(s.dict["Subtype"]) == "Image" && f.int(s.dict["Width"]) > 1000 {
				imgs = append(imgs, s)
			}
		}
	}
	if len(imgs) == 0 {
		return nil, errors.New("this PDF has no scanned page images (it is probably a vector PDF exported from notation software) — export MIDI or MusicXML from that software instead, or export the pages as PNG")
	}
	return &PageSource{f: f, imgs: imgs}, nil
}

// ExtractPages returns one binarized bitmap per PDF page (the largest image
// on that page).
func ExtractPages(data []byte) ([]*Bitmap, error) {
	ps, err := OpenPDF(data)
	if err != nil {
		return nil, err
	}
	var out []*Bitmap
	var firstErr error
	for i := 0; i < ps.Len(); i++ {
		bm, err := ps.Page(i)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, bm)
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}

func (f *pdfFile) decodeStream(s *pdfStream, all bool) ([]byte, error) {
	data := s.raw
	filters := f.array(s.dict["Filter"])
	parms := f.array(s.dict["DecodeParms"])
	for i, fl := range filters {
		name := f.name(fl)
		var p pdfDict
		if i < len(parms) {
			p = f.dict(parms[i])
		}
		switch name {
		case "FlateDecode", "Fl":
			r, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			out, err := io.ReadAll(r)
			if err != nil && len(out) == 0 {
				return nil, err
			}
			data = out
			if p != nil && f.int(p["Predictor"]) >= 10 {
				data = pngUnpredict(data, max(1, f.int(p["Columns"])), max(1, f.int(p["Colors"])), max(1, f.int(p["BitsPerComponent"])))
			}
		default:
			if all {
				return nil, fmt.Errorf("unsupported filter %s", name)
			}
			return data, nil // leave image codecs to decodeImage
		}
	}
	return data, nil
}

func pngUnpredict(data []byte, cols, colors, bpc int) []byte {
	bpp := max(1, colors*bpc/8)
	row := (cols*colors*bpc + 7) / 8
	var out []byte
	prev := make([]byte, row)
	for i := 0; i+row+1 <= len(data); i += row + 1 {
		ft := data[i]
		cur := append([]byte(nil), data[i+1:i+1+row]...)
		for x := 0; x < row; x++ {
			var a, b, c byte
			if x >= bpp {
				a = cur[x-bpp]
				c = prev[x-bpp]
			}
			b = prev[x]
			switch ft {
			case 1:
				cur[x] += a
			case 2:
				cur[x] += b
			case 3:
				cur[x] += byte((int(a) + int(b)) / 2)
			case 4:
				pa, pb, pc := abs(int(b)-int(c)), abs(int(a)-int(c)), abs(int(a)+int(b)-2*int(c))
				if pa <= pb && pa <= pc {
					cur[x] += a
				} else if pb <= pc {
					cur[x] += b
				} else {
					cur[x] += c
				}
			}
		}
		out = append(out, cur...)
		prev = cur
	}
	return out
}

func (f *pdfFile) decodeImage(s *pdfStream) (*Bitmap, error) {
	w, h := f.int(s.dict["Width"]), f.int(s.dict["Height"])
	filters := f.array(s.dict["Filter"])
	parms := f.array(s.dict["DecodeParms"])
	last := ""
	var lp pdfDict
	if len(filters) > 0 {
		last = f.name(filters[len(filters)-1])
		if len(parms) >= len(filters) {
			lp = f.dict(parms[len(filters)-1])
		}
	}
	data, err := f.decodeStream(s, false)
	if err != nil {
		return nil, err
	}
	switch last {
	case "CCITTFaxDecode", "CCF":
		k := f.int(lp["K"])
		cols := f.int(lp["Columns"])
		if cols == 0 {
			cols = 1728
		}
		rows := f.int(lp["Rows"])
		if rows == 0 {
			rows = h
		}
		sf := ccitt.Group4
		if k >= 0 {
			sf = ccitt.Group3
			if k > 0 {
				return nil, errors.New("mixed 2-D Group 3 fax images are not supported")
			}
		}
		blackIs1 := false
		if b, ok := f.resolve(lp["BlackIs1"]).(bool); ok {
			blackIs1 = b
		}
		align := false
		if b, ok := f.resolve(lp["EncodedByteAlign"]).(bool); ok {
			align = b
		}
		r := ccitt.NewReader(bytes.NewReader(data), ccitt.MSB, sf, cols, rows, &ccitt.Options{Align: align, Invert: blackIs1})
		raw, err := io.ReadAll(r)
		if err != nil && len(raw) < ((cols+7)/8)*rows/2 {
			return nil, fmt.Errorf("CCITT decode: %v", err)
		}
		// ccitt output: 1 bits are white (paper) unless inverted
		bm := &Bitmap{W: cols, H: rows, Pix: make([]uint8, cols*rows)}
		stride := (cols + 7) / 8
		for y := 0; y < rows && (y+1)*stride <= len(raw); y++ {
			for x := 0; x < cols; x++ {
				bit := raw[y*stride+x/8] >> (7 - uint(x%8)) & 1
				if bit == 0 {
					bm.Pix[y*cols+x] = 1
				}
			}
		}
		fixPolarity(bm)
		return bm, nil
	case "DCTDecode", "DCT":
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return Binarize(img), nil
	case "JBIG2Decode":
		return nil, errors.New("this PDF uses JBIG2 image compression, which isn't supported yet — export the pages as PNG and open those instead")
	case "JPXDecode":
		return nil, errors.New("this PDF uses JPEG 2000 images, which aren't supported yet — export the pages as PNG and open those instead")
	case "", "FlateDecode", "Fl", "LZWDecode":
		bpc := f.int(s.dict["BitsPerComponent"])
		if bpc == 0 {
			bpc = 8
		}
		ncomp := 1
		switch cs := f.resolve(s.dict["ColorSpace"]).(type) {
		case pdfName:
			if cs == "DeviceRGB" {
				ncomp = 3
			} else if cs == "DeviceCMYK" {
				ncomp = 4
			}
		case []any:
			if len(cs) > 0 && f.name(cs[0]) == "Indexed" {
				return nil, errors.New("indexed-color page images are not supported")
			}
			if len(cs) > 1 && f.name(cs[0]) == "ICCBased" {
				ncomp = max(1, f.int(f.dict(cs[1])["N"]))
			}
		}
		if bpc == 1 {
			bm := &Bitmap{W: w, H: h, Pix: make([]uint8, w*h)}
			stride := (w + 7) / 8
			decode := f.array(s.dict["Decode"])
			inv := len(decode) == 2 && f.int(decode[0]) == 1
			for y := 0; y < h && (y+1)*stride <= len(data); y++ {
				for x := 0; x < w; x++ {
					bit := data[y*stride+x/8] >> (7 - uint(x%8)) & 1
					if (bit == 0) != inv {
						bm.Pix[y*w+x] = 1
					}
				}
			}
			fixPolarity(bm)
			return bm, nil
		}
		if bpc != 8 {
			return nil, fmt.Errorf("%d-bit page images are not supported", bpc)
		}
		g := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * ncomp
				if i+ncomp > len(data) {
					break
				}
				var v int
				switch ncomp {
				case 3:
					v = (int(data[i])*3 + int(data[i+1])*6 + int(data[i+2])) / 10
				case 4:
					v = 255 - min(255, (int(data[i])+int(data[i+1])+int(data[i+2]))/3+int(data[i+3]))
				default:
					v = int(data[i])
				}
				g.Pix[y*g.Stride+x] = uint8(v)
			}
		}
		return Binarize(g), nil
	}
	return nil, fmt.Errorf("unsupported image compression %s", last)
}

// fixPolarity flips a bitmap whose "ink" covers most of the page.
func fixPolarity(b *Bitmap) {
	n := 0
	for _, p := range b.Pix {
		n += int(p)
	}
	if n > len(b.Pix)/2 {
		for i := range b.Pix {
			b.Pix[i] ^= 1
		}
	}
}

// Binarize converts any image to ink/paper using Otsu's threshold.
func Binarize(img image.Image) *Bitmap {
	r := img.Bounds()
	g, ok := img.(*image.Gray)
	if !ok {
		g = image.NewGray(r)
		draw.Draw(g, r, img, r.Min, draw.Src)
	}
	var hist [256]int
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			hist[g.GrayAt(x, y).Y]++
		}
	}
	total := r.Dx() * r.Dy()
	sum := 0.0
	for i, c := range hist {
		sum += float64(i * c)
	}
	var sumB, wB float64
	best, thr := 0.0, 128
	for t := 0; t < 256; t++ {
		wB += float64(hist[t])
		if wB == 0 {
			continue
		}
		wF := float64(total) - wB
		if wF == 0 {
			break
		}
		sumB += float64(t * hist[t])
		mB, mF := sumB/wB, (sum-sumB)/wF
		v := wB * wF * (mB - mF) * (mB - mF)
		if v > best {
			best, thr = v, t
		}
	}
	bm := &Bitmap{W: r.Dx(), H: r.Dy(), Pix: make([]uint8, r.Dx()*r.Dy())}
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			if int(g.GrayAt(x+r.Min.X, y+r.Min.Y).Y) <= thr {
				bm.Pix[y*bm.W+x] = 1
			}
		}
	}
	fixPolarity(bm)
	return bm
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// ---- tokenizer ----------------------------------------------------------------

type parser struct {
	b []byte
	i int
}

func isWS(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}
func isDelim(c byte) bool {
	return isWS(c) || c == '/' || c == '[' || c == ']' || c == '<' || c == '>' || c == '(' || c == ')' || c == '{' || c == '}' || c == '%'
}

func (p *parser) skipWS() {
	for p.i < len(p.b) {
		c := p.b[p.i]
		if isWS(c) {
			p.i++
		} else if c == '%' {
			for p.i < len(p.b) && p.b[p.i] != '\n' && p.b[p.i] != '\r' {
				p.i++
			}
		} else {
			return
		}
	}
}

var errSyntax = errors.New("pdf syntax")

func (p *parser) value() (any, error) {
	p.skipWS()
	if p.i >= len(p.b) {
		return nil, io.EOF
	}
	c := p.b[p.i]
	switch {
	case c == '<' && p.i+1 < len(p.b) && p.b[p.i+1] == '<':
		p.i += 2
		d := pdfDict{}
		for {
			p.skipWS()
			if p.i+1 < len(p.b) && p.b[p.i] == '>' && p.b[p.i+1] == '>' {
				p.i += 2
				return d, nil
			}
			k, err := p.value()
			if err != nil {
				return nil, err
			}
			kn, ok := k.(pdfName)
			if !ok {
				return nil, errSyntax
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			d[string(kn)] = v
		}
	case c == '[':
		p.i++
		var arr []any
		for {
			p.skipWS()
			if p.i < len(p.b) && p.b[p.i] == ']' {
				p.i++
				return arr, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
	case c == '/':
		p.i++
		s := p.i
		for p.i < len(p.b) && !isDelim(p.b[p.i]) {
			p.i++
		}
		return pdfName(p.b[s:p.i]), nil
	case c == '(':
		depth := 0
		var out []byte
		for p.i < len(p.b) {
			ch := p.b[p.i]
			p.i++
			if ch == '\\' && p.i < len(p.b) {
				out = append(out, p.b[p.i])
				p.i++
				continue
			}
			if ch == '(' {
				depth++
				if depth == 1 {
					continue
				}
			}
			if ch == ')' {
				depth--
				if depth == 0 {
					return string(out), nil
				}
			}
			out = append(out, ch)
		}
		return nil, errSyntax
	case c == '<':
		e := bytes.IndexByte(p.b[p.i:], '>')
		if e < 0 {
			return nil, errSyntax
		}
		p.i += e + 1
		return "", nil
	default:
		s := p.i
		for p.i < len(p.b) && !isDelim(p.b[p.i]) {
			p.i++
		}
		tok := string(p.b[s:p.i])
		if tok == "" {
			p.i++
			return nil, errSyntax
		}
		switch tok {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		if n, err := strconv.Atoi(tok); err == nil {
			// possible "n g R"
			save := p.i
			p.skipWS()
			s2 := p.i
			for p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '9' {
				p.i++
			}
			if p.i > s2 {
				g, _ := strconv.Atoi(string(p.b[s2:p.i]))
				p.skipWS()
				if p.i < len(p.b) && p.b[p.i] == 'R' && (p.i+1 >= len(p.b) || isDelim(p.b[p.i+1])) {
					p.i++
					return pdfRef{n, g}, nil
				}
			}
			p.i = save
			return n, nil
		}
		if fv, err := strconv.ParseFloat(tok, 64); err == nil {
			return fv, nil
		}
		return pdfName(tok), nil // operator/keyword
	}
}
