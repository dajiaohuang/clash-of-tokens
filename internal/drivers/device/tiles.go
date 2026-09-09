package device

import (
	"context"
	"errors"
	"image"
	"sort"
	"strings"
)

// TiledOCR preserves source coordinates while enlarging narrow overlapping
// crops. The native runner is shared by all profiles; no Python process exists.
func TiledOCR(ctx context.Context, ocr OCR, img image.Image, region image.Rectangle, width, overlap, scale int) ([]Line, error) {
	if width < 200 || width > 2000 || overlap < 0 || overlap >= width || scale < 1 || scale > 3 || !region.In(img.Bounds()) {
		return nil, errors.New("device: invalid OCR tiling plan")
	}
	var out []Line
	for x := region.Min.X; x < region.Max.X; x += width - overlap {
		end := min(x+width, region.Max.X)
		tile := image.NewRGBA(image.Rect(0, 0, (end-x)*scale, region.Dy()*scale))
		for yy := 0; yy < tile.Bounds().Dy(); yy++ {
			if yy%64 == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			for xx := 0; xx < tile.Bounds().Dx(); xx++ {
				tile.Set(xx, yy, img.At(x+xx/scale, region.Min.Y+yy/scale))
			}
		}
		lines, e := ocr.Read(ctx, tile, tile.Bounds())
		if e != nil {
			return nil, e
		}
		for _, l := range lines {
			l.Box = image.Rect(x+l.Box.Min.X/scale, region.Min.Y+l.Box.Min.Y/scale, x+l.Box.Max.X/scale, region.Min.Y+l.Box.Max.Y/scale)
			out = append(out, l)
		}
		if end == region.Max.X {
			break
		}
	}
	return out, nil
}
func MergeRows(lines []Line, tolerance int) []Line {
	sorted := append([]Line(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Box, sorted[j].Box
		if a.Min.Y+a.Max.Y != b.Min.Y+b.Max.Y {
			return a.Min.Y+a.Max.Y < b.Min.Y+b.Max.Y
		}
		return a.Min.X < b.Min.X
	})
	var rows [][]Line
	for _, l := range sorted {
		if l.Text == "" || l.Box.Empty() {
			continue
		}
		placed := false
		cy := (l.Box.Min.Y + l.Box.Max.Y) / 2
		for i, row := range rows {
			sum := 0
			for _, r := range row {
				sum += (r.Box.Min.Y + r.Box.Max.Y) / 2
			}
			d := cy - sum/len(row)
			if d >= -tolerance && d <= tolerance {
				rows[i] = append(rows[i], l)
				placed = true
				break
			}
		}
		if !placed {
			rows = append(rows, []Line{l})
		}
	}
	var out []Line
	for _, row := range rows {
		sort.SliceStable(row, func(i, j int) bool { return row[i].Box.Min.X < row[j].Box.Min.X })
		merged := row[0]
		for _, next := range row[1:] {
			if strings.Contains(merged.Text, next.Text) {
				continue
			}
			if strings.Contains(next.Text, merged.Text) {
				merged.Text = next.Text
			} else {
				left, right := []rune(merged.Text), []rune(next.Text)
				overlap := 0
				for n := min(len(left), len(right)); n >= 2; n-- {
					if string(left[len(left)-n:]) == string(right[:n]) {
						overlap = n
						break
					}
				}
				if overlap > 0 {
					merged.Text += string(right[overlap:])
				} else {
					merged.Text += " " + next.Text
				}
			}
			merged.Box = merged.Box.Union(next.Box)
			merged.Confidence = min(merged.Confidence, next.Confidence)
		}
		out = append(out, merged)
	}
	return out
}
