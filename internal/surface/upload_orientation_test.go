package surface

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"testing"
)

func jpegWithOrientation(t *testing.T, width, height int, orientation uint16, order binary.ByteOrder) []byte {
	t.Helper()
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	tiff := make([]byte, 8+2+12+4)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 1)
	order.PutUint16(tiff[10:], 0x0112)
	order.PutUint16(tiff[12:], 3)
	order.PutUint32(tiff[14:], 1)
	order.PutUint16(tiff[18:], orientation)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	out := append([]byte{}, plain.Bytes()[:2]...)
	out = append(out, segment...)
	out = append(out, payload...)
	return append(out, plain.Bytes()[2:]...)
}

func TestDetectImageReportsTheShapeARotatedPhotoIsShownAt(t *testing.T) {
	for _, tc := range []struct {
		name          string
		orientation   uint16
		order         binary.ByteOrder
		width, height int
	}{
		{"upright", 1, binary.BigEndian, 40, 20},
		{"upside down", 3, binary.LittleEndian, 40, 20},
		{"turned right", 6, binary.BigEndian, 20, 40},
		{"turned left, little-endian", 8, binary.LittleEndian, 20, 40},
		{"mirrored and turned", 5, binary.BigEndian, 20, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			media, width, height, ok := DetectImage(jpegWithOrientation(t, 40, 20, tc.orientation, tc.order))
			if !ok || media != "image/jpeg" || width != tc.width || height != tc.height {
				t.Fatalf("got %s %dx%d ok=%v, want image/jpeg %dx%d", media, width, height, ok, tc.width, tc.height)
			}
		})
	}
}

func TestJPEGOrientationIgnoresMissingOrBrokenExif(t *testing.T) {
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 4, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"no exif":        plain.Bytes(),
		"truncated":      jpegWithOrientation(t, 4, 2, 6, binary.BigEndian)[:12],
		"not a jpeg":     []byte("plain text"),
		"bad byte order": append([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x10}, append([]byte("Exif\x00\x00XX"), make([]byte, 6)...)...),
	} {
		if got := jpegOrientation(data); got != 1 {
			t.Errorf("%s: orientation %d, want 1", name, got)
		}
	}
}

func TestJPEGOrientationReadsPastFillBytesBeforeAMarker(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		rotated := jpegWithOrientation(t, 40, 20, 6, order)
		filled := append([]byte{0xFF, 0xD8, 0xFF, 0xFF, 0xFF}, rotated[2:]...)
		media, width, height, ok := DetectImage(filled)
		if !ok || media != "image/jpeg" || width != 20 || height != 40 {
			t.Fatalf("%v: got %s %dx%d ok=%v, want image/jpeg 20x40", order, media, width, height, ok)
		}
	}
}

func TestJPEGOrientationSurvivesArbitraryBytes(t *testing.T) {
	seed := jpegWithOrientation(t, 4, 2, 6, binary.BigEndian)
	state := uint32(1)
	for round := 0; round < 20000; round++ {
		data := append([]byte{}, seed[:min(len(seed), 2+round%len(seed))]...)
		for i := 2; i < len(data); i++ {
			state = state*1664525 + 1013904223
			if state>>28 == 0 {
				data[i] = byte(state >> 16)
			}
		}
		if got := jpegOrientation(data); got < 1 || got > 8 {
			t.Fatalf("round %d: orientation %d", round, got)
		}
	}
}
