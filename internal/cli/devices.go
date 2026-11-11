package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func clock(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04")
}

func Pair(args []string, std IO) error {
	fs := newFlags("pair")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	asJSON := fs.Bool("json", false, "print the server's body unchanged")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("%s", Usage("pair"))
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	base, _, err := serverFor(*port, dir)
	if err != nil {
		return err
	}
	raw, err := sendJSON(http.MethodPost, base, "/api/pairing-codes", nil)
	if err != nil {
		return err
	}
	if *asJSON {
		std.Out.Write(bytes.TrimRight(raw, "\n"))
		fmt.Fprintln(std.Out)
		return nil
	}
	var c struct {
		Code    string `json:"code"`
		URL     string `json:"url"`
		Expires string `json:"expires"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.Code == "" {
		return fmt.Errorf("POST /api/pairing-codes: unexpected answer")
	}
	exp := c.Expires
	if t, err := time.Parse(time.RFC3339, c.Expires); err == nil {
		exp = t.Local().Format("15:04")
	}
	fmt.Fprintf(std.Out, "Open this on the device:\n%s\nOr enter the code:\n%s\n", printable(c.URL), printable(c.Code))
	fmt.Fprintf(std.Out, "The code expires at %s and is single-use. Minting another voids this one.\n", printable(exp))
	return nil
}

type deviceRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Login    string `json:"login"`
	PairedAt string `json:"pairedAt"`
	LastSeen string `json:"lastSeen"`
}

func listDevices(base string) ([]deviceRow, []byte, error) {
	raw, err := getJSON(base, "/api/devices")
	if err != nil {
		return nil, nil, err
	}
	var body struct {
		Devices []deviceRow `json:"devices"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil, fmt.Errorf("GET /api/devices: %w", err)
	}
	return body.Devices, raw, nil
}

func Devices(args []string, std IO) error {
	if len(args) > 0 {
		switch args[0] {
		case "rename":
			return devicesRename(args[1:], std)
		case "revoke":
			return devicesRevoke(args[1:], std)
		}
	}
	fs := newFlags("devices")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	asJSON := fs.Bool("json", false, "print the server's body unchanged")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("%s", Usage("devices"))
	}
	base, err := deviceServer(*port)
	if err != nil {
		return err
	}
	devs, raw, err := listDevices(base)
	if err != nil {
		return err
	}
	if *asJSON {
		std.Out.Write(bytes.TrimRight(raw, "\n"))
		fmt.Fprintln(std.Out)
		return nil
	}
	for _, d := range devs {
		row(std.Out, d.Name, d.Login, clock(d.PairedAt), clock(d.LastSeen), d.ID)
	}
	return nil
}

func deviceServer(port string) (string, error) {
	dir, err := admiralHome()
	if err != nil {
		return "", err
	}
	base, _, err := serverFor(port, dir)
	return base, err
}

// positionals re-parses flags after each positional, as lock does.
func positionals(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	for fs.NArg() > 0 {
		pos = append(pos, fs.Arg(0))
		if err := parseFlags(fs, fs.Args()[1:]); err != nil {
			return nil, err
		}
	}
	return pos, nil
}

// resolveDevice treats a key that is one device's id and another's name as
// ambiguous: names are chosen by the paired devices themselves.
func resolveDevice(devs []deviceRow, key string) (deviceRow, error) {
	var hits []deviceRow
	for _, d := range devs {
		if d.ID == key || d.Name == key {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 0:
		return deviceRow{}, fmt.Errorf("no paired device %q (see `admiral devices`)", key)
	case 1:
		return hits[0], nil
	}
	ids := make([]string, len(hits))
	for i, d := range hits {
		ids[i] = d.ID
	}
	return deviceRow{}, fmt.Errorf("%q matches %d paired devices; use an id that is not also a name: %s", key, len(hits), strings.Join(ids, ", "))
}

func devicesRename(args []string, std IO) error {
	fs := newFlags("devices rename")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	pos, err := positionals(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usagef("%s", Usage("devices"))
	}
	base, err := deviceServer(*port)
	if err != nil {
		return err
	}
	devs, _, err := listDevices(base)
	if err != nil {
		return err
	}
	d, err := resolveDevice(devs, pos[0])
	if err != nil {
		return err
	}
	raw, err := sendJSON(http.MethodPatch, base, "/api/devices/"+url.PathEscape(d.ID), map[string]string{"name": pos[1]})
	if err != nil {
		return err
	}
	named := pos[1]
	var got struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &got) == nil && got.Name != "" {
		named = got.Name
	}
	fmt.Fprintf(std.Out, "Renamed %s to %s.\n", printable(d.Name), printable(named))
	return nil
}

func devicesRevoke(args []string, std IO) error {
	fs := newFlags("devices revoke")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	all := fs.Bool("all", false, "revoke every paired device")
	pos, err := positionals(fs, args)
	if err != nil {
		return err
	}
	if *all != (len(pos) == 0) || len(pos) > 1 {
		return usagef("%s", Usage("devices"))
	}
	base, err := deviceServer(*port)
	if err != nil {
		return err
	}
	if *all {
		if _, err := sendJSON(http.MethodDelete, base, "/api/devices", nil); err != nil {
			return err
		}
		fmt.Fprintln(std.Out, "Revoked every paired device.")
		return nil
	}
	devs, _, err := listDevices(base)
	if err != nil {
		return err
	}
	d, err := resolveDevice(devs, pos[0])
	if err != nil {
		return err
	}
	if _, err := sendJSON(http.MethodDelete, base, "/api/devices/"+url.PathEscape(d.ID), nil); err != nil {
		return err
	}
	fmt.Fprintf(std.Out, "Revoked %s.\n", printable(d.Name))
	return nil
}
