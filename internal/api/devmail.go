// SPDX-License-Identifier: Apache-2.0

package api

import (
	"crypto/rand"
	"encoding/base64"
	"html/template"
	"net/http"
	"regexp"
	"strings"
)

// devMailJSON lists caught messages. It exists only in demo mode; elsewhere
// it is a plain 404 so production servers don't advertise it.
func (s *Server) devMailJSON(c *call) error {
	if !s.opt.Demo || s.opt.Catcher == nil {
		return errNotFound
	}
	return c.json(http.StatusOK, map[string]any{"messages": s.opt.Catcher.List()})
}

var linkRE = regexp.MustCompile(`https?://[^\s<>"]+`)

// linkify escapes text and turns URLs into links.
func linkify(text string) template.HTML {
	var sb strings.Builder
	last := 0
	for _, m := range linkRE.FindAllStringIndex(text, -1) {
		sb.WriteString(template.HTMLEscapeString(text[last:m[0]]))
		u := template.HTMLEscapeString(text[m[0]:m[1]])
		sb.WriteString(`<a href="` + u + `">` + u + `</a>`)
		last = m[1]
	}
	sb.WriteString(template.HTMLEscapeString(text[last:]))
	return template.HTML(sb.String()) //nolint:gosec // every part above is escaped
}

var devMailTmpl = template.Must(template.New("mail").Funcs(template.FuncMap{"linkify": linkify}).Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Dev mail · EventPass</title>
<style nonce="{{.Nonce}}">
:root{color-scheme:light dark;--bg:#f6f7f9;--card:#fff;--ink:#1a1d23;--muted:#5b6270;--line:#d9dde3;--link:#1b4ddb}
@media (prefers-color-scheme:dark){:root{--bg:#111318;--card:#1b1e25;--ink:#e8eaee;--muted:#a1a7b3;--line:#2c313b;--link:#8fb0ff}}
body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.5 system-ui,sans-serif}
main{max-width:760px;margin:0 auto;padding:24px 16px}
h1{font-size:1.4rem;margin:0 0 4px}p.note{color:var(--muted);margin:0 0 24px}
article{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:16px;margin-bottom:16px}
h2{font-size:1.05rem;margin:0}.meta{color:var(--muted);font-size:.875rem;margin:2px 0 12px}
pre{white-space:pre-wrap;word-break:break-word;font:inherit;margin:0}a{color:var(--link)}
</style></head><body><main>
<h1>Dev mail</h1>
<p class="note">Demo mode catches every email here instead of sending it. Newest first. <a href="/dev/mail">Refresh</a></p>
{{range .Messages}}<article>
<h2>{{.Subject}}</h2>
<p class="meta">To {{.To}} · {{.Time.Format "15:04:05 MST"}}</p>
<pre>{{linkify .Text}}</pre>
</article>{{else}}<p>No messages yet. Request a sign-in link and it will appear here.</p>{{end}}
</main></body></html>`))

func (s *Server) devMailPage(w http.ResponseWriter, r *http.Request) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	nonce := base64.RawStdEncoding.EncodeToString(b[:])
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = devMailTmpl.Execute(w, map[string]any{"Nonce": nonce, "Messages": s.opt.Catcher.List()})
}
