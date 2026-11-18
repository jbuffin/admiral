// PROTOTYPE, throwaway: three Add campaign modal variants, switchable via ?variant=A|B|C, on the real Campaigns + and the phone list.
// Search results, the quick draft and the drafting-ship launch are stubbed; nothing here calls the server.
import { signal } from '@preact/signals';
import { useEffect, useRef, useState } from 'preact/hooks';
import { Button } from './Button';
import { Icon } from './Icon';

type Result = { ref: string; title: string; repo: string; subs: number; closed: number; followed?: boolean; state: 'open' | 'closed' };

const ISSUES: Result[] = [
  { ref: 'jbuffin/admiral#703', repo: 'jbuffin/admiral', title: 'Map: Add a campaign from Admiral by search, free text or a drafting skill', subs: 6, closed: 3, followed: true, state: 'open' },
  { ref: 'jbuffin/admiral#715', repo: 'jbuffin/admiral', title: 'Module page for fleet plugins', subs: 4, closed: 0, state: 'open' },
  { ref: 'jbuffin/admiral#612', repo: 'jbuffin/admiral', title: 'Launch a ship on a GitHub stack', subs: 3, closed: 3, state: 'closed' },
  { ref: 'jbuffin/commodore#41', repo: 'jbuffin/commodore', title: 'Campaign handoff from a settled design', subs: 5, closed: 1, state: 'open' },
  { ref: 'jbuffin/admiral#702', repo: 'jbuffin/admiral', title: 'Put Add campaign on the Campaigns nav row and drop the duplicate header', subs: 0, closed: 0, state: 'open' },
  { ref: 'jbuffin/admiral#706', repo: 'jbuffin/admiral', title: 'How can Admiral search a fleet’s issues by text, number and URL?', subs: 0, closed: 0, state: 'closed' },
  { ref: 'jbuffin/commodore#7', repo: 'jbuffin/commodore', title: 'Campaign sheet reorders sub-issues on drop', subs: 0, closed: 0, state: 'open' },
  { ref: 'jbuffin/admiral#7', repo: 'jbuffin/admiral', title: 'Campaign list flickers on refresh', subs: 0, closed: 0, state: 'closed' },
];
const REPOS = ['jbuffin/admiral', 'jbuffin/commodore'];
const SKILLS = ['wayfinder', 'to-prd', 'grill-me'];
const HARNESSES = ['Claude Code', 'Codex'];

function search(q: string): Result[] {
  const t = q.trim().toLowerCase();
  if (!t) return ISSUES.filter((i) => i.subs > 0 && i.state === 'open');
  const num = t.match(/^#(\d+)$/)?.[1];
  const hits = num ? ISSUES.filter((i) => i.ref.endsWith(`#${num}`)) : ISSUES.filter((i) => i.title.toLowerCase().includes(t) || i.ref.includes(t));
  return [...hits.filter((i) => i.subs > 0), ...hits.filter((i) => i.subs === 0)];
}
const isRef = (q: string) => /^(https?:\/\/\S+\/issues\/\d+|[\w.-]+\/[\w.-]+#\d+)$/.test(q.trim());

// The fleet's remembered drafting skill; '' is no skill.
const remembered = signal('');
export const draftingRow = signal<string | null>(null);
const toast = signal<string | null>(null);
const say = (t: string) => {
  toast.value = t;
  setTimeout(() => toast.value === t && (toast.value = null), 3200);
};

function variantNow(): 'A' | 'B' | 'C' {
  const v = new URLSearchParams(location.search).get('variant');
  return v === 'B' || v === 'C' ? v : 'A';
}
const variant = signal(variantNow());
const NAMES = { A: 'Omnibox', B: 'Tabs', C: 'Side by side' } as const;
function setVariant(v: 'A' | 'B' | 'C') {
  const q = new URLSearchParams(location.search);
  q.set('variant', v);
  history.replaceState(history.state, '', `${location.pathname}?${q}`);
  variant.value = v;
}

export function AddCampaignPrototype({ onClose }: { onClose: () => void }) {
  const v = variant.value;
  return (
    <>
      <style>{CSS}</style>
      {v === 'A' ? <VariantA onClose={onClose} /> : v === 'B' ? <VariantB onClose={onClose} /> : <VariantC onClose={onClose} />}
      <Switcher />
    </>
  );
}

export function PrototypeToast() {
  return toast.value ? (
    <>
      <style>{CSS}</style>
      <div class="pc-toast" role="status">
        {toast.value}
      </div>
    </>
  ) : null;
}

function Switcher() {
  const order = ['A', 'B', 'C'] as const;
  const step = (d: number) => setVariant(order[(order.indexOf(variant.value) + d + 3) % 3]!);
  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if ((e.target as Element | null)?.closest?.('input, textarea, select, [contenteditable]')) return;
      if (e.key === 'ArrowLeft') step(-1);
      if (e.key === 'ArrowRight') step(1);
    };
    document.addEventListener('keydown', k);
    return () => document.removeEventListener('keydown', k);
  }, []);
  return (
    <div class="pc-switch">
      <button type="button" onClick={() => step(-1)} aria-label="Previous variant">
        ‹
      </button>
      <span>
        {variant.value} ({NAMES[variant.value]})
      </span>
      <button type="button" onClick={() => step(1)} aria-label="Next variant">
        ›
      </button>
    </div>
  );
}

