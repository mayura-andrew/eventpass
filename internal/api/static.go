// SPDX-License-Identifier: Apache-2.0

package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

func writeAuditFailure(action string, err error) {
	slog.Error("audit append failed", "action", action, "err", err)
}

// static serves the single-page app. Hashed files under /assets are cached
// for a year; every other path that is not a file gets index.html so client
// routes like /admin/events/123 work on reload.
func (s *Server) static() http.Handler {
	if s.opt.Static == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "web app not built: run `npm run build` in web/", http.StatusNotFound)
		})
	}
	files := http.FileServerFS(s.opt.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(s.opt.Static, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.opt.Static, "index.html")
		if err != nil {
			http.Error(w, "web app not built: run `npm run build` in web/", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
