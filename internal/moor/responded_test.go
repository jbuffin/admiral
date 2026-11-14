package moor

import (
	"testing"
	"time"
)

func TestRespondedMeansTheAuthorPushedPastOrRepliedInAThreadOfTheViewersReview(t *testing.T) {
	const me, author = "jbuffin", "dana"
	at := func(m int) time.Time { return wT0.Add(time.Duration(m) * time.Minute) }
	rev := func(id, who, state, commit string, m int) Review {
		r := wRev(id, wUser(who), state, "", at(m))
		r.Commit = commit
		return r
	}
	thread := func(id, thr string, who string, m int) Comment {
		c := wCmt(id, wUser(who), "text", at(m))
		c.Thread = thr
		return c
	}
	topLevel := func(who string, m int) Comment { return wCmt("C1", wUser(who), "text", at(m)) }

	for _, c := range []struct {
		name string
		edit func(*Status)
		want bool
	}{
		{"no viewer review and no viewer thread comment", func(st *Status) {}, false},
		{"the viewer reviewed the head", func(st *Status) { st.Reviews = []Review{rev("R1", me, "COMMENTED", "abc", 1)} }, false},
		{"the head moved past the viewer's latest review", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", me, "COMMENTED", "abc", 1)}
		}, true},
		{"a later viewer review on the new head clears it", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", me, "COMMENTED", "abc", 1), rev("R2", me, "APPROVED", "def", 5)}
		}, false},
		{"the viewer's latest review counts even listed before an older one", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R2", me, "APPROVED", "def", 5), rev("R1", me, "COMMENTED", "abc", 1)}
		}, false},
		{"a pending viewer review is ignored", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", me, "COMMENTED", "abc", 1), rev("R2", me, "PENDING", "def", 5)}
		}, true},
		{"a pending review alone is no review", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", me, "PENDING", "abc", 1)}
		}, false},
		{"a dismissed viewer review is ignored", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", me, "COMMENTED", "abc", 1), rev("R2", me, "DISMISSED", "def", 5)}
		}, true},
		{"a review GitHub gave no commit for never reads as pushed past", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", me, "COMMENTED", "", 1)}
		}, false},
		{"another reviewer's review of an old commit is not the viewer's", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", "erin", "COMMENTED", "abc", 1)}
		}, false},
		{"logins compare case-insensitively", func(st *Status) {
			st.Head = "def"
			st.Reviews = []Review{rev("R1", "JBuffin", "COMMENTED", "abc", 1)}
		}, true},
		{"the author replied in a thread the viewer commented in", func(st *Status) {
			st.ThreadComments = []Comment{thread("T1", "TH1", me, 1), thread("T2", "TH1", author, 3)}
		}, true},
		{"the author's comment in a thread without the viewer", func(st *Status) {
			st.ThreadComments = []Comment{thread("T1", "TH1", "erin", 1), thread("T2", "TH1", author, 3), thread("T3", "TH2", me, 0)}
		}, false},
		{"the author spoke in another thread than the viewer's", func(st *Status) {
			st.ThreadComments = []Comment{thread("T1", "TH1", me, 1), thread("T2", "TH2", author, 3)}
		}, false},
		{"the author spoke before the viewer in the thread", func(st *Status) {
			st.ThreadComments = []Comment{thread("T1", "TH1", author, 1), thread("T2", "TH1", me, 3)}
		}, false},
		{"the viewer replies after the author in that thread", func(st *Status) {
			st.ThreadComments = []Comment{thread("T1", "TH1", me, 1), thread("T2", "TH1", author, 3), thread("T3", "TH1", me, 5)}
		}, false},
		{"the author's top-level PR comment does not count", func(st *Status) {
			st.Reviews = []Review{rev("R1", me, "COMMENTED", "abc", 1)}
			st.Comments = []Comment{topLevel(author, 3)}
		}, false},
		{"a reply in one thread counts although another thread is settled", func(st *Status) {
			st.ThreadComments = []Comment{thread("T1", "TH1", me, 1), thread("T2", "TH1", author, 3), thread("T3", "TH2", me, 4), thread("T4", "TH2", author, 2)}
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := Status{State: "OPEN", Author: author, Viewer: me, Head: "abc"}
			c.edit(&st)
			if got := Responded(st); got != c.want {
				t.Errorf("Responded = %v, want %v for %+v", got, c.want, st)
			}
		})
	}
}