function Frame({ label, wide, onClose, children }: { label: string; wide?: boolean; onClose: () => void; children: preact.ComponentChildren }) {
  return (
    <div class="backdrop launch-backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class={`sheet launch-form pc-form${wide ? ' pc-wide' : ''}`} role="dialog" aria-modal="true" aria-label={label} onKeyDown={(e) => e.key === 'Escape' && onClose()}>
        {children}
      </div>
    </div>
  );
}

function ResultRow({ r, onFollow, compact }: { r: Result; onFollow: (r: Result) => void; compact?: boolean }) {
  return (
    <li>
      <button type="button" class="pc-row" disabled={r.followed} onClick={() => onFollow(r)}>
        <span class={`pc-kind ${r.subs > 0 ? 'parent' : ''}`} aria-hidden="true">
          {r.subs > 0 ? `${r.closed}/${r.subs}` : '·'}
        </span>
        <span class="pc-t">
          <span class="pc-title">{r.title}</span>
          <span class="pc-ref mono">
            {r.ref}
            {r.state === 'closed' ? ' · closed' : ''}
            {r.subs > 0 && !compact ? ` · ${r.subs} sub-issues` : ''}
          </span>
        </span>
        {r.followed ? <span class="pc-tag">Following</span> : <span class="pc-go">Follow</span>}
      </button>
    </li>
  );
}

function Results({ q, onFollow, compact }: { q: string; onFollow: (r: Result) => void; compact?: boolean }) {
  const rs = search(q);
  const ref = isRef(q);
  const parents = rs.filter((r) => r.subs > 0);
  const rest = rs.filter((r) => r.subs === 0);
  return (
    <div class="pc-results">
      {ref && rs.length === 0 ? (
        <ul class="pc-list">
          <ResultRow r={{ ref: q.trim(), title: 'Follow this issue', repo: '', subs: 0, closed: 0, state: 'open' }} onFollow={onFollow} />
        </ul>
      ) : null}
      {!q.trim() ? <p class="pc-cap">Open parent issues in this fleet</p> : null}
      {q.trim() && parents.length > 0 ? <p class="pc-cap">Parent issues</p> : null}
      {parents.length > 0 ? (
        <ul class="pc-list">
          {parents.map((r) => (
            <ResultRow key={r.ref} r={r} onFollow={onFollow} compact={compact} />
          ))}
        </ul>
      ) : null}
      {q.trim() && rest.length > 0 ? <p class="pc-cap">Other issues</p> : null}
      {q.trim() && rest.length > 0 ? (
        <ul class="pc-list">
          {rest.map((r) => (
            <ResultRow key={r.ref} r={r} onFollow={onFollow} compact={compact} />
          ))}
        </ul>
      ) : null}
      {q.trim() && rs.length === 0 && !ref ? <p class="note">No issue in jbuffin/admiral or jbuffin/commodore matches.</p> : null}
    </div>
  );
}

