// Copyright (c) 2026 Visvasity LLC

package servers

import (
	"bytes"
	"html/template"
	"net"
	"net/http"
)

// welcomeData is the template model for the welcome page. A relay link is built
// as <Scheme>://<name>.<Base><Port>/ from the request, so it points back at the
// host the client actually used — including through a per-user delegation, where
// X-Forwarded-* reflect the root daemon's scheme/host/port (e.g. Base becomes
// "alice.localhost", giving app.alice.localhost).
type welcomeData struct {
	Scheme  string
	Base    string
	Port    string
	Entries []ListEntry
}

// serveWelcome renders the apex (localhost) welcome page: a listing of the
// daemon's current relays, delegations, and socket-directory backends. It renders
// into a buffer first so a template error never leaves a partial response.
func (r *Relay) serveWelcome(w http.ResponseWriter, req *http.Request) error {
	// Prefer X-Forwarded-* so links are correct when this page is reached through
	// a per-user delegation on the root daemon; fall back to the direct request.
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	if p := req.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := req.Host
	if h := req.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}

	// Split off the port, keeping it in links unless it is the standard one for
	// the scheme, so links work on 1080/1443 or any custom port.
	base, port := host, ""
	if h, p, err := net.SplitHostPort(host); err == nil {
		base = h
		if p != "" && !(scheme == "http" && p == "80") && !(scheme == "https" && p == "443") {
			port = ":" + p
		}
	}
	data := welcomeData{Scheme: scheme, Base: base, Port: port, Entries: r.listEntries()}

	var buf bytes.Buffer
	if err := welcomeTemplate.Execute(&buf, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
	return nil
}

var welcomeTemplate = template.Must(template.New("welcome").Parse(welcomeHTML))

const welcomeHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>localrelay</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif;
         max-width: 48rem; margin: 3rem auto; padding: 0 1rem; line-height: 1.5; }
  h1 { margin: 0 0 .25rem; font-size: 1.6rem; }
  p.sub { margin: 0 0 1.5rem; opacity: .7; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: .5rem .6rem; border-bottom: 1px solid #8884; }
  th { font-size: .75rem; text-transform: uppercase; letter-spacing: .04em; opacity: .7; }
  td.target { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9rem; }
  .kind { font-size: .75rem; padding: .1rem .45rem; border-radius: 1rem; border: 1px solid #8886; }
  .empty { opacity: .7; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .status { white-space: nowrap; font-size: .85rem; }
  .status.up { color: #1a7f37; }
  .status.down { color: #cf222e; }
  .status.unknown { opacity: .6; }
  @media (prefers-color-scheme: dark) {
    .status.up { color: #3fb950; }
    .status.down { color: #f85149; }
  }
</style>
</head>
<body>
<h1>localrelay</h1>
<p class="sub">{{len .Entries}} route{{if ne (len .Entries) 1}}s{{end}} registered</p>
{{if .Entries}}
<table>
  <thead><tr><th>Name</th><th>Kind</th><th>Target</th><th>Status</th></tr></thead>
  <tbody>
  {{range .Entries}}<tr>
    <td><a href="{{$.Scheme}}://{{.Name}}.{{$.Base}}{{$.Port}}/">{{if eq .Kind "user"}}*.{{end}}{{.Name}}.{{$.Base}}</a></td>
    <td><span class="kind">{{.Kind}}</span></td>
    <td class="target">{{.Target}}</td>
    <td class="status {{.Status}}">{{if eq .Status "up"}}● up{{else if eq .Status "down"}}● down{{else}}● …{{end}}</td>
  </tr>
  {{end}}</tbody>
</table>
{{else}}
<p class="empty">No relays registered yet. Add one with <code>localrelay add &lt;name&gt; &lt;target&gt;</code>.</p>
{{end}}
</body>
</html>
`
