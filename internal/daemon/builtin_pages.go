package daemon

import (
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/extension"
)

type builtinPage struct {
	id      string
	handler func(*Daemon) http.Handler
}

var builtinPages = []builtinPage{
	{id: extension.Ledger, handler: (*Daemon).ledgerPage},
}

func builtinPageFor(id string) (builtinPage, bool) {
	for _, page := range builtinPages {
		if page.id == id {
			return page, true
		}
	}
	return builtinPage{}, false
}

func (d *Daemon) pagePort(next http.Handler) http.Handler {
	return d.extensionPortHandler(func(w http.ResponseWriter, r *http.Request, dashboard *dashboardServer) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", extensionPageCSP+" "+strings.Join(d.shellOrigins(r, dashboard), " "))
		next.ServeHTTP(w, r)
	})
}
