package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNeedsMigration: only the server migrates; commands never do.
var ErrNeedsMigration = errors.New("config.json is from an older Admiral: start Admiral once (`admiral`) to migrate it")

const maxOrdersFile = 4 << 20

type Moved struct {
	Key, Name, Fleet, FleetName string
}

type Dropped struct {
	Key, Name        string
	Freeform         bool
	Orders           []string // the pending orders' text, freeform sources only
	OrdersUnreadable bool
}

// Entry is an enabled panel autopilot entry the migration removed.
type Entry struct {
	Key, Name, Panel string // the source's key and name, and the panel id
	Fleet            string // the fleet the source feeds: its own, else the first fleet
}

type Migration struct {
	Backup  string // file name of the backup, beside config.json
	Moved   []Moved
	Removed []Dropped
	// Entries are the enabled panel autopilot entries that went with the engine.
	Entries []Entry
	// AutopilotObject is set when the top-level autopilot object went (its
	// max_ships moved to the top level).
	AutopilotObject bool
	GitHub          bool // an old GitHub plugin source became built in, on every fleet
	GitHubCustom    bool // and it had custom views, which the default views now sit beside
	// Chat is set when the top-level chat key went. ChatFleet is the fleet whose own chat
	// settings it went onto, empty when nothing moved: ChatUnreadable says the key could
	// not be read, else the file had no fleet to take it.
	Chat                     bool
	ChatFleet, ChatFleetName string
	ChatUnreadable           bool
}

type migrationPlan struct {
	out     []byte
	moved   []Moved
	removed []Dropped
	entries []Entry
	object  bool
	changed bool // a kept source lost its autopilot, whether or not it is listed above
	github  bool
	custom  bool
	chat    *chatMove
}

type chatMove struct {
	fleet, name string // the destination; empty when nothing moved
	unreadable  bool
}

func NeedsMigration(data []byte) bool {
	return planMigration(data) != nil
}

// planMigration also returns nil when the file wouldn't parse once migrated:
// that is another error, for the banner.
func planMigration(data []byte) *migrationPlan {
	top, err := ParseObject(data)
	if err != nil {
		return nil
	}
	p := &migrationPlan{}
	if rawSources, ok := top.Get("sources"); ok {
		sources, err := ParseObject(rawSources)
		if err != nil {
			return nil
		}
		// The default fleet is the first one in the file.
		defKey, defName := "", ""
		if rawFleets, ok := top.Get("fleets"); ok {
			if fleets, err := ParseObject(rawFleets); err == nil && fleets.Len() > 0 {
				defKey = fleets.Keys()[0]
				raw, _ := fleets.Get(defKey)
				defName = nameOf(raw, defKey)
			}
		}
		for _, key := range sources.Keys() {
			raw, _ := sources.Get(key)
			src, err := ParseObject(raw)
			if err != nil {
				return nil
			}
			plugin := stringOf(src, "plugin")
			_, hasFleet := src.Get("fleet")
			changed := false
			// The panel autopilot entries went with the engine, whether the source stays or not.
			if ap, ok := src.Get("autopilot"); ok {
				p.entries = append(p.entries, enabledEntries(key, nameOf(raw, key), fleetOf(src, defKey), ap)...)
				src.Delete("autopilot")
				changed = true
				p.changed = true
			}
			switch {
			case plugin == "freeform":
				sources.Delete(key)
				p.removed = append(p.removed, Dropped{Key: key, Name: nameOf(raw, key), Freeform: true})
				continue
			case hasFleet:
			case defKey == "":
				sources.Delete(key)
				p.removed = append(p.removed, Dropped{Key: key, Name: nameOf(raw, key)})
				continue
			default:
				src.SetValue("fleet", defKey)
				changed = true
				p.moved = append(p.moved, Moved{Key: key, Name: nameOf(raw, key), Fleet: defKey, FleetName: defName})
			}
			if changed {
				sources.Set(key, src.JSON())
			}
		}
		p.github, p.custom = migrateGitHub(top, sources)
		top.Set("sources", sources.JSON())
	}
	// The top-level autopilot object: its cap moves up, the rest goes.
	if raw, ok := top.Get("autopilot"); ok {
		// One that is not an object has no cap to move: it is dropped.
		if ap, err := ParseObject(raw); err == nil {
			if cap, ok := ap.Get("max_ships"); ok {
				if _, have := top.Get("max_ships"); !have {
					top.Set("max_ships", cap)
				}
			}
		}
		top.Delete("autopilot")
		p.object = true
	}
	p.chat = migrateChat(top)
	if len(p.moved) == 0 && len(p.removed) == 0 && len(p.entries) == 0 && !p.object && !p.changed && !p.github && p.chat == nil {
		return nil
	}
	out := top.JSON()
	if strings.IndexByte(string(data), '\n') >= 0 {
		if out, err = indent(out); err != nil {
			return nil
		}
	}
	if _, err := Parse(out); err != nil {
		return nil
	}
	p.out = out
	return p
}

