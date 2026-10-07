// Resizes the Threavia mark into the sizes the client serves.
//
// No dependency: downscaling is an area average over the source footprint of
// each destination pixel, done in premultiplied space so the transparent
// margins do not bleed dark edges into the glow.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

func main() {
	source, target := os.Args[1], os.Args[2]

	file, err := os.Open(source)
	if err != nil {
		panic(err)
	}
	decoded, err := png.Decode(file)
	if err != nil {
		panic(err)
	}
	_ = file.Close()

	src := image.NewRGBA(decoded.Bounds())
	draw.Draw(src, src.Bounds(), decoded, decoded.Bounds().Min, draw.Src)

	content := bounds(src)
	fmt.Printf("source %v, content %v\n", src.Bounds().Size(), content.Size())
	mark := src.SubImage(content).(*image.RGBA)

	// The mark as it is: wide, for the sidebar beside the name.
	for _, height := range []int{128, 64, 40} {
		width := content.Dx() * height / content.Dy()
		write(filepath.Join(target, fmt.Sprintf("mark-%dh.png", height)), scale(mark, width, height))
	}

	// Square, for every place that wants an icon: the mark centred with a
	// margin, so it stays itself at sixteen pixels.
	for _, size := range []int{512, 192, 180, 64, 48, 32, 16} {
		write(filepath.Join(target, fmt.Sprintf("icon-%d.png", size)), square(mark, size))
	}
}

// bounds is the smallest rectangle holding every pixel that is not transparent.
func bounds(img *image.RGBA) image.Rectangle {
	found := image.Rectangle{Min: image.Point{X: 1 << 30, Y: 1 << 30}}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if img.RGBAAt(x, y).A < 8 {
				continue
			}
			found.Min.X = min(found.Min.X, x)
			found.Min.Y = min(found.Min.Y, y)
			found.Max.X = max(found.Max.X, x+1)
			found.Max.Y = max(found.Max.Y, y+1)
		}
	}
	return found
}

// scale shrinks an image by averaging the source pixels under each destination
// pixel. Premultiplied components average linearly, which is why the source is
// held as RGBA rather than NRGBA.
func scale(src *image.RGBA, width, height int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	sw, sh := src.Rect.Dx(), src.Rect.Dy()

	for y := 0; y < height; y++ {
		y0 := src.Rect.Min.Y + y*sh/height
		y1 := max(src.Rect.Min.Y+(y+1)*sh/height, y0+1)
		for x := 0; x < width; x++ {
			x0 := src.Rect.Min.X + x*sw/width
			x1 := max(src.Rect.Min.X+(x+1)*sw/width, x0+1)

			var r, g, b, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					p := src.RGBAAt(sx, sy)
					r += uint64(p.R)
					g += uint64(p.G)
					b += uint64(p.B)
					a += uint64(p.A)
					n++
				}
			}
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: uint8(a / n),
			})
		}
	}
	return out
}

// square fits the mark into a square canvas, centred, with a margin so that a
// browser tab does not clip the glow.
func square(mark *image.RGBA, size int) *image.RGBA {
	// As wide as the slot allows, short of the very edge: the mark is twice as
	// wide as it is tall, so width is what it has to spend.
	const fill = 0.94

	width := int(float64(size) * fill)
	height := mark.Rect.Dy() * width / mark.Rect.Dx()

	out := backdrop(size)
	scaled := scale(mark, width, height)
	at := image.Pt((size-width)/2, (size-height)/2)
	draw.Draw(out, image.Rectangle{Min: at, Max: at.Add(image.Pt(width, height))},
		scaled, image.Point{}, draw.Src)
	return out
}

func write(path string, img image.Image) {
	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	if err := png.Encode(file, img); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	info, _ := os.Stat(path)
	fmt.Printf("  %-18s %4d x %-4d %5d bytes\n", filepath.Base(path),
		img.Bounds().Dx(), img.Bounds().Dy(), info.Size())
}
