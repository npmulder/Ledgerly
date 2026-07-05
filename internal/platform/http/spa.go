package httpserver

import (
	"io/fs"
	nethttp "net/http"
	"path"
	"regexp"
	"strings"
)

const (
	cacheControlImmutable = "public, max-age=31536000, immutable"
	cacheControlNoCache   = "no-cache"
)

var hashedAssetPattern = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.`)

func spaHandler(assets fs.FS) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method != nethttp.MethodGet && r.Method != nethttp.MethodHead {
			nethttp.NotFound(w, r)
			return
		}

		requestPath := path.Clean("/" + r.URL.Path)
		if requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") {
			nethttp.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(requestPath, "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if !assetExists(assets, name) {
			name = "index.html"
			if !assetExists(assets, name) {
				nethttp.NotFound(w, r)
				return
			}
		}

		setAssetCacheHeaders(w, name)
		nethttp.ServeFileFS(w, r, assets, name)
	}
}

func assetExists(assets fs.FS, name string) bool {
	info, err := fs.Stat(assets, name)
	return err == nil && !info.IsDir()
}

func setAssetCacheHeaders(w nethttp.ResponseWriter, name string) {
	if name == "index.html" {
		w.Header().Set("Cache-Control", cacheControlNoCache)
		return
	}
	if hashedAssetPattern.MatchString(path.Base(name)) {
		w.Header().Set("Cache-Control", cacheControlImmutable)
	}
}
