package uiconf

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseReportsBadLinesAndKeepsTheRest(t *testing.T) {
	text := strings.Join([]string{
		"# comment",
		"theme = dark",
		"glass.blur = 90",
		"texture.spacing = big",
		"nonsense = 1",
		"no equals sign",
		"theme.accent = #4DA3F5  # house blue",
		"glass@watch = off",
	}, "\n")
	parsed := Parse(text)
	got := map[int]string{}
	for _, p := range parsed.Problems {
		got[p.Line] = p.Message
	}
	want := map[int]string{
		3: "glass.blur must be from 0 to 64; using 64",
		4: "texture.spacing must be a number from 2 to 8",
		5: "unknown key nonsense",
		6: "expected key = value",
		8: "device must be mac, phone, tablet, web or device:<name>",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("problems = %v, want %v", got, want)
	}
	values := parsed.Resolve(Device{Class: "mac"})
	if values["theme"] != "dark" || values["glass.blur"] != 64.0 || values["texture.spacing"] != 3.0 || values["theme.accent"] != "#4da3f5" {
		t.Fatalf("resolved = theme %v blur %v spacing %v accent %v", values["theme"], values["glass.blur"], values["texture.spacing"], values["theme.accent"])
	}
}

func TestChosenNamesTheDeviceAValueComesFrom(t *testing.T) {
	parsed := Parse("glass.blur = 20\nglass.blur@device:studio = 40\ntexture@mac = grain\nbackground = art: dawn\n")
	values, overrides := parsed.Chosen(Device{Class: "mac", Name: "studio"})
	if values["glass.blur"] != "40" || values["texture"] != "grain" || values["background"] != "art: dawn" || values["theme"] != "system" {
		t.Fatalf("chosen = %v", values)
	}
	if !reflect.DeepEqual(overrides, map[string]string{"glass.blur": "device:studio", "texture": "mac"}) {
		t.Fatalf("overrides = %v", overrides)
	}
}

func TestResolvePrefersTheMostSpecificDevice(t *testing.T) {
	parsed := Parse("texture = scanlines\ntexture@device:studio = grain\ntexture@phone = none\n")
	cases := []struct {
		device Device
		want   string
	}{
		{Device{Class: "mac"}, "scanlines"},
		{Device{Class: "phone"}, "none"},
		{Device{Class: "mac", Name: "studio"}, "grain"},
		{Device{Class: "phone", Name: "studio"}, "grain"},
	}
	for _, c := range cases {
		if got := parsed.Resolve(c.device)["texture"]; got != c.want {
			t.Errorf("%+v: texture = %v, want %s", c.device, got, c.want)
		}
	}
}

func TestResolveFillsDefaultsAndExpandsPresets(t *testing.T) {
	values := Parse("").Resolve(Device{Class: "mac"})
	for _, key := range Keys {
		if _, ok := values[key.Name]; !ok {
			t.Fatalf("missing default for %s", key.Name)
		}
	}
	if values["background"] != "image: art/tide-dark.jpg" || values["background.light"] != "image: art/tide-light.jpg" {
		t.Fatalf("default background = %v, light %v", values["background"], values["background.light"])
	}
	slate := Parse("background = gradient: slate").Resolve(Device{Class: "mac"})
	if slate["background"] != Presets["slate"].Dark || slate["background.light"] != Presets["slate"].Light || Presets["slate"].Dark == Presets["slate"].Light {
		t.Fatalf("slate = %v, light %v", slate["background"], slate["background.light"])
	}
	custom := Parse("background = mesh: 2x2, #000000, #111111, #222222, #333333").Resolve(Device{Class: "mac"})
	if custom["background"] != custom["background.light"] {
		t.Fatalf("a custom background is drawn the same in both appearances: %v, %v", custom["background"], custom["background.light"])
	}
	if got := values["statusbar"]; !reflect.DeepEqual(got, []string{"usage", "working", "needs-you", "hosts", "terminal"}) {
		t.Fatalf("statusbar = %v", got)
	}
	if got := Parse("statusbar = none").Resolve(Device{Class: "mac"})["statusbar"]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("statusbar none = %v", got)
	}
}

