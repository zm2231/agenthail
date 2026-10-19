package surface

import (
	"reflect"
	"testing"
)

func TestAttachedFilesSplitBackOutOfTheComposedMessage(t *testing.T) {
	uploads := Uploads{
		{ID: "upload:a/notes.txt", Name: "notes.txt", Path: "/store/a/notes.txt", MediaType: "text/plain"},
		{ID: "upload:b/shot.png", Name: "shot.png", Path: "/store/b/shot.png", MediaType: "image/png"},
		{ID: "upload:c/report.pdf", Name: "report.pdf", Path: "/store/c/report.pdf", MediaType: "application/pdf"},
	}
	for _, test := range []struct {
		name, typed string
		want        []AttachedFile
	}{
		{"typed text", "compare these", []AttachedFile{{Name: "notes.txt", Path: "/store/a/notes.txt"}, {Name: "report.pdf", Path: "/store/c/report.pdf"}}},
		{"files only", "", []AttachedFile{{Name: "notes.txt", Path: "/store/a/notes.txt"}, {Name: "report.pdf", Path: "/store/c/report.pdf"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			composed, _ := uploads.Compose(test.typed, true)
			text, files := SplitAttachedFiles(composed)
			if text != test.typed || !reflect.DeepEqual(files, test.want) {
				t.Fatalf("composed %q split into %q %+v", composed, text, files)
			}
		})
	}
	for _, typed := range []string{
		"no attachments here",
		"see below\nAttached file: /store/a/notes.txt",
		"relative\n\nAttached file: notes.txt",
		"Attached file: /store/a/notes.txt\n\nwith text after it",
	} {
		if text, files := SplitAttachedFiles(typed); text != typed || files != nil {
			t.Fatalf("%q split into %q %+v", typed, text, files)
		}
	}
}
