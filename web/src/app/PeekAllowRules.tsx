import { useState } from 'preact/hooks';
import type { Entry } from './inbox';
import { ruleText } from './AllowRulesPage';
import { Button } from './Button';
import { Flag } from './Flag';
import { harnessName } from './harnessName';
import type { Acting } from './PeekItem';
import type { AllowRuleCard } from './api';

type Decision = 'add' | 'skip';

// Send shows its spinner from acting.busy, not a pending key: a held spinner would keep Send live after a 409 locks it.
// A ship wrote the reason, so it renders as React text and never as markdown or HTML.
export function AllowRulesBody({ entry, acting, fleet }: { entry: Entry; acting: Acting; fleet: string }) {
  const cards: AllowRuleCard[] = Array.isArray(entry.item?.allow_rules?.cards) ? (entry.item.allow_rules.cards as AllowRuleCard[]) : [];
  const [decisions, setDecisions] = useState<Record<string, Decision>>({});
  const off = acting.busy || acting.locked;
  const decided = cards.filter((c) => decisions[c.id]).length;
  const pick = (id: string, d: Decision) => {
    acting.clear();
    setDecisions((m) => ({ ...m, [id]: d }));
  };
  const send = () => {
    if (decided < cards.length) return;
    const added = cards.filter((c) => decisions[c.id] === 'add').length;
    const body = { cards: Object.fromEntries(cards.map((c) => [c.id, { decision: decisions[c.id] }])) };
    return acting.run('allow-rules', body, () => `Allow rules sent: ${added} added, ${cards.length - added} skipped`);
  };
  return (
    <>
      {cards.map((c, idx) => {
        const i = idx + 1;
        const ships = [...new Set((c.prompts ?? []).map((p) => p.ship).filter(Boolean))];
        return (
          <section key={c.id} class="inbox-finding" aria-label={`Allow rule card ${i}`}>
            <span class="inbox-eyebrow">
              <Flag harness={c.harness} size="row" label={harnessName(c.harness)} /> {c.every_repo || !c.repo ? `applies to every repo in ${fleet}` : c.repo}
            </span>
            <code class="label set-text">{ruleText(c)}</code>
            {c.count > 1 && <span class="inbox-hint">Asked {c.count} times</span>}
            {ships.length > 0 && <span class="inbox-hint">From {ships.join(', ')}</span>}
            {c.reason && <p class="inbox-why" style={{ whiteSpace: 'pre-wrap' }}>{c.reason}</p>}
            <div class="inbox-send-row" role="group" aria-label={`Decision for rule ${i}`}>
              <Button label="Add" pressed={decisions[c.id] === 'add'} disabled={off} onClick={() => pick(c.id, 'add')} />
              <Button label="Skip" pressed={decisions[c.id] === 'skip'} disabled={off} onClick={() => pick(c.id, 'skip')} />
            </div>
          </section>
        );
      })}
      {acting.error && (
        <p class="inbox-error" role="alert">
          {acting.error}
        </p>
      )}
      <div class="inbox-send-row">
        <Button label="Send" variant="primary" busy={acting.busy} disabled={decided < cards.length || off} onClick={() => void send()} />
        <span class="inbox-progress">
          {decided} of {cards.length} decided
        </span>
      </div>
    </>
  );
}