function SkillSelect({ value, onChange }: { value: string; onChange: (s: string) => void }) {
  return (
    <label class="field">
      <span>Drafting skill</span>
      <select value={value} onChange={(e) => onChange(e.currentTarget.value)}>
        <option value="">No skill: Admiral drafts a title and description</option>
        {SKILLS.map((s) => (
          <option key={s} value={s}>
            /{s}: a drafting ship runs it with you
          </option>
        ))}
      </select>
    </label>
  );
}

/** Harness and repo for the drafting ship, folded to one line until opened. */
function ShipLine() {
  const [open, setOpen] = useState(false);
  const [h, setH] = useState(HARNESSES[0]!);
  const [repo, setRepo] = useState(REPOS[0]!);
  if (!open)
    return (
      <p class="note pc-shipline">
        Ship: {h} · {repo} · own worktree ·{' '}
        <button type="button" class="pc-link" onClick={() => setOpen(true)}>
          Change
        </button>
      </p>
    );
  return (
    <div class="pair">
      <label class="field">
        <span>Harness</span>
        <select value={h} onChange={(e) => setH(e.currentTarget.value)}>
          {HARNESSES.map((x) => (
            <option key={x}>{x}</option>
          ))}
        </select>
      </label>
      <label class="field">
        <span>Repo</span>
        <select value={repo} onChange={(e) => setRepo(e.currentTarget.value)}>
          {REPOS.map((x) => (
            <option key={x}>{x}</option>
          ))}
        </select>
      </label>
    </div>
  );
}

function fakeDraft(text: string) {
  const first = text.trim().split(/[.\n]/)[0] ?? '';
  return {
    title: first.length > 70 ? `${first.slice(0, 67)}…` : first.charAt(0).toUpperCase() + first.slice(1),
    body: `## Goal\n\n${text.trim()}\n\n## Done when\n\n- [ ] …\n\nSub-issues get added as the campaign is planned.`,
  };
}

/** The confirm step of a quick draft: the drafter's title and body, editable, and the repo the parent goes in. */
function Confirm({ text, onBack, onDone }: { text: string; onBack: () => void; onDone: () => void }) {
  const [drafting, setDrafting] = useState(true);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [repo, setRepo] = useState(REPOS[0]!);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => {
      const d = fakeDraft(text);
      setTitle(d.title);
      setBody(d.body);
      setDrafting(false);
    }, 1400);
    return () => clearTimeout(t);
  }, []);
  return (
    <>
      <label class="field">
        <span>Repo for the parent issue</span>
        <select value={repo} onChange={(e) => setRepo(e.currentTarget.value)}>
          {REPOS.map((x) => (
            <option key={x}>{x}</option>
          ))}
        </select>
      </label>
      <label class="field">
        <span>Title {drafting ? null : <em class="campaign-tag">Drafted</em>}</span>
        <input type="text" value={title} placeholder={drafting ? 'Drafting a title…' : ''} aria-busy={drafting} onInput={(e) => setTitle(e.currentTarget.value)} />
      </label>
      <label class="field">
        <span>Description {drafting ? null : <em class="campaign-tag">Drafted</em>}</span>
        <textarea rows={6} value={body} placeholder={drafting ? 'Drafting a description…' : ''} aria-busy={drafting} onInput={(e) => setBody(e.currentTarget.value)} />
      </label>
      <p class="note">Admiral files this as jason-buffin on github.com, then follows it.</p>
      <div class="launch-foot">
        <Button label="Back" onClick={onBack} />
        <Button
          variant="primary"
          label="Create campaign"
          busy={busy}
          disabled={drafting || !title.trim()}
          onClick={() => {
            setBusy(true);
            setTimeout(() => {
              say(`Created and following “${title}”`);
              onDone();
            }, 700);
          }}
        />
      </div>
    </>
  );
}

function launchShip(skill: string, onClose: () => void, stay?: () => void) {
  const name = `draft-${Math.random().toString(16).slice(2, 6)}`;
  draftingRow.value = `/${skill} · ${name}`;
  if (stay) return stay();
  say(`Launched ${name} running /${skill}; opening its bridge`);
  onClose();
}

const followed = (r: Result, onClose: () => void) => {
  say(`Following “${r.title}”`);
  onClose();
};

