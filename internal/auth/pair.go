package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	codeTTL      = 5 * time.Minute
	failWindow   = 5 * time.Minute
	maxFailures  = 10
	codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford base32: no I, L, O, U
	maxNameLen   = 64
)

type liveCode struct {
	code    string // 8 characters, no hyphen
	expires time.Time
}

func (a *Auth) mintCode() (string, time.Time, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	for i, b := range raw {
		raw[i] = codeAlphabet[b&31]
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.code = liveCode{code: string(raw), expires: a.now().Add(codeTTL)}
	return string(raw[:4]) + "-" + string(raw[4:]), a.code.expires, nil
}

var errTooMany = fmt.Errorf("too many attempts")

// redeem spends the live code if typed matches it. After 10 failures within 5 minutes every attempt is refused.
func (a *Auth) redeem(typed string) (ok bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	recent := a.fails[:0]
	for _, t := range a.fails {
		if now.Sub(t) < failWindow {
			recent = append(recent, t)
		}
	}
	a.fails = recent
	if len(a.fails) >= maxFailures {
		return false, errTooMany
	}
	typed = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(typed))
	if a.code.code != "" && !now.After(a.code.expires) && subtle.ConstantTimeCompare([]byte(typed), []byte(a.code.code)) == 1 {
		a.code = liveCode{}
		return true, nil
	}
	a.fails = append(a.fails, now)
	return false, nil
}

func (a *Auth) servePair(w http.ResponseWriter, r *http.Request, rt route) {
	api := r.URL.Path == "/api/v1/pair"
	if rt.kind != pathLAN && rt.kind != pathTailnet {
		pairFail(w, api, http.StatusForbidden, "open this from the device you are pairing")
		return
	}
	var code, name string
	switch {
	case api:
		var body struct{ Code, Name string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			pairFail(w, true, http.StatusBadRequest, "expected JSON {\"code\": \"…\"}")
			return
		}
		code, name = body.Code, body.Name
	case r.Method == http.MethodGet:
		code = r.URL.Query().Get("code")
		if code == "" {
			writeEntryPage(w, http.StatusOK, "")
			return
		}
	case r.Method == http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil {
			writeEntryPage(w, http.StatusBadRequest, "That didn't come through. Try again.")
			return
		}
		code = r.PostForm.Get("code")
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ok, err := a.redeem(code)
	if err != nil {
		w.Header().Set("Retry-After", fmt.Sprint(int(failWindow.Seconds())))
		pairFail(w, api, http.StatusTooManyRequests, "too many wrong codes; wait a few minutes")
		return
	}
	if !ok {
		pairFail(w, api, http.StatusBadRequest, "that code is wrong, used or expired")
		return
	}
	name = cleanName(name)
	if name == "" {
		name = guessName(r.UserAgent())
	}
	d, token, err := a.add(name, rt.login)
	if err != nil {
		pairFail(w, api, http.StatusInternalServerError, "could not save the device")
		return
	}
	if api {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "device": map[string]string{"id": d.ID, "name": d.Name}})
		return
	}
	a.setCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

func pairFail(w http.ResponseWriter, api bool, status int, msg string) {
	if api {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	writeEntryPage(w, status, msg)
}

func writeEntryPage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	errLine := ""
	if msg != "" {
		errLine = `<p role="alert" style="color: #b00020">` + html.EscapeString(msg) + `</p>`
	}
	fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Pair with Admiral</title></head>
<body style="font-family: system-ui, sans-serif; margin: 3rem 1.5rem; max-width: 24rem">
<h1 style="font-size: 1.25rem">Pair this device with Admiral</h1>
<p>On the Mac, run <code>admiral pair</code> and type the code it shows.</p>
%s
<form method="post" action="/pair">
<input name="code" autocomplete="off" autocapitalize="characters" autocorrect="off" spellcheck="false" placeholder="XXXX-XXXX" required autofocus
 style="font-size: 1.5rem; width: 100%%; padding: .5rem; box-sizing: border-box; letter-spacing: .1em">
<button type="submit" style="font-size: 1.1rem; margin-top: 1rem; padding: .6rem 1.2rem">Pair</button>
</form></body></html>
`, errLine)
}

func cleanName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxNameLen {
		name = string([]rune(name)[:maxNameLen])
	}
	return name
}

// guessName reads the User-Agent. iPadOS Safari sends a Mac one, so an iPad shows as a Mac.
func guessName(ua string) string {
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgA/") || strings.Contains(ua, "EdgiOS/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	platform := ""
	switch {
	case strings.Contains(ua, "Android"):
		platform = "Android"
	case strings.Contains(ua, "iPhone"):
		platform = "iPhone"
	case strings.Contains(ua, "iPad"):
		platform = "iPad"
	case strings.Contains(ua, "Macintosh"):
		platform = "Mac"
	case strings.Contains(ua, "Windows"):
		platform = "Windows"
	case strings.Contains(ua, "CrOS"):
		platform = "ChromeOS"
	case strings.Contains(ua, "Linux"):
		platform = "Linux"
	}
	switch {
	case browser != "" && platform != "":
		return browser + " on " + platform
	case browser != "":
		return browser
	case platform != "":
		return platform
	}
	return "Device"
}
