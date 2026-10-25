package cli

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The line form turns into the JSON question, and a JSON body passes through.
func TestAskFormLinesAndJSON(t *testing.T) {
	got, err := askForm([]byte("title: Where?\n\nquestion: panel | Which panel?\nmulti: true\noption: Freeform | your own list\noption: Work queue\nquestion: b | Sure?\noption: Yes\n"))
	if err != nil {
		t.Fatal(err)
	}
	var have, want any
	if err := json.Unmarshal(got, &have); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"title":"Where?","questions":[
		{"id":"panel","text":"Which panel?","multi":true,"options":[{"label":"Freeform","description":"your own list"},{"label":"Work queue"}]},
		{"id":"b","text":"Sure?","multi":false,"options":[{"label":"Yes"}]}]}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("got %s", got)
	}
	in := []byte(` {"title": "T", "questions": []}`)
	if out, err := askForm(in); err != nil || string(out) != string(in) {
		t.Errorf("JSON should pass through: %s, %v", out, err)
	}
	for _, bad := range []string{"option: x", "colour: red", "no key here"} {
		if _, err := askForm([]byte("title: T\n" + bad)); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}
