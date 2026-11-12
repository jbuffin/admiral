package inbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/crew"
)

// The crew kinds live only in the AdmiralSession directory: Admiral writes them from a run's report, and only an answer writes to the fleet's crew.
const (
	KindCrewNotes  = "crew_notes"
	KindCrewMember = "crew_member"
)

const (
	maxNoteChars   = 300
	maxNotesPerMem = 3
	maxRoleChars   = 64
	reportVersion  = 2
)

// FleetInfo is what the inbox knows of a configured fleet; Key is the configured one.
type FleetInfo struct{ Key, Name, Color string }

// CrewNote is a proposed note. Replaces is the whole line it takes the place of, without the dash.
type CrewNote struct {
	ID       string  `json:"id"`
	Member   string  `json:"member"`
	Role     string  `json:"role"`
	Scope    string  `json:"scope"`
	Repo     string  `json:"repo,omitempty"`
	Note     string  `json:"note"`
	Replaces *string `json:"replaces"`
}

// CrewObject is the crew_notes or crew_member object of an item. Fleet is the key the run's ship was launched in.
type CrewObject struct {
	Fleet      string     `json:"fleet"`
	Ship       string     `json:"ship"`
	RunDate    string     `json:"run_date"`
	Member     string     `json:"member,omitempty"`
	Role       string     `json:"role,omitempty"`
	AdHocRun   string     `json:"ad_hoc_run,omitempty"`
	Definition string     `json:"definition,omitempty"`
	Notes      []CrewNote `json:"notes"`
}

// CrewRun is a ship that ended: where it was launched and what it was called.
type CrewRun struct {
	Home     string
	FleetKey string
	Fleets   func(key string) (FleetInfo, bool)
	// BuiltIns names the fleet's built-in members; an ad hoc member of one of those names is never kept.
	BuiltIns func(key string) []string
	Ship     string
	Session  string
	Launched string
	RunDate  string // the UTC date the ship ended
}

// CrewObject is the object of a crew_notes or crew_member item.
func (it Item) CrewObject() (CrewObject, bool) {
	var o CrewObject
	if it.Kind != KindCrewNotes && it.Kind != KindCrewMember {
		return o, false
	}
	return o, json.Unmarshal(it.fields[it.Kind], &o) == nil && o.Fleet != "" && o.Ship != "" && validDay(o.RunDate) &&
		(it.Kind == KindCrewNotes || (crew.ValidName(o.Member) && o.Definition != ""))
}

func validDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func itemHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

type reportNote struct {
	Member   string  `json:"member"`
	Role     string  `json:"role"`
	Scope    string  `json:"scope"`
	Repo     *string `json:"repo"`
	Note     string  `json:"note"`
	Replaces *string `json:"replaces"`
	AdHoc    bool    `json:"ad-hoc"`
}

type reportCast struct {
	Member string `json:"member"`
	Role   string `json:"role"`
	AdHoc  bool   `json:"ad_hoc"`
}

