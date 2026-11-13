// Package shiptitle gives a ship launched without an order a title and a link, worked out once
// from its first prompt: a GitHub pull request's or issue's own title, else Claude Code's name for
// the session, else the titler's, a small-model judge call.
package shiptitle

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/shipconv"
	"github.com/jbuffin/admiral/internal/ships"
)

const (
	maxTitle      = 120
	maxPrompt     = 4000
	titlerTimeout = time.Minute
	titlerBudget  = 0.05
	pollEvery     = 2 * time.Second
	// Claude Code names a session a little after its first prompt.
	harnessTitleWait = time.Minute
	concurrent       = 3
)

//go:embed titler.md
var titlerPrompt string

const schema = `{"type":"object","properties":{` +
	`"title":{"type":"string","maxLength":200},` +
	`"link":{"type":"integer"}` +
	`},"required":["title","link"],"additionalProperties":false}`

type Service struct {
	List func() []ships.Ship
	Seed func(session string) (shipconv.Seed, bool)
	// Set writes the ship's options; an empty value is left out by the caller, not unset.
	Set func(ctx context.Context, session string, opts map[string]string) error
	// ItemTitle is the title of the GitHub pull request or issue link points into, on one of the fleet's hosts.
	ItemTitle func(ctx context.Context, fleet, link string) (title string, ok bool)
	// Judge runs the titler on the fleet's judge harness.
	Judge func(ctx context.Context, fleet string, call judge.Call) (judge.Reply, error)

	// Wait is how long a Claude ship's own title is waited for; zero means a minute.
	Wait time.Duration

	mu      sync.Mutex
	working map[string]bool
	done    map[string]bool // by session and launch, so a ship whose write failed is not retried each tick
	slots   chan struct{}
}

// Titling reports whether the ship's title is being worked out.
func (s *Service) Titling(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.working[session]
}

func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		s.Poll(ctx, &wg)
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}

// Poll starts the titling of each ship whose first prompt is known and whose title is not settled.
func (s *Service) Poll(ctx context.Context, wg *sync.WaitGroup) {
	s.mu.Lock()
	if s.working == nil {
		s.working, s.done, s.slots = map[string]bool{}, map[string]bool{}, make(chan struct{}, concurrent)
	}
	s.mu.Unlock()
	for _, sh := range s.List() {
		if sh.Titled || sh.Source != "" || sh.Status == ships.StatusMoored || !ships.HasConversation(sh.Harness) {
			continue
		}
		seed, ok := s.Seed(sh.Session)
		if !ok || strings.TrimSpace(seed.FirstPrompt) == "" {
			continue
		}
		key := sh.Session + "\x00" + sh.Launched
		s.mu.Lock()
		if s.working[sh.Session] || s.done[key] {
			s.mu.Unlock()
			continue
		}
		s.working[sh.Session] = true
		s.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.slots <- struct{}{}
			defer func() { <-s.slots }()
			s.title(ctx, sh, seed)
			s.mu.Lock()
			s.done[key] = true
			delete(s.working, sh.Session)
			s.mu.Unlock()
		}()
	}
}

func (s *Service) title(ctx context.Context, sh ships.Ship, seed shipconv.Seed) {
	title, link := s.resolve(ctx, sh, seed)
	if ctx.Err() != nil {
		return
	}
	opts := map[string]string{"titled": "1"}
	if t := clean(title); t != "" {
		opts["title"] = t
	}
	if sh.URL == "" && link != "" {
		opts["link"] = link
	}
	if err := s.Set(ctx, sh.Session, opts); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its title cannot be written: %v\n", sh.Name, err)
	}
}

// resolve follows the order: a GitHub item's title, Claude Code's title, the titler's, the first line.
func (s *Service) resolve(ctx context.Context, sh ships.Ship, seed shipconv.Seed) (title, link string) {
	links := Links(seed.FirstPrompt)
	link = sh.URL
	if link == "" && len(links) == 1 {
		link = links[0]
	}
	var asked *pick
	if link == "" && len(links) > 1 {
		if p, ok := s.ask(ctx, sh.Fleet, seed.FirstPrompt, links); ok {
			asked, link = &p, p.link
		}
	}
	if link != "" && s.ItemTitle != nil {
		if t, ok := s.ItemTitle(ctx, sh.Fleet, link); ok {
			return t, link
		}
	}
	if sh.Harness == "claude" {
		if t := s.harnessTitle(ctx, sh.Session); t != "" {
			return t, link
		}
	}
	if asked == nil {
		if p, ok := s.ask(ctx, sh.Fleet, seed.FirstPrompt, links); ok {
			asked = &p
			if link == "" {
				link = p.link
			}
		}
	}
	if asked != nil && strings.TrimSpace(asked.title) != "" {
		return asked.title, link
	}
	return firstLine(seed.FirstPrompt), link
}

func (s *Service) harnessTitle(ctx context.Context, session string) string {
	wait := s.Wait
	if wait <= 0 {
		wait = harnessTitleWait
	}
	deadline := time.Now().Add(wait)
	for {
		if seed, ok := s.Seed(session); ok && strings.TrimSpace(seed.Title) != "" {
			return seed.Title
		}
		if !time.Now().Before(deadline) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(min(pollEvery, time.Until(deadline))):
		}
	}
}

type pick struct{ title, link string }

// ask runs the titler. Its link is a number into links, so it can only name a link the prompt holds.
func (s *Service) ask(ctx context.Context, fleet, prompt string, links []string) (pick, bool) {
	if s.Judge == nil {
		return pick{}, false
	}
	reply, err := s.Judge(ctx, fleet, judge.Call{Kind: judge.Lookout, Prompt: titlerInput(prompt, links),
		Schema: []byte(schema), BudgetUSD: titlerBudget, Timeout: titlerTimeout})
	if err != nil {
		return pick{}, false
	}
	var p pick
	p.title, _ = reply.Fields["title"].(string)
	if n, ok := reply.Fields["link"].(float64); ok && n >= 1 && int(n) <= len(links) && n == float64(int(n)) {
		p.link = links[int(n)-1]
	}
	return p, true
}

// titlerInput fences the prompt in a run of backticks longer than any inside it.
func titlerInput(prompt string, links []string) string {
	if utf8.RuneCountInString(prompt) > maxPrompt {
		prompt = string([]rune(prompt)[:maxPrompt]) + "…"
	}
	fence, run := "```", 0
	for _, r := range prompt {
		if r == '`' {
			run++
			if run >= len(fence) {
				fence += "`"
			}
		} else {
			run = 0
		}
	}
	var b strings.Builder
	b.WriteString(titlerPrompt)
	b.WriteString("\nThe first message:\n\n" + fence + "\n" + prompt + "\n" + fence + "\n\nThe links:\n\n")
	if len(links) == 0 {
		b.WriteString("(none)\n")
	}
	for i, l := range links {
		fmt.Fprintf(&b, "%d. %s\n", i+1, l)
	}
	return b.String()
}

// firstLine leaves out the links, which the ship shows as its link.
func firstLine(prompt string) string {
	for _, l := range strings.Split(linkRE.ReplaceAllString(prompt, ""), "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			return l
		}
	}
	return ""
}

func clean(title string) string {
	t := strings.TrimSpace(launcher.CleanTitle(title))
	if utf8.RuneCountInString(t) > maxTitle {
		t = strings.TrimSpace(string([]rune(t)[:maxTitle])) + "…"
	}
	return t
}