/* A: one box. Typing searches; the last row is always "Start a new campaign from what you typed". */
function VariantA({ onClose }: { onClose: () => void }) {
  const [q, setQ] = useState('');
  const [mode, setMode] = useState<'find' | 'new' | 'confirm'>('find');
  const [text, setText] = useState('');
  const [skill, setSkill] = useState(remembered.value);
  const inp = useRef<HTMLInputElement>(null);
  useEffect(() => inp.current?.focus(), [mode]);
  return (
    <Frame label="Add campaign" onClose={onClose}>
      <h2 class="sheet-title">{mode === 'confirm' ? 'New campaign' : 'Add campaign'}</h2>
      <div class="launch-body">
        {mode === 'find' ? (
          <>
            <div class="pc-search">
              <Icon name="search" size={16} />
              <input ref={inp} type="search" aria-label="Find an issue" placeholder="Search, #n, owner/repo#n or a URL" value={q} onInput={(e) => setQ(e.currentTarget.value)} />
            </div>
            <Results q={q} onFollow={(r) => followed(r, onClose)} />
            <button
              type="button"
              class="pc-new"
              onClick={() => {
                setText(isRef(q) ? '' : q);
                setMode('new');
              }}
            >
              <Icon name="plus" size={16} />
              <span>{q.trim() && !isRef(q) ? <>Start a new campaign: “{q.trim()}”</> : 'Start a new campaign…'}</span>
            </button>
          </>
        ) : mode === 'new' ? (
          <>
            <label class="field">
              <span>What is this campaign for?</span>
              <textarea rows={5} value={text} placeholder="A sentence or a paragraph. Admiral or your skill turns it into the parent issue." onInput={(e) => setText(e.currentTarget.value)} />
            </label>
            <SkillSelect value={skill} onChange={setSkill} />
            {skill ? <ShipLine /> : null}
            <div class="launch-foot">
              <Button label="Back" onClick={() => setMode('find')} />
              <Button
                variant="primary"
                label={skill ? 'Launch drafting ship' : 'Draft'}
                disabled={!text.trim()}
                onClick={() => {
                  remembered.value = skill;
                  if (skill) launchShip(skill, onClose);
                  else setMode('confirm');
                }}
              />
            </div>
          </>
        ) : (
          <Confirm text={text} onBack={() => setMode('new')} onDone={onClose} />
        )}
      </div>
    </Frame>
  );
}

/* B: two tabs, Follow an issue and Create new; the drafting ship ending stays in the modal. */
function VariantB({ onClose }: { onClose: () => void }) {
  const [tab, setTab] = useState<'follow' | 'create'>('follow');
  const [q, setQ] = useState('');
  const [text, setText] = useState('');
  const [skill, setSkill] = useState(remembered.value);
  const [step, setStep] = useState<'form' | 'confirm' | 'launched'>('form');
  return (
    <Frame label="Add campaign" onClose={onClose}>
      <h2 class="sheet-title">Add campaign</h2>
      {step === 'form' ? (
        <div class="pc-tabs" role="tablist">
          <button type="button" role="tab" aria-selected={tab === 'follow'} onClick={() => setTab('follow')}>
            Follow an issue
          </button>
          <button type="button" role="tab" aria-selected={tab === 'create'} onClick={() => setTab('create')}>
            Create new
          </button>
        </div>
      ) : null}
      <div class="launch-body pc-fixed">
        {step === 'launched' ? (
          <div class="pc-launched">
            <p>
              <strong>{draftingRow.value?.split(' · ')[1]}</strong> is running <span class="mono">/{skill}</span> with you.
            </p>
            <p class="note">It files the parent issue and Admiral follows it when the ship names it. It shows under Campaigns as Drafting until then.</p>
            <div class="launch-foot">
              <Button label="Close" onClick={onClose} />
              <Button
                variant="primary"
                label="Open bridge"
                onClick={() => {
                  say('Opening the bridge');
                  onClose();
                }}
              />
            </div>
          </div>
        ) : step === 'confirm' ? (
          <Confirm text={text} onBack={() => setStep('form')} onDone={onClose} />
        ) : tab === 'follow' ? (
          <>
            <input class="pc-plain" type="search" autoFocus aria-label="Find an issue" placeholder="Search, #n, owner/repo#n or a URL" value={q} onInput={(e) => setQ(e.currentTarget.value)} />
            <Results q={q} onFollow={(r) => followed(r, onClose)} />
          </>
        ) : (
          <>
            <label class="field">
              <span>Describe the campaign</span>
              <textarea rows={6} autoFocus value={text} placeholder="What should this campaign get done?" onInput={(e) => setText(e.currentTarget.value)} />
            </label>
            <fieldset>
              <legend>Who drafts it</legend>
              <div class="seg nv-seg">
                <button type="button" class={`seg-opt${skill === '' ? ' on' : ''}`} onClick={() => setSkill('')}>
                  Admiral
                </button>
                {SKILLS.map((s) => (
                  <button type="button" key={s} class={`seg-opt${skill === s ? ' on' : ''}`} onClick={() => setSkill(s)}>
                    /{s}
                  </button>
                ))}
              </div>
            </fieldset>
            <p class="note">{skill ? `A With-you ship runs /${skill} and files the parent issue. Remembered for this fleet.` : 'Admiral drafts a title and description for you to edit before it files the issue.'}</p>
            {skill ? <ShipLine /> : null}
            <div class="launch-foot">
              <Button label="Cancel" onClick={onClose} />
              <Button
                variant="primary"
                label={skill ? 'Launch drafting ship' : 'Draft'}
                disabled={!text.trim()}
                onClick={() => {
                  remembered.value = skill;
                  if (skill) launchShip(skill, onClose, () => setStep('launched'));
                  else setStep('confirm');
                }}
              />
            </div>
          </>
        )}
      </div>
    </Frame>
  );
}