// migrateChat moves the top-level chat key's model and effort onto the fleet it names, else
// the first fleet with a Claude Code dock, else the first fleet, and removes the key. It
// returns nil when there is no such key.
func migrateChat(top *Object) *chatMove {
	raw, ok := top.Get("chat")
	if !ok {
		return nil
	}
	top.Delete("chat")
	old, err := ParseObject(raw)
	if err != nil {
		return &chatMove{unreadable: true}
	}
	rawFleets, ok := top.Get("fleets")
	if !ok {
		return &chatMove{}
	}
	fleets, err := ParseObject(rawFleets)
	if err != nil {
		return &chatMove{unreadable: true}
	}
	if fleets.Len() == 0 {
		return &chatMove{}
	}
	dest := stringOf(old, "fleet")
	if _, ok := fleets.Get(dest); !ok {
		dest = fleets.Keys()[0]
		for _, k := range fleets.Keys() {
			if f, err := ParseObject(mustRaw(fleets, k)); err == nil {
				if _, ok := f.Get("claude"); ok {
					dest = k
					break
				}
			}
		}
	}
	rawFleet, _ := fleets.Get(dest)
	fleet, err := ParseObject(rawFleet)
	if err != nil {
		return &chatMove{unreadable: true}
	}
	chat := NewObject()
	if rc, ok := fleet.Get("chat"); ok {
		if chat, err = ParseObject(rc); err != nil {
			chat = NewObject()
		}
	}
	for _, k := range []string{"model", "effort"} {
		if v, ok := old.Get(k); ok && stringOf(old, k) != "" {
			if _, have := chat.Get(k); !have {
				chat.Set(k, v)
			}
		}
	}
	if chat.Len() > 0 {
		fleet.Set("chat", chat.JSON())
		fleets.Set(dest, fleet.JSON())
		top.Set("fleets", fleets.JSON())
	}
	return &chatMove{fleet: dest, name: nameOf(rawFleet, dest)}
}

// enabledEntries lists the enabled entries of a source's autopilot object, in
// panel order. A disabled entry, or an object that isn't one, lists nothing.
func enabledEntries(key, name, fleet string, raw json.RawMessage) []Entry {
	panels, err := ParseObject(raw)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, panel := range panels.Keys() {
		v, _ := panels.Get(panel)
		e, err := ParseObject(v)
		if err != nil {
			continue
		}
		var on bool
		if raw, ok := e.Get("enabled"); ok && json.Unmarshal(raw, &on) == nil && on {
			out = append(out, Entry{Key: key, Name: name, Panel: panel, Fleet: fleet})
		}
	}
	return out
}

// githubCarried are the settings a fleet's new GitHub source copies from the
// old plugin source.
var githubCarried = []string{"views", "host", "account", "hotLabels", "refreshMinutes"}

