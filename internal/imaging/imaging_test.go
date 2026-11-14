package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/smarty/gunit"
	"github.com/smarty/gunit/assert/should"
)

func TestImagingFixture(t *testing.T) {
	gunit.Run(new(ImagingFixture), t)
}

type ImagingFixture struct {
	*gunit.Fixture
}

var red = color.RGBA{255, 0, 0, 255}

// twoByOne is a 2x1 image: red on the left, blue on the right.
func twoByOne() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, red)
	img.SetRGBA(1, 0, color.RGBA{0, 0, 255, 255})
	return img
}

func (this *ImagingFixture) TestOrientRotateClockwise() {
	out := Orient(twoByOne(), 6)
	this.So(out.Bounds().Dx(), should.Equal, 1)
	this.So(out.Bounds().Dy(), should.Equal, 2)
	this.So(out.RGBAAt(0, 0), should.Equal, red) // left edge rotates to the top
}

func (this *ImagingFixture) TestOrientRotateCounterClockwise() {
	out := Orient(twoByOne(), 8)
	this.So(out.Bounds().Dy(), should.Equal, 2)
	this.So(out.RGBAAt(0, 1), should.Equal, red) // left edge rotates to the bottom
}

func (this *ImagingFixture) TestOrientMirror() {
	out := Orient(twoByOne(), 2)
	this.So(out.RGBAAt(1, 0), should.Equal, red)
}

func (this *ImagingFixture) TestNormalizeShrinksAndOutputsJPEG() {
	var buf bytes.Buffer
	this.So(png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 400, 200))), should.BeNil)

	out, err := Normalize(buf.Bytes(), 100)
	this.So(err, should.BeNil)
	img, err := jpeg.Decode(bytes.NewReader(out))
	this.So(err, should.BeNil)
	this.So(img.Bounds().Dx(), should.Equal, 100)
	this.So(img.Bounds().Dy(), should.Equal, 50)
}

func (this *ImagingFixture) TestNormalizeRejectsGarbage() {
	_, err := Normalize([]byte("not an image"), 100)
	this.So(err, should.NotBeNil)
}

func (this *ImagingFixture) TestReadsExifOrientation() {
	this.So(jpegOrientation(jpegWithOrientation(6, false)), should.Equal, 6)
	this.So(jpegOrientation(jpegWithOrientation(3, true)), should.Equal, 3)
	this.So(jpegOrientation([]byte{0xFF, 0xD8, 0xFF, 0xDA}), should.Equal, 1)
}

// jpegWithOrientation builds the start of a JPEG with an APP1 EXIF segment.
func jpegWithOrientation(orientation byte, bigEndian bool) []byte {
	var tiff []byte
	if bigEndian {
		tiff = []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, orientation, 0, 0}
	} else {
		tiff = []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 0x01, 3, 0, 1, 0, 0, 0, orientation, 0, 0, 0}
	}
	segment := append([]byte("Exif\x00\x00"), tiff...)
	size := len(segment) + 2
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte(size >> 8), byte(size)}
	out = append(out, segment...)
	return append(out, 0xFF, 0xDA)
}
