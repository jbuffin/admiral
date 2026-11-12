package usage

// Boat is one crew boat of a ship; Figures is nil when its transcript gave none.
type Boat struct {
	Name, Harness string
	Figures       *Figures
}

func tokensJSON(t Tokens) map[string]any {
	return map[string]any{"input": t.Input, "output": t.Output, "reasoning": t.Reasoning, "cache_read": t.CacheRead, "cache_write": t.CacheWrite}
}

func moneyJSON(m *Money) any {
	if m == nil {
		return nil
	}
	out := map[string]any{"amount": m.Amount, "basis": string(m.Basis), "incomplete": m.Incomplete}
	if m.PremiumRequestCost != nil {
		out["premium_requests"] = *m.PremiumRequestCost
	}
	return out
}

func figuresJSON(f *Figures) (tokens, money any) {
	if f == nil {
		return nil, nil
	}
	return tokensJSON(f.Tokens), moneyJSON(f.Money)
}

// ExitFields are the fields of a `usage` outcome record. The ship's totals are its own figures plus
// its crew boats'; tokens add across harnesses, money only within one basis, so a boat on another
// basis keeps its money under crew alone. A ship with no figures of its own has null totals,
// however much its crew used, and a zero round or an empty crew is left out.
func ExitFields(fleet, harness string, round int, own *Figures, crew []Boat) map[string]any {
	out := map[string]any{"fleet": fleet, "harness": harness}
	if round > 0 {
		out["round"] = round
	}
	var total *Figures
	if own != nil {
		sum := Figures{Tokens: own.Tokens}
		if own.Money != nil {
			m := *own.Money
			sum.Money = &m
		}
		for _, b := range crew {
			if b.Figures == nil {
				// An unread boat may have spent money the total does not hold.
				if sum.Money != nil {
					sum.Money.Incomplete = true
				}
				continue
			}
			sum.Tokens = sum.Tokens.Add(b.Figures.Tokens)
			if sum.Money != nil && b.Figures.Money != nil {
				if m, same := sum.Money.Add(*b.Figures.Money); same {
					sum.Money = &m
				} else {
					sum.Money.Incomplete = true
				}
			}
		}
		total = &sum
	}
	out["tokens"], out["money"] = figuresJSON(total)
	if len(crew) > 0 {
		boats := make([]map[string]any, 0, len(crew))
		for _, b := range crew {
			t, m := figuresJSON(b.Figures)
			boats = append(boats, map[string]any{"name": b.Name, "harness": b.Harness, "tokens": t, "money": m})
		}
		out["crew"] = boats
	}
	return out
}

// SumFigures adds readings of separate transcripts of one process, such as a Codex boat's rollouts. Nil
// entries are skipped, and none at all gives nil. Money adds only within the first basis seen.
func SumFigures(parts []*Figures) *Figures {
	var out *Figures
	for _, p := range parts {
		if p == nil {
			continue
		}
		if out == nil {
			c := Figures{Tokens: p.Tokens}
			if p.Money != nil {
				m := *p.Money
				c.Money = &m
			}
			out = &c
			continue
		}
		out.Tokens = out.Tokens.Add(p.Tokens)
		switch {
		case p.Money == nil:
		case out.Money == nil:
			m := *p.Money
			out.Money = &m
		default:
			if m, same := out.Money.Add(*p.Money); same {
				out.Money = &m
			} else {
				out.Money.Incomplete = true
			}
		}
	}
	return out
}
