package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/github"
)

// followTimeout covers a first follow, which reads the whole issue tree from GitHub before answering.
const followTimeout = 60 * time.Second

// Campaigns follows an existing issue as a campaign, or prints the Make campaign link for a new parent.
func Campaigns(args []string, std IO) error {
	if len(args) == 0 {
		return usagef("%s", Usage("campaigns"))
	}
	switch args[0] {
	case "follow":
		return campaignsFollow(args[1:], std)
	case "link":
		return campaignsLink(args[1:], std)
	}
	return usagef("%s", Usage("campaigns"))
}

func campaignsFollow(args []string, std IO) error {
	fs := newFlags("campaigns follow")
	fleet := fs.String("fleet", "", "the fleet (default: the ship's)")
	port := fs.String("port", defaultPort(), "the server's port")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usagef("%s", Usage("campaigns"))
	}
	if os.Getenv("ADMIRAL_SCOUT") != "" {
		return errors.New("a scout changes nothing, so it can't follow a campaign")
	}
	dir, shipFleetKey, err := campaignHome()
	if err != nil {
		return err
	}
	if *fleet == "" {
		*fleet = shipFleetKey
	}
	base, _, err := serverFor(*port, dir)
	if err != nil {
		return err
	}
	body := map[string]string{"reference": rest[0]}
	if *fleet != "" {
		body["fleet"] = *fleet
	}
	status, b, err := postFollow(base, body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		if msg := titleLine(errorText(b)); msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("POST /api/campaigns: %d %s", status, http.StatusText(status))
	}
	var view struct {
		URL       string `json:"url"`
		Fleet     string `json:"fleet"`
		Title     string `json:"title"`
		Ended     bool   `json:"ended"`
		EndReason string `json:"end_reason"`
	}
	if err := json.Unmarshal(b, &view); err != nil {
		return fmt.Errorf("POST /api/campaigns: %v", err)
	}
	link, fleetKey, title := titleLine(view.URL), titleLine(view.Fleet), titleLine(view.Title)
	switch reason := titleLine(view.EndReason); {
	// A closed issue's end_reason is the word ended, which would only repeat the line.
	case view.Ended && (reason == "" || strings.EqualFold(reason, "ended")):
		fmt.Fprintf(std.Out, "%s has ended: %s\n", link, title)
	case view.Ended:
		fmt.Fprintf(std.Out, "%s has ended (%s): %s\n", link, reason, title)
	case status == http.StatusCreated:
		fmt.Fprintf(std.Out, "Followed %s in fleet %s: %s\n", link, fleetKey, title)
	default:
		fmt.Fprintf(std.Out, "Following %s in fleet %s: %s\n", link, fleetKey, title)
	}
	return nil
}

// postFollow keeps the status, which sendJSON drops, because 201 and 200 print different lines.
func postFollow(base string, body any) (int, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	resp, err := (&http.Client{Timeout: followTimeout}).Post(base+"/api/campaigns", "application/json", bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, b, err
}

func campaignsLink(args []string, std IO) error {
	fs := newFlags("campaigns link")
	fleet := fs.String("fleet", "", "the fleet (default: the ship's)")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return usagef("%s", Usage("campaigns"))
	}
	urls := make([]string, len(rest))
	for i, s := range rest {
		ref, err := campaigns.ParseIssueRef(s)
		if err != nil {
			return usagef("%s: %v", s, err)
		}
		host := strings.ToLower(ref.Host)
		if host == "" {
			host = github.DefaultHost
		}
		urls[i] = fmt.Sprintf("https://%s/%s/%s/issues/%d", host, ref.Owner, ref.Repo, ref.Number)
	}
	if *fleet == "" {
		if dir, ship := shipDir(); dir != "" {
			if f, ok := shipFleet(dir, ship); ok {
				*fleet = f.Key
			}
		}
	}
	if *fleet == "" {
		return usagef("no fleet: pass --fleet <key>")
	}
	q := url.Values{"fleet": {*fleet}, "newCampaign": {strings.Join(urls, ",")}}
	fmt.Fprintln(std.Out, "/app/orders?"+q.Encode())
	return nil
}

// shipDir is the ship's Admiral home, read the way advise reads it; "" outside a ship.
func shipDir() (dir, ship string) {
	ship, dir = os.Getenv("ADMIRAL_SHIP"), os.Getenv("ADMIRAL_HOME")
	if report := os.Getenv("ADMIRAL_REPORT"); report != "" {
		dir = filepath.Dir(filepath.Dir(report))
	}
	if ship == "" || dir == "." {
		return "", ""
	}
	return dir, ship
}

// campaignHome never falls back to admiralHome inside a ship, which would create ~/.admiral.
func campaignHome() (dir, fleet string, err error) {
	if os.Getenv("ADMIRAL_SHIP") == "" {
		dir, err = admiralHome()
		return dir, "", err
	}
	dir, ship := shipDir()
	if dir == "" {
		return "", "", errors.New("this ship's Admiral home is unknown: ADMIRAL_REPORT is not set")
	}
	if f, ok := shipFleet(dir, ship); ok {
		fleet = f.Key
	}
	return dir, fleet, nil
}

// titleLine prints text from the server or GitHub as data: control characters become spaces.
func titleLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)), " ")
}

const shipNote = `Admiral campaigns, from inside a ship:
- To track an existing issue as a campaign (for example a tracking issue you just filed), run: admiral campaigns follow <issue URL>
- To make a new parent issue from several issues, run: admiral campaigns link <issue URL>... and give the engineer the link it prints; only the engineer confirms a new parent in Admiral.
- Never call Admiral's campaign API by hand.`

// ShipNote is a Claude ship's SessionStart hook; it writes no state, so it never rebinds the ship's transcript.
func ShipNote(args []string, std IO) error {
	if os.Getenv("ADMIRAL_SHIP") == "" || os.Getenv("ADMIRAL_SCOUT") != "" {
		return nil
	}
	note := shipNote
	if c := shipContext(); c != "" {
		note += "\n\n" + c
	}
	raw, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "SessionStart", "additionalContext": note}})
	if err != nil {
		return nil
	}
	_, _ = fmt.Fprintln(std.Out, string(raw))
	return nil
}

// shipContextCap bounds what a woken ship's context file adds to its session; Admiral's own document is far smaller.
const shipContextCap = 16 << 10

// shipContext is the context document Admiral wrote for this wake, or "" unless ADMIRAL_CONTEXT names a regular file
// beside the ship's report: a path in the checkout, a link or an oversized file is never read, so a repo cannot pose as Admiral.
func shipContext() string {
	path := os.Getenv("ADMIRAL_CONTEXT")
	report := os.Getenv("ADMIRAL_REPORT")
	if path == "" || report == "" || filepath.Dir(path) != filepath.Dir(report) {
		return ""
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() > shipContextCap {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(f, shipContextCap+1))
	if err != nil || len(b) > shipContextCap {
		return ""
	}
	return strings.TrimSpace(string(b))
}
