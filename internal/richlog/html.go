package richlog

import (
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"
	"time"
)

// WriteHTML renders the recorded session to a single self-contained HTML file.
func (r *Recorder) WriteHTML(path string) error {
	if r == nil || path == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	type metaChip struct{ Key, Value string }
	metas := make([]metaChip, 0, len(r.meta))
	for k, v := range r.meta {
		metas = append(metas, metaChip{k, v})
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Key < metas[j].Key })

	data := struct {
		Generated string
		Duration  string
		Metas     []metaChip
		Events    []*Event
		NumEvents int
	}{
		Generated: time.Now().Format("2006-01-02 15:04:05"),
		Duration:  time.Since(r.start).Round(time.Millisecond).String(),
		Metas:     metas,
		Events:    r.events,
		NumEvents: len(r.events),
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create rich log: %w", err)
	}
	defer f.Close()
	if err := pageTemplate.Execute(f, data); err != nil {
		return fmt.Errorf("render rich log: %w", err)
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"ts": func(t time.Time) string { return t.Format("15:04:05.000") },
	"summaryLine": func(ev *Event) string {
		if ev.objects != nil {
			g := ev.objects
			first, last := uint64(0), uint64(0)
			if len(g.Rows) > 0 {
				first, last = g.Rows[0].ObjectID, g.Rows[len(g.Rows)-1].ObjectID
			}
			return fmt.Sprintf("track %s · group %d · %d objects (ids %d–%d) · %s",
				g.Track, g.GroupID, len(g.Rows), first, last, humanBytes(g.TotalBytes))
		}
		return ev.Summary
	},
	"objGroup": func(ev *Event) *objectGroup { return ev.objects },
	"hbytes":   func(n int) string { return humanBytes(int64(n)) },
	"dirClass": func(d Direction) string { return "dir-" + string(d) },
	"dirLabel": func(d Direction) string {
		switch d {
		case DirSent:
			return "→ sent"
		case DirRecv:
			return "← recv"
		default:
			return "· local"
		}
	},
	"lower": strings.ToLower,
}).Parse(pageHTML))

const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>moqsub session log</title>
<style>
:root {
  --bg: #0c0f14; --card: #141a24; --card2: #101620; --border: #243044;
  --text: #e8edf5; --muted: #8b9bb4; --dim: #5c6b82;
  --accent: #5eead4; --accent-soft: rgba(94,234,212,.12);
  --sent: #60a5fa; --recv: #34d399; --local: #a78bfa; --err: #f87171;
  --mono: "SF Mono", "JetBrains Mono", ui-monospace, Menlo, monospace;
  --sans: system-ui, -apple-system, "Segoe UI", sans-serif;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text); font-family: var(--sans); line-height: 1.55; }
header { padding: 2.2rem 1.5rem 1.6rem; border-bottom: 1px solid var(--border);
  background: radial-gradient(ellipse 70% 90% at 50% -30%, var(--accent-soft), transparent); }
header h1 { margin: 0 0 .3rem; font-size: 1.35rem; letter-spacing: -.01em; }
header .sub { color: var(--muted); font-size: .85rem; margin: 0; }
.chips { display: flex; flex-wrap: wrap; gap: .4rem .6rem; margin-top: 1rem; }
.chip { font-family: var(--mono); font-size: .68rem; color: var(--muted);
  background: var(--card); border: 1px solid var(--border); border-radius: 999px; padding: .18rem .6rem; }
.chip b { color: var(--accent); font-weight: 500; }
main { max-width: 60rem; margin: 0 auto; padding: 1.5rem 1rem 4rem; }
.controls { display: flex; gap: .5rem; margin-bottom: 1rem; }
.controls button { font: inherit; font-size: .75rem; color: var(--muted); background: var(--card);
  border: 1px solid var(--border); border-radius: 6px; padding: .3rem .7rem; cursor: pointer; }
.controls button:hover { color: var(--text); border-color: var(--dim); }
.event { border: 1px solid var(--border); border-radius: 10px; background: var(--card);
  margin-bottom: .5rem; overflow: hidden; }
.event summary { display: flex; align-items: center; gap: .6rem; flex-wrap: wrap;
  padding: .5rem .8rem; cursor: pointer; list-style: none; }
.event summary::-webkit-details-marker { display: none; }
.event summary::before { content: "▸"; color: var(--dim); font-size: .7rem; transition: transform .12s; }
.event[open] summary::before { transform: rotate(90deg); }
.event .time { font-family: var(--mono); font-size: .68rem; color: var(--dim); min-width: 6.2rem; }
.badge { font-family: var(--mono); font-size: .62rem; padding: .1rem .45rem; border-radius: 4px;
  border: 1px solid; white-space: nowrap; }