// migrateGitHub makes the plugin's GitHub sources (no `views`) built-in ones and
// gives each fleet without one a copy; with every source on `views` it finds
// nothing, so a source the engineer deletes later stays deleted.
func migrateGitHub(top, sources *Object) (changed, custom bool) {
	var template *Object
	covered := map[string]bool{}
	for _, key := range sources.Keys() {
		raw, _ := sources.Get(key)
		src, err := ParseObject(raw)
		if err != nil || stringOf(src, "plugin") != "github" {
			continue
		}
		covered[stringOf(src, "fleet")] = true
		settings := NewObject()
		if rs, ok := src.Get("settings"); ok {
			if settings, err = ParseObject(rs); err != nil {
				continue
			}
		}
		if _, ok := settings.Get("views"); ok {
			continue
		}
		views := json.RawMessage("[]")
		if rp, ok := settings.Get("panels"); ok {
			var list []json.RawMessage
			if json.Unmarshal(rp, &list) == nil {
				views = rp
				custom = custom || len(list) > 0
			}
			settings.Delete("panels")
		}
		settings.Set("views", views)
		src.Set("settings", settings.JSON())
		sources.Set(key, src.JSON())
		changed = true
		if template == nil {
			template = src
		}
	}
	if template == nil {
		return false, false
	}
	rawFleets, ok := top.Get("fleets")
	if !ok {
		return changed, custom
	}
	fleets, err := ParseObject(rawFleets)
	if err != nil {
		return changed, custom
	}
	settings, _ := ParseObject(mustRaw(template, "settings"))
	for _, fleet := range fleets.Keys() {
		if covered[fleet] {
			continue
		}
		key := "github-" + fleet
		for n := 2; ; n++ {
			if _, taken := sources.Get(key); !taken {
				break
			}
			key = fmt.Sprintf("github-%s-%d", fleet, n)
		}
		carried := NewObject()
		for _, k := range githubCarried {
			if v, ok := settings.Get(k); ok {
				carried.Set(k, v)
			}
		}
		src := NewObject()
		src.SetValue("plugin", "github")
		if raw, ok := template.Get("name"); ok {
			src.Set("name", raw)
		}
		src.SetValue("fleet", fleet)
		src.Set("settings", carried.JSON())
		if raw, ok := template.Get("panel_order"); ok {
			src.Set("panel_order", raw)
		}
		sources.Set(key, src.JSON())
	}
	return changed, custom
}

// mustRaw is the raw value of key in o, or an empty object.
func mustRaw(o *Object, key string) json.RawMessage {
	if raw, ok := o.Get(key); ok {
		return raw
	}
	return json.RawMessage("{}")
}

func stringOf(o *Object, key string) string {
	raw, ok := o.Get(key)
	if !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func fleetOf(src *Object, def string) string {
	if f := stringOf(src, "fleet"); f != "" {
		return f
	}
	return def
}

func nameOf(raw json.RawMessage, key string) string {
	if o, err := ParseObject(raw); err == nil {
		if n := stringOf(o, "name"); n != "" {
			return n
		}
	}
	return key
}

// Migrate changes nothing when the file has another error, so the second start
// is a no-op. An existing config.json.pre-<version> backup stays as it is.
func Migrate(dir, version string) (*Migration, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := planMigration(data)
	if p == nil {
		return nil, nil
	}
	for i, d := range p.removed {
		if d.Freeform {
			p.removed[i].Orders, p.removed[i].OrdersUnreadable = pendingOrders(dir, d.Key)
		}
	}
	backup := FileName + ".pre-" + version
	if err := writeNew(filepath.Join(filepath.Dir(path), backup), data); err != nil {
		return nil, fmt.Errorf("backup %s: %w", backup, err)
	}
	if err := replaceFile(path, p.out); err != nil {
		return nil, err
	}
	m := &Migration{Backup: backup, Moved: p.moved, Removed: p.removed, Entries: p.entries, AutopilotObject: p.object, GitHub: p.github, GitHubCustom: p.custom}
	if p.chat != nil {
		m.Chat, m.ChatFleet, m.ChatFleetName, m.ChatUnreadable = true, p.chat.fleet, p.chat.name, p.chat.unreadable
	}
	return m, nil
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// pendingOrders checks the key itself: the migration runs before the config is parsed.
func pendingOrders(dir, key string) (texts []string, unreadable bool) {
	if !NameRe.MatchString(key) {
		return nil, true
	}
	path := filepath.Join(dir, "data", key, "orders.json")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, true
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, true
	}
	if rel, err := filepath.Rel(root, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, true
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, true
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxOrdersFile+1))
	if err != nil || len(b) > maxOrdersFile {
		return nil, true
	}
	var orders []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(b, &orders) != nil || orders == nil {
		return nil, true
	}
	for _, o := range orders {
		texts = append(texts, o.Text)
	}
	return texts, false
}

