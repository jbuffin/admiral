package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/tailscale"
)

// errTailscaleStopped ends setup after a check said why; nothing was written.
var errTailscaleStopped = errors.New("Tailscale mode was not turned on and nothing was written")

// remoteAsk is what the person asked for under "remote": the mode, and logins to allow besides the Mac's own.
type remoteAsk struct {
	tailscale bool
	logins    []string
	macLogin  string // set once the checks have run
}

// merged adds the asked logins and the Mac's own to the ones already allowed; the mode only turns on.
func (a *remoteAsk) merged(cur config.Remote) config.Remote {
	out := config.Remote{Tailscale: cur.Tailscale || a.tailscale, AllowedLogins: slices.Clone(cur.AllowedLogins)}
	for _, l := range append(slices.Clone(a.logins), a.macLogin) {
		if l != "" && !slices.Contains(out.AllowedLogins, l) {
			out.AllowedLogins = append(out.AllowedLogins, l)
		}
	}
	return out
}

func (s *session) parseRemote(raw json.RawMessage, path string) error {
	obj, err := config.ParseObject(raw)
	if err != nil {
		return fmt.Errorf("%s: remote: %w", path, err)
	}
	ask := &remoteAsk{}
	for _, k := range obj.Keys() {
		v, _ := obj.Get(k)
		switch k {
		case "tailscale":
			if err := json.Unmarshal(v, &ask.tailscale); err != nil {
				return fmt.Errorf("%s: remote.tailscale: must be true or false", path)
			}
		case "allowedLogins":
			if err := json.Unmarshal(v, &ask.logins); err != nil {
				return fmt.Errorf("%s: remote.allowedLogins: must be a list of logins", path)
			}
		default:
			return fmt.Errorf("%s: remote.%s: unknown key", path, k)
		}
	}
	s.remote = ask
	return nil
}

func (s *session) askRemote() error {
	yes, err := confirm(s.reader(), s.io.Out, "Turn on Tailscale mode (reach Admiral from your tailnet through tailscale serve)?")
	if err != nil {
		return err
	}
	if yes {
		s.remote = &remoteAsk{tailscale: true}
	}
	return nil
}

// currentRemote is the remote section config.json holds now.
func currentRemote(dir string) (config.Remote, error) {
	data, err := config.ReadConfig(dir)
	if err != nil {
		return config.Remote{}, err
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return config.Remote{}, err
	}
	return cfg.Remote, nil
}

// tailscaleStep runs the checks, prints the Certificate Transparency warning and asks to go on, changing nothing.
// With dry it also prints the serve command it would run. It returns false when the person declined.
func (s *session) tailscaleStep(yes, dry bool) (bool, error) {
	w := s.io.Out
	pre := tailscale.Prepare(context.Background(), DefaultPort)
	if err := printChecks(w, pre); err != nil {
		return false, err
	}
	if pre.Serving {
		fmt.Fprintln(w, "tailscale serve already proxies to Admiral; leaving it as it is.")
	} else if pre.Holder != "" {
		fmt.Fprintf(w, "[failed] HTTPS port 443 is already used by %s, and setup never replaces it.\n", pre.Holder)
		fmt.Fprintf(w, "To serve Admiral on another HTTPS port, run:\n  %s\nthen run setup again.\n", "tailscale serve --bg --https=8443 http://127.0.0.1:"+DefaultPort)
		return false, errTailscaleStopped
	}
	s.remote.macLogin = pre.Login
	if pre.Login == "" {
		fmt.Fprintln(w, "tailscale did not name a login for this Mac, so allowedLogins gets none from it.")
	}
	fmt.Fprintln(w, "\nCertificate Transparency: the HTTPS certificate publishes this Mac's ts.net name in public Certificate Transparency logs.")
	if dry {
		if !pre.Serving {
			fmt.Fprintf(w, "Would run: %s\n", tailscale.ServeHint(DefaultPort))
		}
		return true, nil
	}
	s.serve = !pre.Serving
	if yes {
		return true, nil
	}
	ok, err := confirm(s.reader(), w, "Go on?")
	if err == nil && !ok {
		fmt.Fprintln(w, "Nothing changed.")
	}
	return ok, err
}

// printChecks prints one line per check up to the first failure, which stops setup.
func printChecks(w io.Writer, pre tailscale.Preflight) error {
	for _, c := range pre.Checks {
		if !c.OK {
			fmt.Fprintf(w, "[failed] %s\n", c.Detail)
			return errTailscaleStopped
		}
		fmt.Fprintf(w, "[passed] %s\n", c.Detail)
	}
	return nil
}

// runServe sets serve up once; a failure prints the CLI's message and stops setup before anything is written.
func (s *session) runServe() error {
	if !s.serve {
		return nil
	}
	fmt.Fprintf(s.io.Out, "Running: %s\n", tailscale.ServeHint(DefaultPort))
	if err := tailscale.EnableServe(context.Background(), DefaultPort); err != nil {
		fmt.Fprintf(s.io.Out, "[failed] %v\n", err)
		return errTailscaleStopped
	}
	s.serve, s.served = false, true
	return nil
}
