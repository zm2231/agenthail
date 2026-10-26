package uiconf

import (
	"reflect"
	"testing"
)

const masked = "version = 1\nbackground@mac = image: backgrounds/a.jpg\nbackground@device:studio = art: dawn\nbackground@phone = art: tide\ntexture@mac = none\n"

func TestSetForClearsOnlyTheLinesThatMaskTheWritingDevice(t *testing.T) {
	studio := Device{Class: "mac", Name: "studio"}
	cases := []struct {
		name    string
		device  Device
		line    string
		want    string
		cleared []string
	}{
		{"all devices from a Mac", studio, "background = art: caustics", "version = 1\nbackground@phone = art: tide\ntexture@mac = none\nbackground = art: caustics\n", []string{"background@mac", "background@device:studio"}},
		{"all Macs", studio, "background@mac = art: field", "version = 1\nbackground@mac = art: field\nbackground@phone = art: tide\ntexture@mac = none\n", []string{"background@device:studio"}},
		{"this Mac", studio, "background@device:studio = art: field", "version = 1\nbackground@mac = image: backgrounds/a.jpg\nbackground@device:studio = art: field\nbackground@phone = art: tide\ntexture@mac = none\n", nil},
		{"all devices from a phone", Device{Class: "phone", Name: "pocket"}, "background = art: caustics", "version = 1\nbackground@mac = image: backgrounds/a.jpg\nbackground@device:studio = art: dawn\ntexture@mac = none\nbackground = art: caustics\n", []string{"background@phone"}},
		{"another device", studio, "background@phone = art: field", "version = 1\nbackground@mac = image: backgrounds/a.jpg\nbackground@device:studio = art: dawn\nbackground@phone = art: field\ntexture@mac = none\n", nil},
	}
	for _, c := range cases {
		text, cleared := SetFor(masked, c.device, mustAssign(t, c.line))
		if text != c.want || !reflect.DeepEqual(cleared, c.cleared) {
			t.Errorf("%s:\ntext = %q\nwant   %q\ncleared = %v, want %v", c.name, text, c.want, cleared, c.cleared)
		}
		chosen, _ := Parse(text).Chosen(c.device)
		if want := mustAssign(t, c.line); c.device.rank(want.Device) > 0 && chosen["background"] != want.Value {
			t.Errorf("%s: %+v sees %q, want %q", c.name, c.device, chosen["background"], want.Value)
		}
	}
}

func TestUnsetForClearsTheLinesThatMaskTheReset(t *testing.T) {
	studio := Device{Class: "mac", Name: "studio"}
	text, cleared := UnsetFor(masked, studio, "background", "texture")
	if want := "version = 1\nbackground@phone = art: tide\n"; text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
	if want := []string{"background@mac", "background@device:studio", "texture@mac"}; !reflect.DeepEqual(cleared, want) {
		t.Fatalf("cleared = %v, want %v", cleared, want)
	}
	text, cleared = UnsetFor(masked, studio, "background@device:studio", "background@mac")
	if want := "version = 1\nbackground@phone = art: tide\ntexture@mac = none\n"; text != want || len(cleared) != 0 {
		t.Fatalf("named unset: text = %q cleared = %v", text, cleared)
	}
}
