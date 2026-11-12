package conversation_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

type servedQuestion struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Question struct {
		Header      string `json:"header"`
		Text        string `json:"text"`
		MultiSelect bool   `json:"multiSelect"`
		Answer      string `json:"answer"`
		Pending     bool   `json:"pending"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"question"`
}

// served reads the items the way a client gets them: as JSON.
func served(t *testing.T, items []conversation.Item) []servedQuestion {
	t.Helper()
	var out []servedQuestion
	for _, it := range items {
		raw, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		var q servedQuestion
		if err := json.Unmarshal(raw, &q); err != nil {
			t.Fatal(err)
		}
		if q.Kind == "question" {
			out = append(out, q)
		}
	}
	return out
}

const claudeBatchAsk = `{"type":"assistant","uuid":"b1","message":{"content":[{"type":"tool_use","id":"tb1","name":"AskUserQuestion","input":{"questions":[` +
	`{"question":"Which **database**?","header":"Database","multiSelect":false,"options":[{"label":"Postgres","description":"The default, ` + "`pg`" + `."},{"label":"SQLite","description":"One file."}]},` +
	`{"question":"Which checks run?","header":"Checks","multiSelect":true,"options":[{"label":"Lint","description":"Static analysis."},{"label":"Unit","description":"Fast tests."},{"label":"E2E","description":"Browser tests."}]}` +
	`]}}]}}` + "\n"

const claudeBatchResult = `{"type":"user","uuid":"b2","message":{"content":[{"type":"tool_result","tool_use_id":"tb1","content":"Your questions have been answered."}]},` +
	`"toolUseResult":{"answers":{"Which **database**?":"SQLite","Which checks run?":"Lint, E2E"}}}` + "\n"

func TestClaudeBatchQuestionsEachCarryTheirHeaderOptionsAndAnswer(t *testing.T) {
	qs := served(t, read(t, claudeBatchAsk+claudeBatchResult))
	if len(qs) != 2 {
		t.Fatalf("%d question items, want 2 (one per question)", len(qs))
	}
	first, second := qs[0], qs[1]
	if first.Question.Header != "Database" || second.Question.Header != "Checks" {
		t.Errorf("headers = %q, %q, want Database, Checks", first.Question.Header, second.Question.Header)
	}
	if first.Question.Text != "Which **database**?" || len(first.Question.Options) != 2 ||
		first.Question.Options[0].Label != "Postgres" || first.Question.Options[0].Description != "The default, `pg`." {
		t.Errorf("first question = %+v", first.Question)
	}
	if first.Question.MultiSelect || !second.Question.MultiSelect || len(second.Question.Options) != 3 {
		t.Errorf("multiSelect = %v, %v with %d options, want false, true, 3", first.Question.MultiSelect, second.Question.MultiSelect, len(second.Question.Options))
	}
	if first.Question.Answer != "SQLite" || second.Question.Answer != "Lint, E2E" {
		t.Errorf("answers = %q, %q, want each question's own", first.Question.Answer, second.Question.Answer)
	}
	if first.Question.Pending || second.Question.Pending {
		t.Errorf("answered questions are still pending")
	}
}

func TestClaudeAnswerThatMatchesNoOptionIsTheFreeTextAnswer(t *testing.T) {
	result := `{"type":"user","uuid":"b2","message":{"content":[{"type":"tool_result","tool_use_id":"tb1","content":"ok"}]},` +
		`"toolUseResult":{"answers":{"Which **database**?":"MariaDB, please","Which checks run?":"Unit"}}}` + "\n"
	qs := served(t, read(t, claudeBatchAsk+result))
	if len(qs) != 2 || qs[0].Question.Answer != "MariaDB, please" || qs[1].Question.Answer != "Unit" {
		t.Errorf("answers = %+v, want the free text and Unit as recorded", qs)
	}
}

func TestCodexLiveQuestionsCarryTheirHeader(t *testing.T) {
	live := conversation.NewLive(conversation.Options{}, liveThread)
	feed(live, fmt.Sprintf(`{"method":"item/tool/requestUserInput","id":5,"params":{"threadId":%q,"turnId":"t","itemId":"call_h","questions":[`+
		`{"id":"a","header":"Colour","question":"Pick one","isSecret":false,"options":[{"label":"Red","description":"Warm"},{"label":"Blue","description":"Cool"}]}]}}`, liveThread))
	qs := served(t, live.Pending())
	if len(qs) != 1 || qs[0].Question.Header != "Colour" {
		t.Errorf("pending = %+v, want one question with header Colour", qs)
	}
}
