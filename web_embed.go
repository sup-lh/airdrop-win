package airdropwin

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var embeddedDist embed.FS

func WebFS() fs.FS {
	sub, err := fs.Sub(embeddedDist, "web/dist")
	if err != nil {
		return embeddedDist
	}
	return sub
}
