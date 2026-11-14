package moor

import "time"

// Detail is one pull request in full, as the PR tab shows it.
type Detail struct {
	Number        int             `json:"number"`
	URL           string          `json:"url"`
	Title         string          `json:"title"`
	Author        DetailActor     `json:"author"`
	Reviewing     bool            `json:"reviewing"`
	Body          string          `json:"body"`
	State         string          `json:"state"`
	Draft         bool            `json:"draft"`
	Head          string          `json:"head"`
	Base          string          `json:"base"`
	DefaultBranch string          `json:"default_branch"`
	Review        string          `json:"review"`
	Checks        string          `json:"checks"`
	Mergeable     string          `json:"mergeable"`
	MergeState    string          `json:"merge_state"`
	FailingChecks []DetailCheck   `json:"failing_checks"`
	Reviews       []DetailReview  `json:"reviews"`
	Requested     []DetailActor   `json:"requested"`
	Threads       []DetailThread  `json:"threads"`
	Comments      []DetailComment `json:"comments"`
	Stack         *DetailStack    `json:"stack"`
	// AnswerAll is the command Answer all reviews would send; set by the ships service, only when the whole stack has feedback.
	AnswerAll string `json:"answer_all,omitempty"`
}

// DetailActor is who wrote something; Bot is true for a GraphQL Bot.
type DetailActor struct {
	Login string `json:"login"`
	Bot   bool   `json:"bot"`
}

type DetailCheck struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type DetailReview struct {
	Author DetailActor `json:"author"`
	State  string      `json:"state"`
}

type DetailComment struct {
	Author    DetailActor `json:"author"`
	Body      string      `json:"body"`
	URL       string      `json:"url"`
	CreatedAt time.Time   `json:"created_at"`
}

type DetailThread struct {
	Path     string          `json:"path"`
	Line     int             `json:"line"`
	Resolved bool            `json:"resolved"`
	Comments []DetailComment `json:"comments"`
}

// DetailStack is the GitHub stack a PR sits in; nil when it is in none.
type DetailStack struct {
	Number  int                `json:"number"`
	Size    int                `json:"size"`
	Base    string             `json:"base"`
	URL     string             `json:"url"`
	Entries []DetailStackEntry `json:"entries"`
}

type DetailStackEntry struct {
	Position int    `json:"position"`
	Number   int    `json:"number"`
	URL      string `json:"url"`
	State    string `json:"state"`
	Head     string `json:"head"`
}
