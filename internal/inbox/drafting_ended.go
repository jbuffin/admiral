package inbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// KindDraftingEnded lives only in the AdmiralSession directory: a drafting ship ended without filing a campaign the fleet follows. The engineer can only remove it.
const KindDraftingEnded = "drafting_ended"

// DraftingEnded is the item's drafting_ended object. The ship's name is data, not part of the title, so the client shows it as text.
// Transcript, StartCmd and ConfigDir are the ship's conversation binding as Admiral checked it at the end, read only by the conversation route and never sent to a client.
type DraftingEnded struct {
	Ship       string `json:"ship"`
	Name       string `json:"name"`
	Fleet      string `json:"fleet"`
	Harness    string `json:"harness"`
	Transcript string `json:"transcript,omitempty"`
	StartCmd   string `json:"start_cmd,omitempty"`
	ConfigDir  string `json:"config_dir,omitempty"`
}

// DraftingEnded is the object of a drafting_ended item.
func (it Item) DraftingEnded() (DraftingEnded, bool) {
	var d DraftingEnded
	return d, json.Unmarshal(it.fields[KindDraftingEnded], &d) == nil && d.Ship != "" && d.Name != "" && d.Fleet != ""
}

// WriteDraftingEnded posts the item for a drafting ship that ended with no campaign followed and returns its id.
func WriteDraftingEnded(home string, d DraftingEnded) (string, error) {
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	id := "drafting-ended-" + time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(rnd[:])
	err := publishAdmiralItem(home, id, map[string]any{"kind": KindDraftingEnded, "title": d.Name + " ended without filing a campaign", KindDraftingEnded: d})
	return id, err
}

// withoutBinding is the object as a client may see it.
func (d DraftingEnded) withoutBinding() DraftingEnded {
	d.Transcript, d.StartCmd, d.ConfigDir = "", "", ""
	return d
}

// serveDraftingConversation answers with the ship's conversation, read through the binding recorded when it ended.
func (s *Service) serveDraftingConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.DraftingConversation == nil || !idPattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "no drafting item "+id)
		return
	}
	it, err := s.open(AdmiralSession, id)
	d, ok := it.DraftingEnded()
	if err != nil || it.Kind != KindDraftingEnded || !ok {
		writeError(w, http.StatusNotFound, "no drafting item "+id)
		return
	}
	status, body := s.DraftingConversation(d, url.Values{"before": r.URL.Query()["before"]}.Encode())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
