package vlm

import (
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Layout of a stitched batch: every region is preceded by a black bar that
// carries its number [k] in white, and followed by a white gap.
const (
	barHeight = 34
	gap       = 14
	margin    = 8
	labelZoom = 2 // basicfont is 13 px tall; ×2 keeps the number legible
)

// stitch stacks the crops top to bottom on a white canvas, each under a
// numbered bar (1-based, in order).
func stitch(src image.Image, rects []image.Rectangle) image.Image {
	w, h := 0, 0
	for _, r := range rects {
		w = max(w, r.Dx())
		h += barHeight + r.Dy() + gap
	}
	w += 2 * margin
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	y := 0
	for i, r := range rects {
		bar := image.Rect(0, y, w, y+barHeight)
		draw.Draw(dst, bar, image.Black, image.Point{}, draw.Src)
		drawLabel(dst, bar, fmt.Sprintf("[%d]", i+1))
		y += barHeight
		draw.Draw(dst, image.Rect(margin, y, margin+r.Dx(), y+r.Dy()), src, r.Min, draw.Src)
		y += r.Dy() + gap
	}
	return dst
}

// drawLabel writes s in white at the left of bar, zoomed labelZoom times.
func drawLabel(dst *image.RGBA, bar image.Rectangle, s string) {
	face := basicfont.Face7x13
	small := image.NewRGBA(image.Rect(0, 0, face.Advance*len(s)+2, face.Height))
	d := font.Drawer{Dst: small, Src: image.NewUniform(color.White), Face: face, Dot: fixed.P(1, face.Ascent)}
	d.DrawString(s)
	zoomed := image.Rect(0, 0, small.Bounds().Dx()*labelZoom, small.Bounds().Dy()*labelZoom)
	off := image.Pt(bar.Min.X+margin, bar.Min.Y+(bar.Dy()-zoomed.Dy())/2)
	draw.NearestNeighbor.Scale(dst, zoomed.Add(off), small, small.Bounds(), draw.Over, nil)
}

// reMarker matches a region marker on its own line start: "<<<3>>>", and the
// looser "[3]" line some models write instead.
var reMarker = regexp.MustCompile(`(?m)^[ \t]*(?:<<<\s*(\d+)\s*>>>|\[(\d+)\][ \t]*$)[ \t]*\n?`)

// splitBatch cuts a batch answer at its markers: region number → text.
func splitBatch(answer string) map[int]string {
	_, answer = SplitFrontMatter(answer)
	locs := reMarker.FindAllStringSubmatchIndex(answer, -1)
	out := map[int]string{}
	for i, m := range locs {
		var num string
		if m[2] >= 0 {
			num = answer[m[2]:m[3]]
		} else {
			num = answer[m[4]:m[5]]
		}
		k, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		end := len(answer)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if _, dup := out[k]; !dup {
			out[k] = strings.TrimSpace(answer[m[1]:end])
		}
	}
	return out
}
