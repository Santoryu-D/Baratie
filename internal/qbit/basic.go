package qbit

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// basicAuth : une seedbox peut placer l'interface web derrière une
// identification HTTP du serveur, une fenêtre « identifiez-vous » avant même
// qBittorrent (calcifer chez son hébergeur, 23/10/26 : il fallait écrire le
// mot de passe dans l'adresse). Sur un 401 qui la demande, la requête est
// rejouée avec l'utilisateur et le mot de passe des champs, puis ils partent
// d'emblée. Des identifiants écrits dans l'adresse restent prioritaires : Go
// les pose lui-même avant d'arriver ici.
type basicAuth struct {
	next       http.RoundTripper
	user, pass string
	on         atomic.Bool
}

func (b *basicAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "" || (b.user == "" && b.pass == "") {
		return b.next.RoundTrip(req)
	}
	if b.on.Load() {
		r := req.Clone(req.Context())
		r.SetBasicAuth(b.user, b.pass)
		return b.next.RoundTrip(r)
	}
	resp, err := b.next.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(strings.ToLower(resp.Header.Get("WWW-Authenticate")), "basic") {
		return resp, err
	}
	r := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return resp, nil
		}
		body, gerr := req.GetBody()
		if gerr != nil {
			return resp, nil
		}
		r.Body = body
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	r.SetBasicAuth(b.user, b.pass)
	resp2, err := b.next.RoundTrip(r)
	if err == nil && resp2.StatusCode != http.StatusUnauthorized {
		b.on.Store(true)
	}
	return resp2, err
}
