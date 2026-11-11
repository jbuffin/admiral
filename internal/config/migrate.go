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
	GitHub                      bool // the GitHub source takes the default fleet
}

type Dropped struct {
	Key, Name        string
	Freeform         bool
	Orders           []string // the pending orders' text, freeform sources only
	OrdersUnreadable bool
}

type Migration struct {
	Backup        string // file name of the backup, beside config.json
	Moved         []Moved
	Removed       []Dropped
	ClearedResume string // the resume source it cleared; empty when none
}

type migrationPlan struct {
	out     []byte
	moved   []Moved
	removed []Dropped
	cleared string
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
	rawSources, ok := top.Get("sources")
	if !ok {
		return nil
	}
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

	p := &migrationPlan{}
	for _, key := range sources.Keys() {
		raw, _ := sources.Get(key)
		src, err := ParseObject(raw)
		if err != nil {
			return nil
		}
		plugin := stringOf(src, "plugin")
		_, hasFleet := src.Get("fleet")
		switch {
		case plugin == "freeform":
			sources.Delete(key)
			p.removed = append(p.removed, Dropped{Key: key, Name: nameOf(raw, key), Freeform: true})
		case hasFleet:
		case defKey == "":
			sources.Delete(key)
			p.removed = append(p.removed, Dropped{Key: key, Name: nameOf(raw, key)})
		default:
			src.SetValue("fleet", defKey)
			sources.Set(key, src.JSON())
			p.moved = append(p.moved, Moved{Key: key, Name: nameOf(raw, key), Fleet: defKey, FleetName: defName, GitHub: plugin == "github"})
		}
	}
	if len(p.moved) == 0 && len(p.removed) == 0 {
		return nil
	}
	top.Set("sources", sources.JSON())
	if raw, ok := top.Get("autopilot"); ok {
		if ap, err := ParseObject(raw); err == nil {
			if rs, ok := ap.Get("resume_source"); ok {
				var r struct {
					Source string `json:"source"`
				}
				if json.Unmarshal(rs, &r) == nil {
					for _, d := range p.removed {
						if d.Key == r.Source {
							ap.Delete("resume_source")
							p.cleared = r.Source
							top.Set("autopilot", ap.JSON())
						}
					}
				}
			}
		}
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

func stringOf(o *Object, key string) string {
	raw, ok := o.Get(key)
	if !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
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
	return &Migration{Backup: backup, Moved: p.moved, Removed: p.removed, ClearedResume: p.cleared}, nil
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
	title = "Admiral updated your config"
	fmt.Fprintf(&b, "Admiral moved your sources into fleets. The old file is saved as %s beside config.json.\n", m.Backup)
	for _, s := range m.Moved {
		fmt.Fprintf(&b, "\n- Source %s (%s) now belongs to fleet %s (%s).", s.Name, s.Key, s.FleetName, s.Fleet)
		if s.GitHub {
			b.WriteString(" The GitHub source went to the default fleet for now.")
		}
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
	if m.ClearedResume != "" {
		fmt.Fprintf(&b, "\n- Autopilot's resume source named %s, which is gone, so it was cleared.", m.ClearedResume)
	}
	return title, b.String()
}
