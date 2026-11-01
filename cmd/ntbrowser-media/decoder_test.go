package main

import (
	"image"
	"image/color"
	"testing"

	"nimbleterminal.dev/ntmediabox"
)

func TestDecoderRefusesPathNames(t *testing.T) {
	d := newVideoDecoder(t.TempDir())
	defer d.Close()
	for _, name := range []string{"", ".", "..", "../x", "/etc/passwd", "a/b", `a\b`} {
		if err := d.Load(ntmediabox.OpenRequest{Name: name}); err == nil || err.Error() != "bad media name" {
			t.Errorf("Load(%q) = %v", name, err)
		}
	}
}

func TestPixelsReusesOpaqueFrame(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	pix, w, h := pixels(img)
	if w != 2 || h != 1 || &pix[0] != &img.Pix[0] {
		t.Fatalf("pixels copied the frame: %dx%d", w, h)
	}
}

func TestPixelsConvertsOtherImages(t *testing.T) {
	img := image.NewGray(image.Rect(3, 3, 5, 4))
	img.SetGray(3, 3, color.Gray{Y: 200})
	pix, w, h := pixels(img)
	if w != 2 || h != 1 || len(pix) != 8 || pix[0] != 200 || pix[3] != 255 {
		t.Fatalf("%dx%d %v", w, h, pix)
	}
	tall := image.NewNRGBA(image.Rect(0, 0, 2, 2)).SubImage(image.Rect(1, 0, 2, 2))
	if pix, w, h := pixels(tall); w != 1 || h != 2 || len(pix) != 8 {
		t.Fatalf("subimage %dx%d %d", w, h, len(pix))
	}
}