func plainLine(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= maxRoleChars && !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

// WriteCrewItems turns the crew proposals of a run's report into Inbox items, and writes nothing to the fleet's crew.
// The report is the ship's own output, so each entry is checked and a bad one skipped alone. A second call for the same
// run writes nothing new.
func WriteCrewItems(run CrewRun, report []byte) error {
	if run.FleetKey == "" || run.Fleets == nil {
		return nil
	}
	fleet, ok := run.Fleets(run.FleetKey)
	if !ok || fleet.Key == "" || !filepath.IsLocal(fleet.Key) || strings.ContainsRune(fleet.Key, filepath.Separator) {
		return nil
	}
	var r struct {
		Version int               `json:"version"`
		Cast    []json.RawMessage `json:"cast"`
		Notes   []json.RawMessage `json:"notes"`
	}
	if json.Unmarshal(report, &r) != nil || r.Version != reportVersion {
		return nil
	}
	skip := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: crew proposal skipped: %s\n", run.Ship, fmt.Sprintf(format, args...))
	}
	fact := func(n reportNote) (string, bool) {
		note := strings.TrimSpace(n.Note)
		switch {
		case !crew.ValidName(n.Member) || !plainLine(n.Role):
			skip("member %q or role %q is not valid", n.Member, n.Role)
		case n.Role == crew.FixChecker:
			skip("the fix checker has no memory")
		case n.Scope != "fleet" && n.Scope != "repo":
			skip("a note of %s has the scope %q", n.Member, n.Scope)
		case n.Scope == "repo" && (n.Repo == nil || !repoOK(*n.Repo)):
			skip("a note of %s has no valid repo", n.Member)
		case note == "" || utf8.RuneCountInString(note) > maxNoteChars || !crew.ValidNote("- "+run.RunDate+" ("+run.Ship+"): "+note):
			skip("a note of %s is empty, over %d characters or has characters that can't be shown", n.Member, maxNoteChars)
		case n.Replaces != nil && !crew.ValidNote("- "+*n.Replaces):
			skip("a note of %s replaces something that is not a note line", n.Member)
		default:
			return note, true
		}
		return "", false
	}

	perMember := map[string]int{}
	var shared []CrewNote
	adHocNotes := map[string][]CrewNote{}
	var adHocOrder []string
	adHocRole := map[string]string{}
	castAdHoc := map[string]bool{}
	addAdHoc := func(member, role string) {
		if _, ok := adHocRole[member]; !ok {
			adHocRole[member] = role
			adHocOrder = append(adHocOrder, member)
		}
	}
	for _, raw := range r.Cast {
		var c reportCast
		if json.Unmarshal(raw, &c) != nil || !c.AdHoc {
			continue
		}
		if !crew.ValidName(c.Member) || !plainLine(c.Role) || c.Role == crew.FixChecker {
			skip("cast member %q is not valid", c.Member)
			continue
		}
		addAdHoc(c.Member, c.Role)
		castAdHoc[c.Member] = true
	}
	for _, raw := range r.Notes {
		var n reportNote
		if json.Unmarshal(raw, &n) != nil {
			skip("a note is not an object of the schema")
			continue
		}
		text, ok := fact(n)
		if !ok {
			continue
		}
		if perMember[n.Member]++; perMember[n.Member] > maxNotesPerMem {
			skip("%s proposed more than %d notes", n.Member, maxNotesPerMem)
			continue
		}
		c := CrewNote{Member: n.Member, Role: n.Role, Scope: n.Scope, Note: text, Replaces: n.Replaces}
		if n.Scope == "repo" {
			c.Repo = *n.Repo
		}
		if n.AdHoc || castAdHoc[n.Member] {
			addAdHoc(n.Member, n.Role)
			adHocNotes[n.Member] = append(adHocNotes[n.Member], c)
		} else {
			shared = append(shared, c)
		}
	}

	title := func(format string, args ...any) string { return fmt.Sprintf(format, args...) }
	if len(shared) > 0 {
		for i := range shared {
			shared[i].ID = "n" + strconv.Itoa(i+1)
		}
		err := publishAdmiralItem(run.Home, "crew-notes-"+itemHash(run.Session, run.Launched), map[string]any{
			"kind": KindCrewNotes, "title": title("Crew notes from %s", run.Ship),
			KindCrewNotes: CrewObject{Fleet: fleet.Key, Ship: run.Ship, RunDate: run.RunDate, Notes: shared},
		})
		if err != nil {
			return err
		}
	}
	var errs []error
	var builtIns []string
	if run.BuiltIns != nil {
		builtIns = run.BuiltIns(run.FleetKey)
	}
	for _, member := range adHocOrder {
		if slices.Contains(builtIns, member) || fleetMemberExists(run.Home, fleet.Key, member) {
			skip("%s is already a standing member, so an ad hoc one of that name is dropped", member)
			continue
		}
		def, runID, ok := readAdHocDefinition(run.Home, fleet.Key, member)
		if !ok {
			skip("no usable ad hoc file for %s, so its notes are dropped", member)
			continue
		}
		notes := adHocNotes[member]
		if notes == nil {
			notes = []CrewNote{}
		}
		for i := range notes {
			notes[i].ID = "n" + strconv.Itoa(i+1)
		}
		errs = append(errs, publishAdmiralItem(run.Home, "crew-member-"+itemHash(run.Session, run.Launched, member), map[string]any{
			"kind": KindCrewMember, "title": title("Keep ad hoc member %s?", member),
			KindCrewMember: CrewObject{Fleet: fleet.Key, Ship: run.Ship, RunDate: run.RunDate, Member: member, Role: adHocRole[member], AdHocRun: runID, Definition: def, Notes: notes},
		}))
	}
	return errors.Join(errs...)
}

func fleetMemberExists(home, fleetKey, member string) bool {
	_, err := os.Lstat(filepath.Join(home, "fleets", fleetKey, "crew", member+".md"))
	return err == nil
}

func repoOK(slug string) bool {
	_, _, ok := crew.SplitRepo(slug)
	return ok
}

// readAdHocDefinition reads the ad hoc file the way the resolver does: no link in the path, a regular file, at most
// the size cap, and a member that says ad-hoc: true.
func readAdHocDefinition(home, fleetKey, member string) (text, runID string, ok bool) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return "", "", false
	}
	defer root.Close()
	dir := root
	for _, part := range []string{"fleets", fleetKey, "crew", "ad-hoc"} {
		next, err := realDir(dir, part)
		if err != nil {
			return "", "", false
		}
		defer next.Close()
		dir = next
	}
	name := member + ".md"
	if fi, err := dir.Lstat(name); err != nil || !fi.Mode().IsRegular() {
		return "", "", false
	}
	f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65536+1))
	if err != nil || len(b) > 65536 || !utf8.Valid(b) {
		return "", "", false
	}
	m, ferr := crew.CheckAdHoc(b, member)
	if ferr != nil {
		return "", "", false
	}
	if m.AdHocRun != nil {
		runID = *m.AdHocRun
	}
	return string(b), runID, true
}

// publishAdmiralItem writes the item once: an item or answer already there for id is left as it is.
func publishAdmiralItem(home, id string, fields map[string]any) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("inbox: %q is not an item id", id)
	}
	dir := filepath.Join(home, "inbox", AdmiralSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, answerName(id))); err == nil {
		return nil
	}
	item := map[string]any{"version": 1, "id": id, "created": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range fields {
		item[k] = v
	}
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".item-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	// A link fails when the item exists, so an item is never replaced.
	if err := os.Link(tmp.Name(), filepath.Join(dir, id+".json")); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}
