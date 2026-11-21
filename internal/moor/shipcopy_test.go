package moor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type shipFiles struct{ report, inbox string }

func seedShipFiles(t *testing.T) shipFiles {
	t.Helper()
	home := t.TempDir()
	f := shipFiles{report: filepath.Join(home, "reports", "s.json"), inbox: filepath.Join(home, "inbox", "s")}
	for _, d := range []string{filepath.Dir(f.report), f.inbox} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAShipCopyCarriesTheReportAndTheItemsWithTheirAnswers(t *testing.T) {
	f, checkout := seedShipFiles(t), t.TempDir()
	put(t, f.report, `{"result":"done"}`)
	put(t, filepath.Join(f.inbox, "i1.json"), "ITEM")
	put(t, filepath.Join(f.inbox, "i1.answer.json"), "ANSWER")
	put(t, filepath.Join(f.inbox, "notes.txt"), "NOT JSON")
	if err := WriteShipCopy(checkout, PlanShipCopy(f.report, f.inbox)); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		LastReportPath(checkout):                                 `{"result":"done"}`,
		filepath.Join(InboxCopyPath(checkout), "i1.json"):        "ITEM",
		filepath.Join(InboxCopyPath(checkout), "i1.answer.json"): "ANSWER",
	} {
		if b, _ := os.ReadFile(path); string(b) != want {
			t.Errorf("%s = %q, want %q", path, b, want)
		}
	}
	if _, err := os.Stat(filepath.Join(InboxCopyPath(checkout), "notes.txt")); err == nil {
		t.Error("a file that is not .json was copied")
	}
}

func TestAShipCopySkipsALinkADirectoryAndAnOversizedFile(t *testing.T) {
	f, checkout := seedShipFiles(t), t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	put(t, outside, "SECRET")
	put(t, f.report, strings.Repeat("x", maxCopiedFile+1))
	put(t, filepath.Join(f.inbox, "big.json"), strings.Repeat("x", maxCopiedFile+1))
	put(t, filepath.Join(f.inbox, "ok.json"), "OK")
	if err := os.Symlink(outside, filepath.Join(f.inbox, "link.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.inbox, "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteShipCopy(checkout, PlanShipCopy(f.report, f.inbox)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LastReportPath(checkout)); err == nil {
		t.Error("an oversized report was copied")
	}
	ents, _ := os.ReadDir(InboxCopyPath(checkout))
	if len(ents) != 1 || ents[0].Name() != "ok.json" {
		t.Errorf("inbox copy = %v, want only ok.json", ents)
	}
}

func TestAShipCopySkipsALinkAsTheReport(t *testing.T) {
	f, checkout := seedShipFiles(t), t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	put(t, outside, "SECRET")
	if err := os.Symlink(outside, f.report); err != nil {
		t.Fatal(err)
	}
	if err := WriteShipCopy(checkout, PlanShipCopy(f.report, f.inbox)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LastReportPath(checkout)); err == nil {
		t.Error("a linked report was copied")
	}
}

func TestAShipCopyRefusesALinkPlantedAtTheInboxCopy(t *testing.T) {
	f, checkout := seedShipFiles(t), t.TempDir()
	put(t, filepath.Join(f.inbox, "i1.json"), "ITEM")
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, ".admiral", "untrusted"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, InboxCopyPath(checkout)); err != nil {
		t.Fatal(err)
	}
	if err := WriteShipCopy(checkout, PlanShipCopy(f.report, f.inbox)); err == nil {
		t.Error("a link at the inbox copy was followed, want the copy refused")
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Errorf("a file was written through the link: %v", ents)
	}
}

func TestAShipCopyRemovesTheEarlierRoundsCopiesWhenTheSourcesAreGone(t *testing.T) {
	f, checkout := seedShipFiles(t), t.TempDir()
	put(t, f.report, "R")
	put(t, filepath.Join(f.inbox, "i1.json"), "ITEM")
	if err := WriteShipCopy(checkout, PlanShipCopy(f.report, f.inbox)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(f.report)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.inbox); err != nil {
		t.Fatal(err)
	}
	if err := WriteShipCopy(checkout, PlanShipCopy(f.report, f.inbox)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{LastReportPath(checkout), InboxCopyPath(checkout)} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s outlived its source", p)
		}
	}
}
