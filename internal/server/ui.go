package server

import (
	"io/fs"
	"net/http"
	"strings"
)

// SPAHandler returns an http.Handler that serves static files from the given
// filesystem and falls back to index.html for client-side routing.
func SPAHandler(embedded fs.FS) http.Handler {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("web: could not create sub-filesystem: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Try to open the file. If it exists, serve it directly.
		f, err := dist.Open(path)
		if err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for client-side routing.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