func TestBackgroundValues(t *testing.T) {
	good := []string{
		"none", "desktop", "image: backgrounds/a1.png", "gradient: dusk", "art: tide", "art: field",
		"gradient: 160deg, #1b1035 0%, #e0763a 100%",
		"mesh: 2x2, #000000, #111111, #222222, #333333",
	}
	for _, value := range good {
		if _, problem := checkBackground(value); problem != "" {
			t.Errorf("%q: %s", value, problem)
		}
	}
	bad := []string{"image:", "image: a.gif", "gradient: sunset", "gradient: aurora", "art: sunset", "art:", "gradient: 160deg, red 0%, blue 100%", "mesh: 2x2, #000000", "mesh: 9x9, #000000", "wallpaper"}
	for _, value := range bad {
		if _, problem := checkBackground(value); problem == "" {
			t.Errorf("%q accepted", value)
		}
	}
	for name, preset := range Presets {
		for _, mesh := range []string{preset.Dark, preset.Light} {
			if problem := checkMesh(strings.TrimPrefix(mesh, "mesh: ")); problem != "" {
				t.Errorf("preset %s: %s", name, problem)
			}
		}
	}
	if len(Presets) != len(PresetNames) {
		t.Fatalf("%d presets, %d names", len(Presets), len(PresetNames))
	}
}

func TestSetKeepsCommentsAndOrderAndUnsetRemoves(t *testing.T) {
	text := "# mine\ntheme = dark  # night owl\ntexture.spacing = 4\n"
	theme, err := ParseAssignment("theme = light")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := ParseAssignment("texture@phone = none")
	if err != nil {
		t.Fatal(err)
	}
	got := Set(text, theme, phone)
	want := "# mine\ntheme = light  # night owl\ntexture.spacing = 4\ntexture@phone = none\n"
	if got != want {
		t.Fatalf("Set = %q, want %q", got, want)
	}
	if got := Unset(want, "texture", "phone"); got != "# mine\ntheme = light  # night owl\ntexture.spacing = 4\n" {
		t.Fatalf("Unset = %q", got)
	}
	if _, err := ParseAssignment("glass.blur = wide"); err == nil {
		t.Fatal("bad value accepted")
	}
	if _, err := ParseAssignment("glass.blur = 99"); err == nil {
		t.Fatal("out-of-range value accepted by ui set")
	}
}