/* C: wide, search on the left, a standing Create new panel on the right; stacked on a phone. Quick draft fills in under the text. */
function VariantC({ onClose }: { onClose: () => void }) {
  const [q, setQ] = useState('');
  const [text, setText] = useState('');
  const [skill, setSkill] = useState(remembered.value);
  const [confirm, setConfirm] = useState(false);
  return (
    <Frame label="Add campaign" wide onClose={onClose}>
      <h2 class="sheet-title">Add campaign</h2>
      <div class="pc-split">
        <section class="pc-col">
          <h3 class="pc-h">Follow an issue</h3>
          <input class="pc-plain" type="search" autoFocus aria-label="Find an issue" placeholder="Search, #n, owner/repo#n or a URL" value={q} onInput={(e) => setQ(e.currentTarget.value)} />
          <Results q={q} compact onFollow={(r) => followed(r, onClose)} />
        </section>
        <section class="pc-col pc-create">
          <h3 class="pc-h">Or start a new one</h3>
          {confirm ? (
            <Confirm text={text} onBack={() => setConfirm(false)} onDone={onClose} />
          ) : (
            <>
              <label class="field">
                <span>What is it for?</span>
                <textarea rows={4} value={text} placeholder="Describe the goal" onInput={(e) => setText(e.currentTarget.value)} />
              </label>
              <SkillSelect value={skill} onChange={setSkill} />
              {skill ? <ShipLine /> : null}
              <div class="launch-foot">
                <Button
                  variant="primary"
                  label={skill ? 'Launch drafting ship' : 'Draft'}
                  disabled={!text.trim()}
                  onClick={() => {
                    remembered.value = skill;
                    if (skill) launchShip(skill, onClose);
                    else setConfirm(true);
                  }}
                />
              </div>
            </>
          )}
        </section>
      </div>
    </Frame>
  );
}

