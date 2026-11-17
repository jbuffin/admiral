package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// Red-phase stubs: the tests compile against these names and fail until a module's page is implemented.

const (
	maxPageRows   = 5000
	maxPageGroups = 100
	maxChips      = 10
	maxActions    = 10
	maxRowDetail  = 1 << 20 // bytes
	maxTitleRunes = 200
)

var errNotBuilt = errors.New("not implemented")

type Page struct{}
type RowDetail struct{}
type MessageKind struct{}
type PageView struct{}

func parsePage(raw json.RawMessage) (Page, error)           { return Page{}, errNotBuilt }
func parseRowDetail(raw json.RawMessage) (RowDetail, error) { return RowDetail{}, errNotBuilt }
func parseMessageKinds(raw json.RawMessage) []MessageKind   { return nil }

func (r *runner) pullPage(ctx context.Context, p conn) {}
func (r *runner) Page() (PageView, bool)               { return PageView{}, false }
func (r *runner) rowDetail(ctx context.Context, id string, include []string) (RowDetail, error) {
	return RowDetail{}, errNotBuilt
}

func (s *Service) serveNotBuilt(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
