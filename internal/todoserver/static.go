package todoserver

import (
	"embed"
	"io/fs"
)

//go:embed web
var web embed.FS

// Web is the phone app's files, served at the server's root.
func Web() fs.FS {
	sub, err := fs.Sub(web, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
