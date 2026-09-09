package device

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Line struct {
	Text       string
	Box        image.Rectangle
	Confidence float64
}
type OCR interface {
	Read(context.Context, image.Image, image.Rectangle) ([]Line, error)
}

// Tesseract is an explicit local native binary dependency, never a web service.
type Tesseract struct{ Path, DataDir string }

func (t Tesseract) Read(ctx context.Context, img image.Image, region image.Rectangle) ([]Line, error) {
	if t.Path == "" {
		return nil, errors.New("device: OCR executable is not configured")
	}
	region = region.Intersect(img.Bounds())
	if region.Empty() {
		return nil, errors.New("device: empty OCR region")
	}
	crop := image.NewRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	draw.Draw(crop, crop.Bounds(), img, region.Min, draw.Src)
	var input bytes.Buffer
	if e := png.Encode(&input, crop); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"stdin", "stdout", "-l", "chi_sim", "--psm", "6"}
	if t.DataDir != "" {
		args = append(args, "--tessdata-dir", t.DataDir)
	}
	args = append(args, "tsv")
	cmd := exec.CommandContext(ctx, t.Path, args...)
	hideCommand(cmd)
	cmd.Stdin = &input
	out := &boundedBuffer{limit: 4 << 20}
	errout := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = errout
	cmd.WaitDelay = time.Second
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("device: OCR failed; Tesseract and chi_sim language data required")
	}
	return ParseTSV(out.b.Bytes(), region.Min)
}
func ParseTSV(data []byte, offset image.Point) ([]Line, error) {
	if len(data) > 4<<20 {
		return nil, errors.New("device: OCR output too large")
	}
	rows := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(rows) == 0 || rows[0] != "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext" {
		return nil, errors.New("device: invalid OCR header")
	}
	var lines []Line
	lastKey := ""
	for _, raw := range rows[1:] {
		if raw == "" {
			continue
		}
		row := strings.SplitN(raw, "\t", 12)
		if len(row) != 12 {
			return nil, errors.New("device: malformed OCR row")
		}
		if row[0] != "5" || strings.TrimSpace(row[11]) == "" {
			continue
		}
		var nums [4]int
		var e error
		for i := range nums {
			nums[i], e = strconv.Atoi(row[6+i])
			if e != nil || nums[i] < 0 || nums[i] > 16384 {
				return nil, errors.New("device: invalid OCR bounds")
			}
		}
		confidence, e := strconv.ParseFloat(row[10], 64)
		if e != nil || math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 100 {
			continue
		}
		box := image.Rect(nums[0], nums[1], nums[0]+nums[2], nums[1]+nums[3]).Add(offset)
		key := strings.Join(row[1:5], "/")
		if key == lastKey && len(lines) > 0 {
			p := &lines[len(lines)-1]
			p.Text += " " + row[11]
			p.Box = p.Box.Union(box)
			p.Confidence = min(p.Confidence, confidence/100)
		} else {
			lines = append(lines, Line{row[11], box, confidence / 100})
			lastKey = key
		}
		if len(lines) > 20000 {
			return nil, errors.New("device: too many OCR lines")
		}
	}
	return lines, nil
}
func Capture(ctx context.Context, r Runner) (image.Image, error) {
	data, e := r.Run(ctx, "exec-out", "screencap", "-p")
	if e != nil {
		return nil, e
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 16<<20 {
		return nil, errors.New("device: invalid or oversized screenshot")
	}
	img, e := png.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, errors.New("device: invalid PNG screenshot")
	}
	return img, nil
}
