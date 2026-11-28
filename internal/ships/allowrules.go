package ships

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/permlog"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/standing"
)

// proposeAllowRules turns the prompts a person allowed by hand in the ship's round into allow rule cards. It reads the
// permission log, not the report, which only gives reason lines. A failure is logged and never holds up the ended record.
func (s *Service) proposeAllowRules(sess sessionhost.Session, report []byte) {
	recs, err := permlog.ReadRound(s.Launcher.Home, sess.Name)
	if err != nil {
		fmt.Fprintf(s.log(), "admiral: ship %s: its permission log cannot be read: %v\n", sess.Name, err)
		return
	}
	if len(recs) == 0 || recs[0].Fleet == "" {
		return
	}
	fleet := recs[0].Fleet
	var restricted bool
	var tier standing.Tier
	if s.EventShip != nil {
		var fromEvent bool
		var tierText string
		if restricted, fromEvent, tierText, err = s.EventShip(fleet, sess.Name); err != nil {
			fmt.Fprintf(s.log(), "admiral: ship %s: no allow rules proposed, its launch cannot be read: %v\n", sess.Name, err)
			return
		}
		if fromEvent {
			if tier, err = parseTier(tierText); err != nil {
				fmt.Fprintf(s.log(), "admiral: ship %s: no allow rules proposed, its trigger's tier is unreadable: %v\n", sess.Name, err)
				return
			}
		}
	}
	prompts := make([]allowrules.Prompt, len(recs))
	for i, r := range recs {
		prompts[i] = allowrules.Prompt{Record: r, Restricted: restricted, Tier: tier}
	}
	have, err := s.Launcher.AllowRules().List(fleet)
	if err != nil {
		fmt.Fprintf(s.log(), "admiral: ship %s: the fleet's allow rules cannot be read: %v\n", sess.Name, err)
		return
	}
	cands, _ := allowrules.Candidates(prompts, map[string]allowrules.Fleet{fleet: have}, s.Launcher.Home)
	if err := inbox.WriteAllowRuleProposals(s.Launcher.Home, fleet, cands, inbox.AllowRuleReasons(report)); err != nil {
		fmt.Fprintf(s.log(), "admiral: ship %s: allow rule proposals were not written: %v\n", sess.Name, err)
	}
}

// parseTier reads a trigger's "T0" to "T3"; anything else is an error, so the caller proposes nothing.
func parseTier(s string) (standing.Tier, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(s, "T"))
	if err != nil || !strings.HasPrefix(s, "T") || n < 0 || n > 4 {
		return 0, fmt.Errorf("tier %q", s)
	}
	return standing.Tier(n), nil
}
