package crew

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

const sharedCommit = "0123456789abcdef0123456789abcdef01234567"

// sharedSidecar is a readable sidecar for member text; extra is spliced in before the closing brace, and tag is raw JSON.
func sharedSidecar(member, tag, extra string) string {
	sum := sha256.Sum256([]byte(member))
	return fmt.Sprintf(`{"version":1,"host":"github.com","repo":"octocat/admiral-crew","path":"crew/scout.md","commit":%q,"content_sha256":%q,"tag":%s%s}`,
		sharedCommit, hex.EncodeToString(sum[:]), tag, extra)
}

// sharedOf resolves one member named scout beside the given sidecar bytes and returns its `shared` value and the warnings.
func sharedOf(t *testing.T, sidecar any) (any, []diag, bool) {
	t.Helper()
	dir := layerDir(t, "repo", map[string]any{
		"scout.md":          memberText("scout", "scribe", "", "Body."),
		"scout.shared.json": sidecar,
	})
	got := resolveJSON(t, repoOnly(dir))
	items, _ := got["members"].([]any)
	if len(items) != 1 {
		t.Fatalf("members = %v, want only scout: the sidecar is never a member", members(t, got))
	}
	m := items[0].(map[string]any)
	v, present := m["shared"]
	return v, diags(t, got, "warnings"), present
}

func wantUnreadable(t *testing.T, sidecar any) {
	t.Helper()
	v, warnings, present := sharedOf(t, sidecar)
	if !present || v != nil {
		t.Errorf("shared = %v (present %v), want an explicit null", v, present)
	}
	wantWarnings := []diag{d("repo", "scout.shared.json", "shared-unreadable", nil)}
	if len(warnings) != 1 || warnings[0]["code"] != "shared-unreadable" || warnings[0]["file"] != "scout.shared.json" {
		t.Errorf("warnings = %s, want %s", pretty(warnings), pretty(wantWarnings))
	}
}

func TestReadableSidecarGivesSharedAttached(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	v, warnings, _ := sharedOf(t, sharedSidecar(member, `"v1.2"`, ""))
	want := map[string]any{"owner": "octocat", "repo": "admiral-crew", "commit": sharedCommit, "tag": "v1.2", "detached": false}
	if fmt.Sprint(v) != fmt.Sprint(want) {
		t.Errorf("shared = %v, want %v", v, want)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %s, want none", pretty(warnings))
	}
}

func TestSidecarWithAnEditedMemberIsDetached(t *testing.T) {
	v, _, _ := sharedOf(t, sharedSidecar("not the member's bytes", `null`, ""))
	m, _ := v.(map[string]any)
	if m == nil || m["detached"] != true || m["tag"] != nil {
		t.Errorf("shared = %v, want detached true and tag null", v)
	}
}

func TestSidecarWithInvalidUTF8IsUnreadable(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	wantUnreadable(t, []byte(sharedSidecar(member, `null`, `,"note":"caf`)+"\xe9\"}"))
}

func TestSidecarWithNonFiniteNumbersIsUnreadable(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	for _, lit := range []string{"Infinity", "-Infinity", "NaN"} {
		t.Run(lit, func(t *testing.T) {
			wantUnreadable(t, sharedSidecar(member, `null`, `,"extra":`+lit))
		})
	}
}

func TestSidecarVersionMustBeTheIntegerOne(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	good := sharedSidecar(member, `null`, "")
	for _, version := range []string{"1.0", "1e0", "true", "2", `"1"`} {
		t.Run(version, func(t *testing.T) {
			wantUnreadable(t, strings.Replace(good, `"version":1`, `"version":`+version, 1))
		})
	}
}

func TestSidecarWithAnIntegerOver4300DigitsIsUnreadableEvenInAnUnknownField(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	wantUnreadable(t, sharedSidecar(member, `null`, `,"big":`+strings.Repeat("9", 4301)))
}

func TestSidecarWithAByteOrderMarkIsUnreadable(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	wantUnreadable(t, append([]byte("\xef\xbb\xbf"), sharedSidecar(member, `null`, "")...))
}

func TestSidecarWithALoneSurrogateInTagIsUnreadable(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	for _, tag := range []string{`"v1\ud800"`, `"\udc00v1"`} {
		t.Run(tag, func(t *testing.T) { wantUnreadable(t, sharedSidecar(member, tag, "")) })
	}
}

func TestSidecarWithAPairedSurrogateInTagIsReadable(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	v, warnings, _ := sharedOf(t, sharedSidecar(member, `"v1😀"`, ""))
	if v == nil || len(warnings) != 0 {
		t.Errorf("shared = %v, warnings = %s, want a readable sidecar", v, pretty(warnings))
	}
}

func TestSidecarWithALoneSurrogateInAnUnknownFieldStaysReadable(t *testing.T) {
	member := memberText("scout", "scribe", "", "Body.")
	v, warnings, _ := sharedOf(t, sharedSidecar(member, `null`, `,"note":"\ud800"`))
	m, _ := v.(map[string]any)
	if m == nil || m["owner"] != "octocat" || len(warnings) != 0 {
		t.Errorf("shared = %v, warnings = %s, want attached and no warning", v, pretty(warnings))
	}
}