func TestShowDefaultsParsesCleanly(t *testing.T) {
	for _, docs := range []bool{false, true} {
		if problems := Parse(ShowDefaults(docs)).Problems; len(problems) != 0 {
			t.Fatalf("docs=%v: %v", docs, problems)
		}
	}
}

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ui"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreVersionsEveryChange(t *testing.T) {
	s := openStore(t)
	state, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Text, "version = 1") || state.Rev == "" {
		t.Fatalf("initial state = %+v", state)
	}
	theme, _ := ParseAssignment("theme = light")
	if _, changed, err := s.Update("@fixer", "Light theme", func(text string) string { return Set(text, theme) }); err != nil || !changed {
		t.Fatalf("update: changed=%v err=%v", changed, err)
	}
	if _, changed, err := s.Update("@fixer", "Light theme again", func(text string) string { return Set(text, theme) }); err != nil || changed {
		t.Fatalf("no-op update: changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(s.Path(), []byte(Set(mustRead(t, s.Path()), mustAssign(t, "texture.spacing = 5"))), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Current(); err != nil {
		t.Fatal(err)
	}
	log, err := s.Log(10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range log {
		got = append(got, e.Author+": "+e.Message)
	}
	want := []string{"person: Edited by hand", "@fixer: Light theme", "agenthail: Create ui.conf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	diff, err := s.Diff(log[1].Rev)
	if err != nil || !strings.Contains(diff, "+theme = light") {
		t.Fatalf("diff = %q, %v", diff, err)
	}
}

func TestUndoRevertsAndRefusesConflicts(t *testing.T) {
	s := openStore(t)
	update := func(author, message, line string) {
		t.Helper()
		if _, _, err := s.Update(author, message, func(text string) string { return Set(text, mustAssign(t, line)) }); err != nil {
			t.Fatal(err)
		}
	}
	update("@fixer", "Dark", "theme = dark")
	update(Person, "Bigger text", "texture.spacing = 6")
	state, entry, err := s.Undo("", Person)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(state.Text, "texture.spacing") || !strings.Contains(state.Text, "theme = dark") || entry.Message != "Undo Bigger text" {
		t.Fatalf("after undo: %q %+v", state.Text, entry)
	}
	update("@fixer", "Light", "theme = light")
	log, _ := s.Log(10)
	dark := ""
	for _, e := range log {
		if e.Message == "Dark" {
			dark = e.Rev
		}
	}
	if _, _, err := s.Undo(dark, Person); err == nil || !strings.Contains(err.Error(), "can't undo \"Dark\" cleanly") {
		t.Fatalf("conflicting undo err = %v", err)
	}
	state, err = s.Current()
	if err != nil || !strings.Contains(state.Text, "theme = light") {
		t.Fatalf("state after refused undo: %q %v", state.Text, err)
	}
	if _, _, err := s.Undo("--help", Person); err == nil {
		t.Fatal("option-like revision accepted")
	}
}

func TestBackgroundsAreImportedAndOnlyThoseServed(t *testing.T) {
	s := openStore(t)
	source := filepath.Join(t.TempDir(), "wall.PNG")
	if err := os.WriteFile(source, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := s.ImportBackground(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Update(Person, "Wallpaper", func(text string) string { return Set(text, mustAssign(t, "background = image: "+rel)) }); err != nil {
		t.Fatal(err)
	}
	files, err := s.git("ls-files")
	if err != nil || !strings.Contains(files, rel) {
		t.Fatalf("committed files = %q, %v", files, err)
	}
	asset, err := s.OpenAsset(rel)
	if err != nil {
		t.Fatal(err)
	}
	served, _ := io.ReadAll(asset)
	asset.Close()
	if string(served) != "png bytes" || asset.Tag != "" {
		t.Fatalf("asset = %q tag %q", served, asset.Tag)
	}
	for _, name := range ArtNames {
		for _, variant := range []string{"dark", "light"} {
			art, err := s.OpenAsset("art/" + name + "-" + variant + ".jpg")
			if err != nil {
				t.Fatalf("built-in %s %s: %v", name, variant, err)
			}
			head := make([]byte, 3)
			_, _ = io.ReadFull(art, head)
			art.Close()
			if string(head) != "\xff\xd8\xff" || art.Tag == "" {
				t.Fatalf("built-in %s %s is not a JPEG: %x", name, variant, head)
			}
		}
	}
	for _, bad := range []string{"ui.conf", "../secret.png", "/etc/x.png", "backgrounds/../../x.png", "backgrounds/a.txt", "art/../ui.conf", "art/missing.jpg"} {
		if asset, err := s.OpenAsset(bad); err == nil {
			asset.Close()
			t.Errorf("served %q", bad)
		}
	}
	uploaded, err := s.ImportBackgroundData("photo.JPG", strings.NewReader("jpeg bytes"))
	if err != nil || !strings.HasPrefix(uploaded, "backgrounds/") || !strings.HasSuffix(uploaded, ".jpg") {
		t.Fatalf("upload = %q, %v", uploaded, err)
	}
	if _, err := s.ImportBackgroundData("photo.gif", strings.NewReader("gif")); err == nil {
		t.Fatal("uploaded a gif")
	}
	if _, err := s.ImportBackgroundData("photo.png", strings.NewReader("")); err == nil {
		t.Fatal("uploaded an empty image")
	}
	if _, err := s.ImportBackground(filepath.Join(t.TempDir(), "notes.txt")); err == nil {
		t.Fatal("imported a non-image")
	}
}

func mustAssign(t *testing.T, line string) Assignment {
	t.Helper()
	a, err := ParseAssignment(line)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
