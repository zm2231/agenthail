package mcpoauth

import (
	"html/template"
	"net/http"
	"net/url"
)

var pages = template.Must(template.New("").Parse(`
{{define "head"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">{{if .Refresh}}<meta http-equiv="refresh" content="3;url={{.Refresh}}">{{end}}<title>Agenthail</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#222}h1{font-size:1.25rem}.code{font:600 2rem ui-monospace,monospace;letter-spacing:.2em}input,button{font:inherit;padding:.4rem .6rem}</style></head><body>{{end}}
{{define "choose"}}{{template "head" .}}<h1>{{.Client}} wants to connect to Agenthail</h1>
<p>Once you approve, it acts as one of your agent identities and is sent back to <b>{{.RedirectHost}}</b>.</p>
<form method="post" action="{{.Action}}">{{range $k, $v := .Hidden}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
<p><label>Identity <input name="identity" placeholder="@scout" required autocomplete="off" autofocus></label></p>
<p><button type="submit">Ask on my devices</button></p></form></body></html>{{end}}
{{define "waiting"}}{{template "head" .}}<h1>Approve on your device</h1>
<p>If @{{.Identity}} takes remote connections, your paired devices were asked to let {{.Client}} connect as it. Approve the ask that shows this code:</p>
<p class="code">{{.Code}}</p><p>This page moves on by itself once you answer.</p></body></html>{{end}}
{{define "message"}}{{template "head" .}}<h1>{{.Title}}</h1><p>{{.Text}}</p></body></html>{{end}}
`))

type ChoosePage struct {
	Client       string
	RedirectHost string
	Action       string
	Hidden       map[string]string
}

type WaitingPage struct {
	Client   string
	Identity string
	Code     string
	Refresh  string
}

type MessagePage struct {
	Title string
	Text  string
}

func render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pages.ExecuteTemplate(w, name, data)
}

func (p ChoosePage) Write(w http.ResponseWriter) {
	render(w, http.StatusOK, "choose", struct {
		ChoosePage
		Refresh string
	}{ChoosePage: p})
}

func (p WaitingPage) Write(w http.ResponseWriter) { render(w, http.StatusOK, "waiting", p) }

func (p MessagePage) Write(w http.ResponseWriter, status int) {
	render(w, status, "message", struct {
		MessagePage
		Refresh string
	}{MessagePage: p})
}

func RedirectHost(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	return u.Host
}

func HiddenParams(q url.Values) map[string]string {
	hidden := map[string]string{}
	for _, key := range AuthorizeParams {
		if value := q.Get(key); value != "" {
			hidden[key] = value
		}
	}
	return hidden
}