const CSS = `
.pc-form { gap: 0; }
.pc-form .launch-body { padding-bottom: 4px; }
.pc-fixed { min-height: min(420px, 60dvh); }
@media (min-width: 641px) { .pc-wide { width: 860px; } }
.pc-search { display: flex; align-items: center; gap: 8px; padding: 0 10px; border: 1px solid var(--color-line2); border-radius: var(--shape-radius-md); background: var(--color-bg); color: var(--color-muted); }
.pc-search input { flex: 1; min-width: 0; min-height: var(--shape-min-touch-target); border: 0; background: none; font: inherit; font-size: 16px; color: var(--color-text); outline: none; }
.pc-plain { width: 100%; box-sizing: border-box; min-height: var(--shape-min-touch-target); padding: 8px 10px; font: inherit; font-size: 16px; color: var(--color-text); background: var(--color-bg); border: 1px solid var(--color-line2); border-radius: var(--shape-radius-md); }
.pc-results { display: flex; flex-direction: column; gap: 4px; }
.pc-cap { margin: 6px 2px 0; font-size: var(--font-size-meta); color: var(--color-muted); }
.pc-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
.pc-row { display: flex; align-items: center; gap: 10px; width: 100%; min-height: 52px; padding: 6px 8px; border: 0; border-radius: var(--shape-radius-md); background: none; color: var(--color-text); font: inherit; text-align: left; cursor: pointer; }
.pc-row:hover:not(:disabled) { background: var(--color-sel); }
.pc-row:disabled { cursor: default; opacity: .75; }
.pc-kind { flex: none; width: 38px; text-align: center; font-size: var(--font-size-meta); color: var(--color-muted); }
.pc-kind.parent { padding: 2px 0; border: 1px solid var(--color-accent); border-radius: 999px; color: var(--color-text); }
.pc-t { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.pc-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pc-ref { font-size: var(--font-size-meta); color: var(--color-muted); }
.pc-go { flex: none; font-size: var(--font-size-meta); color: var(--color-accent); }
.pc-tag { flex: none; font-size: var(--font-size-meta); color: var(--color-ok); }
.pc-new { display: flex; align-items: center; gap: 8px; min-height: var(--shape-min-touch-target); padding: 6px 8px; margin-top: 4px; border: 1px dashed var(--color-line2); border-radius: var(--shape-radius-md); background: none; color: var(--color-text); font: inherit; text-align: left; cursor: pointer; }
.pc-tabs { display: flex; gap: 4px; margin: 0 0 10px; border-bottom: 1px solid var(--color-line); }
.pc-tabs button { min-height: var(--shape-min-touch-target); padding: 0 12px; border: 0; border-bottom: 2px solid transparent; background: none; color: var(--color-text2); font: inherit; cursor: pointer; }
.pc-tabs button[aria-selected='true'] { border-color: var(--color-accent); color: var(--color-text); font-weight: 600; }
.pc-link { border: 0; background: none; padding: 0; color: var(--color-accent); font: inherit; cursor: pointer; min-height: 32px; }
.pc-split { display: grid; grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr); gap: 20px; overflow-y: auto; min-height: 0; padding: 2px; }
.pc-col { display: flex; flex-direction: column; gap: 10px; min-width: 0; }
.pc-create { padding-left: 20px; border-left: 1px solid var(--color-line); }
.pc-h { margin: 0; font-size: var(--font-size-meta); font-weight: 600; color: var(--color-text2); text-transform: uppercase; letter-spacing: .04em; }
@media (max-width: 760px) { .pc-split { grid-template-columns: minmax(0, 1fr); } .pc-create { padding: 16px 0 0; border-left: 0; border-top: 1px solid var(--color-line); } }
.pc-launched { display: flex; flex-direction: column; gap: 10px; }
.pc-launched p { margin: 0; }
.pc-switch { position: fixed; z-index: 99; left: 50%; bottom: calc(16px + env(safe-area-inset-bottom)); transform: translateX(-50%); display: flex; align-items: center; gap: 10px; padding: 6px 10px; border-radius: 999px; background: #111; color: #fff; font: 600 13px/1 system-ui; box-shadow: 0 4px 18px rgba(0,0,0,.35); }
.pc-switch button { width: 36px; height: 36px; border: 0; border-radius: 999px; background: #333; color: #fff; font-size: 20px; cursor: pointer; }
.pc-toast { position: fixed; z-index: 98; left: 50%; top: calc(16px + env(safe-area-inset-top)); transform: translateX(-50%); max-width: calc(100vw - 32px); padding: 10px 14px; border-radius: var(--shape-radius-md); background: var(--color-panel); border: 1px solid var(--color-line2); box-shadow: 0 4px 18px rgba(0,0,0,.2); }
.pc-drafting { display: flex; align-items: center; gap: 8px; padding: 6px; font-size: var(--font-size-meta); color: var(--color-text2); }
.pc-drafting .dot { width: 8px; height: 8px; border-radius: 999px; background: var(--color-accent); animation: pc-pulse 1.2s infinite; }
@keyframes pc-pulse { 50% { opacity: .3; } }
`;
