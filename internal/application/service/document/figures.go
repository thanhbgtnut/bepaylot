package document

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"image/jpeg"

	"github.com/thanhenti/bepaylot/internal/types"
)

// cropFigures stores a JPEG crop of every figure block that references an
// asset key, so markdown image links resolve (§5.3). The page image is only
// downloaded when the page has figures.
func (s *Service) cropFigures(ctx context.Context, row types.DocumentPage, page *types.ParsedPage) error {
	var figs []int
	for i, b := range page.Blocks {
		if b.Type == types.BlockFigure && b.AssetKey != "" && b.BBox.Width() > 8 && b.BBox.Height() > 8 {
			figs = append(figs, i)
		}
	}
	if len(figs) == 0 {
		return nil
	}
	rc, _, err := s.objects.Get(ctx, row.ImageKey)
	if err != nil {
		return err
	}
	src, err := jpeg.Decode(rc)
	rc.Close()
	if err != nil {
		return err
	}
	bounds := src.Bounds()
	for _, i := range figs {
		b := page.Blocks[i].BBox
		r := image.Rect(int(b.X0), int(b.Y0), int(b.X1), int(b.Y1)).Intersect(bounds)
		if r.Empty() {
			page.Blocks[i].AssetKey = ""
			continue
		}
		dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return err
		}
		if _, err := s.objects.Put(ctx, page.Blocks[i].AssetKey, &buf, int64(buf.Len()), "image/jpeg"); err != nil {
			return err
		}
	}
	return nil
}
