package uiconf

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type rgb struct{ r, g, b float64 }

func hexRGB(hex string) rgb {
	value, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return rgb{float64(value>>16&0xff) / 255, float64(value>>8&0xff) / 255, float64(value&0xff) / 255}
}

func (c rgb) hex() string {
	channel := func(v float64) int { return int(math.Round(min(max(v, 0), 1) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", channel(c.r), channel(c.g), channel(c.b))
}

func (c rgb) mixed(with rgb, amount float64) rgb {
	return rgb{c.r + (with.r-c.r)*amount, c.g + (with.g-c.g)*amount, c.b + (with.b-c.b)*amount}
}

func Readable(hex string, surfaces []string, dark bool, target float64) string {
	grounds := make([]rgb, len(surfaces))
	for i, surface := range surfaces {
		grounds[i] = hexRGB(surface)
	}
	return lift(hexRGB(hex), grounds, dark, target, 0.01).hex()
}

func lift(color rgb, grounds []rgb, dark bool, target, step float64) rgb {
	toward := rgb{0, 0, 0}
	if dark {
		toward = rgb{1, 1, 1}
	}
	shown := color
	for amount := 0.0; ; {
		// Measured on the 8-bit colour that is served, not the unrounded mix.
		if passes(hexRGB(shown.hex()), grounds, target) || amount >= 1 {
			return shown
		}
		amount = min(1, amount+step)
		shown = color.mixed(toward, amount)
	}
}

func passes(color rgb, grounds []rgb, target float64) bool {
	for _, ground := range grounds {
		if contrast(color, ground) < target {
			return false
		}
	}
	return true
}

func linear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func luminance(c rgb) float64 {
	return 0.2126*linear(c.r) + 0.7152*linear(c.g) + 0.0722*linear(c.b)
}

func contrast(a, b rgb) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}