// Notice never quotes a source's settings.
func (m *Migration) Notice() (title, body string) {
	var b strings.Builder
	fleets := len(m.Moved)+len(m.Removed) > 0
	autopilot := len(m.Entries) > 0 || m.AutopilotObject
	title = "Admiral updated your config"
	if !fleets && !m.GitHub && !m.Chat {
		title = "Admiral removed autopilot from your config"
	}
	var did []string
	if fleets {
		did = append(did, "moved your sources into fleets")
	}
	if m.GitHub && !fleets {
		did = append(did, "updated your GitHub source")
	}
	switch {
	case m.ChatFleet != "":
		did = append(did, "moved the chat settings onto their fleet")
	case m.Chat:
		did = append(did, "removed the old chat settings")
	}
	if autopilot {
		did = append(did, "removed the autopilot engine's settings")
	}
	if len(did) == 0 {
		did = append(did, "updated your config")
	}
	fmt.Fprintf(&b, "Admiral %s. The old file is saved as %s beside config.json.\n", strings.Join(did, " and "), m.Backup)
	for _, s := range m.Moved {
		fmt.Fprintf(&b, "\n- Source %s (%s) now belongs to fleet %s (%s).", s.Name, s.Key, s.FleetName, s.Fleet)
	}
	for _, s := range m.Removed {
		if !s.Freeform {
			fmt.Fprintf(&b, "\n- Removed source %s (%s): there is no fleet to give it. The backup keeps it.", s.Name, s.Key)
			continue
		}
		fmt.Fprintf(&b, "\n- Removed the freeform source %s (%s).", s.Name, s.Key)
		switch {
		case s.OrdersUnreadable:
			b.WriteString(" Its orders file could not be read, so its orders are not listed here. The file is still in data/" + s.Key + "/orders.json.")
		case len(s.Orders) == 0:
			b.WriteString(" It had no pending orders.")
		default:
			b.WriteString(" Its pending orders were:")
			for _, o := range s.Orders {
				fmt.Fprintf(&b, "\n  * %s", strings.ReplaceAll(o, "\n", "\n    "))
			}
			b.WriteString("\n  An order may already have a ship if its launch was never acknowledged.")
		}
	}
	if m.GitHub {
		b.WriteString("\n- GitHub is now built in and runs on every fleet.")
		if m.GitHubCustom {
			b.WriteString(" Your custom views were copied to each one. The three default views (Assigned to me, Review requested, My PRs) now show beside them.")
		}
	}
	if m.Chat {
		switch {
		case m.ChatUnreadable:
			b.WriteString("\n- The old chat settings could not be read, so they were dropped. Each fleet has its own chat settings now, at the defaults; the backup keeps the old ones.")
		case m.ChatFleet == "":
			b.WriteString("\n- The chat settings key is gone: each fleet has its own chat settings now, and there was no fleet to move yours to.")
		default:
			fmt.Fprintf(&b, "\n- Each fleet has its own chat settings now. Yours went to fleet %s (%s); every other fleet has the defaults.", m.ChatFleetName, m.ChatFleet)
		}
	}
	if len(m.Entries) > 0 {
		b.WriteString("\nThese view autopilot entries were removed with the autopilot engine. Automate… on the matching view in the Orders list drafts a scheduled routine for each:")
		for _, e := range m.Entries {
			fmt.Fprintf(&b, "\n- Source %s (%s), view %s.", e.Name, e.Key, e.Panel)
		}
	}
	if m.AutopilotObject {
		b.WriteString("\nThe autopilot object is gone, with its resume source. Its max_ships, if it had one, is now the top-level max_ships.")
	}
	return title, b.String()
}