.dir-sent .badge.dir { color: var(--sent); border-color: var(--sent); background: rgba(96,165,250,.08); }
.dir-received .badge.dir { color: var(--recv); border-color: var(--recv); background: rgba(52,211,153,.08); }
.dir-local .badge.dir { color: var(--local); border-color: var(--local); background: rgba(167,139,250,.08); }
.name { font-family: var(--mono); font-size: .78rem; font-weight: 600; }
.err .name { color: var(--err); }
.chan { font-family: var(--mono); font-size: .64rem; color: var(--muted);
  background: var(--card2); border: 1px solid var(--border); border-radius: 4px; padding: .08rem .4rem; }
.oneline { color: var(--muted); font-size: .76rem; flex: 1; min-width: 12rem;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.detail { border-top: 1px solid var(--border); background: var(--card2); padding: .8rem 1rem 1rem; }
.explain { font-size: .82rem; color: var(--text); max-width: 46rem; margin: 0 0 .7rem; }
.speclink { font-size: .74rem; }
.speclink a { color: var(--accent); }
table.fields { border-collapse: collapse; font-family: var(--mono); font-size: .72rem; margin: .5rem 0; }
table.fields td { border: 1px solid var(--border); padding: .25rem .6rem; }
table.fields td.k { color: var(--muted); }
table.fields td.v { color: var(--accent); }
pre.body { font-family: var(--mono); font-size: .7rem; background: #0a0d12; border: 1px solid var(--border);
  border-radius: 8px; padding: .7rem .9rem; overflow-x: auto; color: #a8b8d0; }
table.objects { border-collapse: collapse; font-family: var(--mono); font-size: .7rem; margin-top: .5rem; }
table.objects th, table.objects td { border: 1px solid var(--border); padding: .2rem .6rem; text-align: right; }
table.objects th { color: var(--muted); font-weight: 500; background: var(--card); }
table.objects td:first-child { color: var(--dim); }
footer { text-align: center; color: var(--dim); font-size: .72rem; padding: 1.5rem; }
</style>
</head>
<body>
<header>
  <h1>moqsub session log</h1>
  <p class="sub">Media over QUIC Transport (draft-18) — every message exchanged, in order. Click a row to expand.</p>
  <div class="chips">
    {{range .Metas}}<span class="chip">{{.Key}} <b>{{.Value}}</b></span>{{end}}
    <span class="chip">events <b>{{.NumEvents}}</b></span>
    <span class="chip">duration <b>{{.Duration}}</b></span>
    <span class="chip">generated <b>{{.Generated}}</b></span>
  </div>
</header>
<main>
  <div class="controls">
    <button onclick="document.querySelectorAll('details.event').forEach(d=>d.open=true)">Expand all</button>
    <button onclick="document.querySelectorAll('details.event').forEach(d=>d.open=false)">Collapse all</button>
  </div>
  {{range .Events}}
  <details class="event {{dirClass .Dir}}{{if eq .Name "REQUEST_ERROR"}} err{{end}}">
    <summary>
      <span class="time">{{ts .Time}}</span>
      <span class="badge dir">{{dirLabel .Dir}}</span>
      <span class="name">{{.Name}}</span>
      {{if .Channel}}<span class="chan">{{.Channel}}</span>{{end}}
      <span class="oneline">{{summaryLine .}}</span>
    </summary>
    <div class="detail">
      {{if .Explain}}<p class="explain">{{.Explain}}</p>{{end}}
      {{if .Spec.Section}}<p class="speclink">Spec: <a href="{{.Spec.URL}}" target="_blank" rel="noopener">draft-ietf-moq-transport-18 §{{.Spec.Section}}{{if .Spec.Label}} — {{.Spec.Label}}{{end}}</a></p>{{end}}
      {{if .Fields}}
      <table class="fields">
        {{range .Fields}}<tr><td class="k">{{.Key}}</td><td class="v">{{.Value}}</td></tr>{{end}}
      </table>
      {{end}}
      {{if .Body}}<pre class="body">{{.Body}}</pre>{{end}}
      {{with objGroup .}}
      <table class="objects">
        <tr><th>time</th><th>object_id</th><th>payload</th></tr>
        {{range .Rows}}<tr><td>{{ts .Time}}</td><td>{{.ObjectID}}</td><td>{{hbytes .Bytes}}</td></tr>{{end}}
      </table>
      {{end}}
    </div>
  </details>
  {{end}}
</main>
<footer>Generated by moqsub · spec links point to draft-ietf-moq-transport-18 on datatracker.ietf.org</footer>
</body>
</html>
`
