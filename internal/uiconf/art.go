package uiconf

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sync"
)

const artDir = "art"

//go:embed art/*.jpg
var art embed.FS

var artTags = sync.OnceValue(func() map[string]string {
	tags := map[string]string{}
	entries, _ := art.ReadDir(artDir)
	for _, entry := range entries {
		data, err := art.ReadFile(artDir + "/" + entry.Name())
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		tags[artDir+"/"+entry.Name()] = hex.EncodeToString(sum[:8])
	}
	return tags
})

func openArt(rel string) (fs.File, string, error) {
	file, err := art.Open(rel)
	return file, artTags()[rel], err
}
