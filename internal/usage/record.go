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
				continue
			}
			sum.Tokens = sum.Tokens.Add(b.Figures.Tokens)
			if sum.Money != nil && b.Figures.Money != nil {
				if m, same := sum.Money.Add(*b.Figures.Money); same {
					sum.Money = &m
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
