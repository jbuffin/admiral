package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
)

type holdSwitch struct {
	On bool   `json:"on"`
	By string `json:"by"`
}

type holdState struct {
	On     bool       `json:"on"`
	By     string     `json:"by"`
	Fleet  holdSwitch `json:"fleet"`
	Global holdSwitch `json:"global"`
}

// Hold turns Hold fire on or off for a fleet or for all fleets, and shows its state.
func Hold(args []string, std IO) error {
	fs := newFlags("hold")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	fleet := fs.String("fleet", "", "the fleet's key")
	all := fs.Bool("all", false, "every fleet")
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return usagef("%s", Usage("hold"))
	}
	verb := args[0]
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 || *all && *fleet != "" {
		return usagef("%s", Usage("hold"))
	}
	var path string
	switch verb {
	case "on", "off":
		if !*all && *fleet == "" {
			return usagef("hold %s needs --fleet <key> or --all", verb)
		}
		path = "/hold-fire/" + verb
	case "status":
		path = "/hold-fire"
	default:
		return usagef("%s", Usage("hold"))
	}
	if *fleet != "" {
		path = "/api/fleets/" + url.PathEscape(*fleet) + path
	} else {
		path = "/api" + path
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	base, _, err := serverFor(*port, dir)
	if err != nil {
		return err
	}
	var raw []byte
	if verb == "status" {
		raw, err = getJSON(base, path)
	} else {
		raw, err = sendJSON("POST", base, path, nil)
	}
	if err != nil {
		return err
	}
	var st holdState
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	onOff := map[bool]string{true: "on", false: "off"}
	if *fleet == "" {
		line := "Hold fire for all fleets is " + onOff[st.On]
		if st.On && st.By != "" {
			line += " (turned on by " + printable(st.By) + ")"
		}
		fmt.Fprintln(std.Out, line)
		return nil
	}
	line := "Hold fire for " + printable(*fleet) + " is " + onOff[st.On]
	switch {
	case st.Global.On:
		line += " (all fleets are held)"
	case st.On && st.Fleet.By != "":
		line += " (turned on by " + printable(st.Fleet.By) + ")"
	}
	fmt.Fprintln(std.Out, line)
	return nil
}
