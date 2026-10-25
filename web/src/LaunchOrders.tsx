import { useEffect, useRef, useState } from 'preact/hooks';
import { type Dock, type Fleet, config } from './config';
import { harnesses } from './harnesses';
import { announceLaunch, dragging, forgetOpener, launching, newShip, returnFocus } from './launch';
import { LogoFlag } from './Logos';
import { Modal } from './Modal';
import { phoneTab } from './PhoneShell';
import { launchHint, resources } from './resources';
import { ships } from './ships';
import { phone } from './sizeClass';
import { api, isWebUrl, sources } from './sources';

interface DirEntry {
  path: string;
  name: string;
  kind: string;
  ships_here: number;
}
interface Skill {
  name: string;
}
interface SkillsData {
  skills: Skill[];
  commodore: { found: boolean; name: string };
}

// 'commodore', 'none', 'custom' or `skill:<favorite's name>`.
type Choice = string;

const COMMODORE_DESC = 'Launch a task force: plans the mission, assembles teammates and sees it through to a merged PR.';
const MAX_ROWS = 6;

const titleOf = (text: string) => text.split('\n')[0];
// A typed name loses one leading slash or dollar sign.
const bare = (typed: string) => typed.trim().replace(/^[/$]/, '');

// The skills of this directory that are favorites. The favorites themselves are
// the server's list, pushed in the config message, not what the GET returned.
const favoritesOf = (d: SkillsData, names: string[]) => d.skills.filter((s) => names.includes(s.name));

function defaultChoice(d: SkillsData, names: string[]): Choice {
  if (d.commodore.found) return 'commodore';
  const fav = favoritesOf(d, names)[0];
  return fav ? `skill:${fav.name}` : 'none';
}

// Keeps the choice unless the skill it names is gone from this directory.
function keep(prev: Choice | null, d: SkillsData, names: string[]): Choice {
  if (prev === 'none' || prev === 'custom') return prev;
  if (prev === 'commodore' && d.commodore.found) return prev;
  if (prev?.startsWith('skill:') && favoritesOf(d, names).some((s) => s.name === prev.slice(6))) return prev;
  return defaultChoice(d, names);
}

// Focusing a field selects its text, so typing replaces it; the mouseup that ends
// the focusing click would drop the selection, so it is swallowed once.
const selectOnFocus = {
  onFocus: (e: FocusEvent) => {
    const el = e.target as HTMLInputElement;
    el.select();
    el.dataset.fresh = '1';
  },
  onMouseUp: (e: MouseEvent) => {
    const el = e.target as HTMLInputElement;
    if (el.dataset.fresh) {
      e.preventDefault();
      delete el.dataset.fresh;
    }
  },
};

export function Ghost() {
  const d = dragging.value;
  if (!d) return null;
  return (
    <div class="ghost" style={`left:${d.x - 125}px;top:${d.y - 40}px`} aria-hidden="true">
      <div class="ghost-order">
        {isWebUrl(d.order.url) && <span class="ghost-id">{d.order.id}</span>}
        <span class="ghost-title">{titleOf(d.order.title)}</span>
        <span class="ghost-hint">{d.dock ? 'Let go to give launch orders' : 'Drop on a dock'}</span>
      </div>
    </div>
  );
}

export function LaunchOrders() {
  const l = launching.value;
  if (!l) return null;
  const fleet = config.value?.fleets.find((f) => f.key === l.fleet);
  const dock = fleet?.docks.find((d) => d.harness === l.harness);
  if (!fleet || !dock) return null;
  return <Dialog key={`${l.source ?? ''}/${l.panel ?? ''}/${l.order?.id ?? ''}/${l.fleet}/${l.harness}`} fleet={fleet} dock={dock} />;
}

function Star({ on, name, onToggle }: { on: boolean; name: string; onToggle: () => void }) {
  return (
    <button type="button" class="star" aria-label={`${on ? 'Unstar' : 'Star'} ${name}`} onClick={onToggle}>
      <svg width="20" height="20" viewBox="0 0 24 24" aria-hidden="true" fill={on ? '#FFC23D' : 'none'} stroke="#14304A" stroke-width="2" stroke-linejoin="round">
        <path d="M12 3l2.7 5.6 6.1.8-4.5 4.2 1.1 6-5.4-3-5.4 3 1.1-6L3.2 9.4l6.1-.8z" />
      </svg>
    </button>
  );
}

function Dialog({ fleet, dock }: { fleet: Fleet; dock: Dock }) {
  const { source = '', panel, order, harness } = launching.value!;
  const h = harnesses[harness];
  const close = () => {
    launching.value = null;
    returnFocus();
  };

  const [entries, setEntries] = useState<DirEntry[] | null>(null);
  const [dirsError, setDirsError] = useState('');
  const [dir, setDir] = useState('');
  const [typed, setTyped] = useState<string | null>(null); // the filter, while the engineer is typing in the field
  const [dirOpen, setDirOpen] = useState(false);
  const [dirHi, setDirHi] = useState(0);

  const [skills, setSkills] = useState<SkillsData | null>(null);
  const [reading, setReading] = useState(!!order); // the skills of this directory are on their way; a dock launch has none
  const [choice, setChoice] = useState<Choice | null>(null);
  const [custom, setCustom] = useState('');
  const [note, setNote] = useState(''); // Just launch's optional text before the order; a dock launch's whole prompt
  const [listOpen, setListOpen] = useState(false);
  const [hi, setHi] = useState(0);
  const [starError, setStarError] = useState('');
  const [spaceError, setSpaceError] = useState(false);
  const [starring, setStarring] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const dirBox = useRef<HTMLDivElement>(null);
  const skillBox = useRef<HTMLDivElement>(null);
  const asked = useRef(0);
  const favNames = config.value?.favorite_skills ?? [];
  const hint = order ? launchHint(sources.value, source, panel, resources.value) : null;

  useEffect(() => {
    let live = true;
    api<{ entries: DirEntry[]; selected: string | null }>('POST', '/api/launch-orders/dirs', {
      fleet: fleet.key,
      harness,
      source,
      order_id: order?.id ?? '',
    }).then((r) => {
      if (!live) return;
      if (!r.ok) return setDirsError(r.error);
      setEntries(r.data.entries);
      // A dock launch has no order to guess a checkout from: the last one or nothing.
      setDir(r.data.selected ?? (order ? r.data.entries[0]?.path : '') ?? '');
    });
    return () => {
      live = false;
    };
  }, []);

  // The skills depend on the directory, so they are read again when it changes.
  useEffect(() => {
    // The old directory's skills are not the new one's: nothing to offer until they arrive.
    // The choice stays, and keep() applies it to the new skills.
    setSkills(null);
    if (!dir || !order) return;
    setReading(true);
    const n = ++asked.current;
    const q = new URLSearchParams({ fleet: fleet.key, harness, dir });
    api<SkillsData>('GET', `/api/launch-orders/skills?${q}`).then((r) => {
      if (n !== asked.current) return;
      setReading(false);
      if (!r.ok) {
        // No skills to offer: Just launch, and the launch itself shows what is wrong.
        setChoice((prev) => (prev === 'custom' ? prev : 'none'));
        return setError(r.error);
      }
      setError('');
      setSkills(r.data);
      setChoice((prev) => keep(prev, r.data, config.value?.favorite_skills ?? []));
    });
  }, [dir]);

  // Starring or unstarring arrives as the server's config push; a choice that was
  // a favorite no longer offered falls back to the default.
  useEffect(() => {
    if (skills) setChoice((prev) => keep(prev, skills, favNames));
    setStarring(false);
  }, [favNames.join('\n')]);

  // An opened list is brought into view inside the scrolling body.
  useEffect(() => {
    if (listOpen) document.getElementById('launch-skills')?.scrollIntoView({ block: 'nearest' });
  }, [listOpen]);
  useEffect(() => {
    if (dirOpen) document.getElementById('launch-dirs')?.scrollIntoView({ block: 'nearest' });
  }, [dirOpen]);

  // A press outside a list closes it.
  useEffect(() => {
    const away = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!skillBox.current?.contains(t)) setListOpen(false);
      if (!dirBox.current?.contains(t)) {
        setDirOpen(false);
        setTyped(null);
      }
    };
    document.addEventListener('pointerdown', away);
    return () => document.removeEventListener('pointerdown', away);
  }, []);

  const entry = entries?.find((e) => e.path === dir);
  const shownDirs = (entries ?? []).filter((e) => typed === null || e.name.toLowerCase().includes(typed.trim().toLowerCase()));
  const query = bare(custom).toLowerCase();
  const matches = (skills?.skills ?? []).filter((s) => !query || s.name.toLowerCase().includes(query));
  const rows = matches.slice(0, MAX_ROWS);
  const favorites = skills ? favoritesOf(skills, favNames) : [];

  const pickDir = (e: DirEntry) => {
    setDir(e.path);
    setTyped(null);
    setDirOpen(false);
  };
  const pickSkill = (name: string) => {
    setCustom(name);
    setChoice('custom');
    setListOpen(false);
    setSpaceError(false);
  };

  const toggleStar = async (s: Skill) => {
    if (starring) return;
    // All favorites, not only those installed on this dock: the PUT replaces the list.
    const next = favNames.includes(s.name) ? favNames.filter((n) => n !== s.name) : [...favNames, s.name];
    setStarring(true);
    const r = await api<{ favorite_skills: string[] }>('PUT', '/api/favorites', { skills: next });
    setStarError(r.ok ? '' : r.error);
    // Stay busy until the config push carries the new list, so a second star builds on it;
    // if the push already arrived (or nothing changed) there is nothing to wait for.
    const pushed = (config.value?.favorite_skills ?? []).join('\n');
    if (!r.ok || r.data.favorite_skills.join('\n') === pushed) setStarring(false);
  };

  const hoist = async (ev?: Event) => {
    ev?.preventDefault();
    if (busy || reading || !dir) return;
    let skill: { kind: string; name?: string };
    if (!order) skill = { kind: 'none' };
    else if (choice === 'commodore') skill = { kind: 'commodore' };
    else if (choice === 'none') skill = { kind: 'none' };
    else if (choice === 'custom') {
      const name = bare(custom);
      // The raw value: a space or tab at either end is refused too, not trimmed away.
      if (/\s/.test(custom)) return setSpaceError(true);
      if (!name) return setError('Type a skill name, or pick another.');
      // Sent as typed: the server takes off the one leading / or $.
      skill = { kind: 'skill', name: custom };
    } else skill = { kind: 'skill', name: choice!.slice(6) };
    setBusy(true);
    setError('');
    const r = await api<{ ship: { session: string } }>('POST', '/api/ships', {
      fleet: fleet.key,
      harness,
      dir,
      skill,
      source,
      order_id: order?.id ?? '',
      ...(!order ? { text: note } : skill.kind === 'none' && note.trim() && { note }),
    });
    setBusy(false);
    if (!r.ok) return setError(r.error);
    launching.value = null;
    forgetOpener();
    announceLaunch({ fleet: fleet.key, harness, source, order_id: order?.id ?? '', session: r.data.ship.session });
    if (phone.value) {
      // The new ship's row shows for a moment on the Ships tab, as the prototype does (:1489-1494).
      const session = r.data.ship.session;
      phoneTab.value = 'ships';
      newShip.value = session;
      setTimeout(() => {
        if (newShip.value === session) newShip.value = null;
      }, 2600);
    }
  };

  // Escape closes an open list first; otherwise it is Stay in port.
  const cancel = (e: Event) => {
    if (listOpen || dirOpen) {
      e.preventDefault();
      setListOpen(false);
      setDirOpen(false);
      setTyped(null);
    }
  };

  const walk = (e: KeyboardEvent, count: number, at: number, set: (n: number) => void, open: boolean, show: () => void, pick: () => void) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!open) show();
      else set(Math.min(count - 1, at + 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      set(Math.max(0, at - 1));
    } else if (e.key === 'Enter' && open && count > 0) {
      e.preventDefault(); // picks the row; it does not hoist sails
      pick();
    }
  };

  const radio = (id: Choice, disabled = false) => ({
    type: 'radio' as const,
    name: 'skill',
    checked: choice === id,
    disabled,
    onChange: () => setChoice(id),
  });

  return (
    <Modal label="Launch orders" variant="launch" onClose={close} onCancel={cancel}>
      <form class="launch-form" onSubmit={hoist}>
        <header class="launch-head" style={`background-color:${fleet.color};color:${fleet.ink}`}>
          <svg width="56" height="40" viewBox="0 0 40 28" aria-hidden="true">
            <LogoFlag harness={harness} kind="pier" />
          </svg>
          <div class="launch-title">
            <h2>Launch orders</h2>
            <span>
              {fleet.name} · {h.label} dock
            </span>
          </div>
        </header>
        <div class="launch-body">
          {order && (
            <div class="launch-order">
              {isWebUrl(order.url) && <span class="launch-order-id">{order.id}</span>}
              <span class="launch-order-title">{titleOf(order.title)}</span>
            </div>
          )}
          {hint && <p class="launch-warn">{hint}</p>}

          {dirsError ? (
            <p class="launch-note">{dirsError}</p>
          ) : (
            <>
              {!order && (
                <div class="field">
                  <label for="launch-text">What should it do?</label>
                  <textarea
                    id="launch-text"
                    class="launch-input launch-text"
                    placeholder="Optional. Leave empty to start the harness with no prompt."
                    rows={4}
                    value={note}
                    onInput={(e) => setNote((e.target as HTMLTextAreaElement).value)}
                  />
                </div>
              )}
              <div class="field" ref={dirBox}>
                <label for="launch-dir">Directory</label>
                <input
                  id="launch-dir"
                  role="combobox"
                  aria-expanded={dirOpen}
                  aria-controls="launch-dirs"
                  aria-autocomplete="list"
                  class="launch-input"
                  autocomplete="off"
                  spellcheck={false}
                  value={typed ?? entry?.name ?? ''}
                  disabled={!entries}
                  {...selectOnFocus}
                  onClick={() => setDirOpen(true)}
                  onInput={(e) => {
                    setTyped((e.target as HTMLInputElement).value);
                    setDirHi(0);
                    setDirOpen(true);
                  }}
                  onKeyDown={(e) => walk(e, shownDirs.length, dirHi, setDirHi, dirOpen, () => setDirOpen(true), () => pickDir(shownDirs[dirHi]))}
                />
                {dirOpen && (
                  <div id="launch-dirs" role="listbox" aria-label="Directories" class="launch-list">
                    {shownDirs.length === 0 && <div class="list-empty">No checkout or search root matches.</div>}
                    {shownDirs.map((e, i) => (
                      <div
                        key={e.path}
                        role="option"
                        aria-selected={i === dirHi}
                        class={i === dirHi ? 'row hi' : 'row'}
                        onMouseDown={(ev) => ev.preventDefault()}
                        onClick={() => pickDir(e)}
                      >
                        <span class="row-name">{e.name}</span>
                        <span class="row-kind">{e.kind === 'search_root' ? 'search root' : 'checkout'}</span>
                        {e.ships_here > 0 && <span class="row-busy">ship underway</span>}
                      </div>
                    ))}
                  </div>
                )}
                {entry && entry.ships_here > 0 && <p class="launch-warn">Another ship already runs here</p>}
              </div>

              {order && (
                <>
              <span class="label">Sail with</span>
              {/* Not a label: a tap on the text of a disabled row must reach the row, to show why it is off. */}
              <div
                class={choice === 'commodore' ? 'choice on' : 'choice'}
                title={skills && !skills.commodore.found ? 'Commodore not found' : undefined}
                onClick={() => skills?.commodore.found && setChoice('commodore')}
              >
                <input {...radio('commodore', !skills?.commodore.found)} aria-labelledby="commodore-title" aria-describedby="commodore-desc" />
                <span class="choice-text">
                  <span class="choice-title" id="commodore-title">
                    Commodore
                  </span>
                  <span class="choice-desc" id="commodore-desc">
                    {skills && !skills.commodore.found ? 'Commodore not found' : COMMODORE_DESC}
                  </span>
                </span>
                <span class="badge">Task force</span>
              </div>

              {favorites.slice(0, 5).map((s) => (
                <label key={s.name} class={choice === `skill:${s.name}` ? 'choice on' : 'choice'}>
                  <input {...radio(`skill:${s.name}`)} />
                  <span class="choice-text">
                    <span class="choice-title mono">{s.name}</span>
                    <span class="choice-desc">Favorite skill</span>
                  </span>
                  <span class="choice-star" aria-hidden="true">
                    ★
                  </span>
                </label>
              ))}

              <div class={choice === 'custom' ? 'choice custom on' : 'choice custom'} ref={skillBox}>
                <label class="custom-label">
                  <input {...radio('custom')} />
                  <span class="choice-title">Custom skill</span>
                </label>
                <input
                  type="text"
                  class="launch-input mono"
                  aria-label="Custom skill"
                  aria-controls="launch-skills"
                  placeholder="Start typing a skill name"
                  autocomplete="off"
                  autocapitalize="off"
                  spellcheck={false}
                  value={custom}
                  onFocus={(e) => {
                    selectOnFocus.onFocus(e);
                    setChoice('custom');
                    setListOpen(true);
                  }}
                  onMouseUp={selectOnFocus.onMouseUp}
                  // A field drops a pasted newline without a word, so the paste is refused here instead.
                  onPaste={(e) => {
                    if (!/\s/.test(e.clipboardData?.getData('text') ?? '')) return;
                    e.preventDefault();
                    setChoice('custom');
                    setSpaceError(true);
                  }}
                  onInput={(e) => {
                    setCustom((e.target as HTMLInputElement).value);
                    setChoice('custom');
                    setSpaceError(false);
                    setHi(0);
                    setListOpen(true);
                  }}
                  onKeyDown={(e) => walk(e, rows.length, hi, setHi, listOpen, () => setListOpen(true), () => pickSkill(rows[hi].name))}
                />
                {spaceError && <p class="launch-warn">Skill names have no spaces</p>}
                {listOpen && (
                  <div id="launch-skills" role="listbox" aria-label="Installed skills" class="launch-list">
                    <div class="list-head">
                      <span>Installed skills</span>
                      <span>{matches.length}</span>
                    </div>
                    {rows.map((s, i) => (
                      <div key={s.name} class={i === hi ? 'row hi' : 'row'}>
                        <div role="option" aria-selected={i === hi} class="row-pick" onMouseDown={(ev) => ev.preventDefault()} onClick={() => pickSkill(s.name)}>
                          <span class="row-name">{s.name}</span>
                        </div>
                        <Star on={favNames.includes(s.name)} name={s.name} onToggle={() => toggleStar(s)} />
                      </div>
                    ))}
                    {rows.length === 0 && <div class="list-empty">No installed skill matches. It will launch with the name exactly as typed.</div>}
                  </div>
                )}
                {starError && <p class="launch-warn">{starError}</p>}
              </div>

              <div class={choice === 'none' ? 'choice custom on' : 'choice custom'}>
                <label class="custom-label">
                  <input {...radio('none')} />
                  <span class="choice-text">
                    <span class="choice-title">Just launch</span>
                    <span class="choice-desc">Start the harness and give it the order as written.</span>
                  </span>
                </label>
                <textarea
                  class="launch-input launch-text"
                  aria-label="Text before the order"
                  placeholder="Optional text to send before the order"
                  rows={2}
                  value={note}
                  onFocus={() => setChoice('none')}
                  onInput={(e) => {
                    setNote((e.target as HTMLTextAreaElement).value);
                    setChoice('none');
                  }}
                />
              </div>
                </>
              )}
              {error && <p role="alert">{error}</p>}
            </>
          )}
        </div>
        <footer class="launch-foot">
          <button type="button" class="launch-button press" onClick={close}>
            Stay in port
          </button>
          {!dirsError && (
            <button type="submit" class="launch-button primary press" disabled={reading || !dir || busy}>
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M12 3v14M12 4l7 10h-7M3 18c2 2 4 2 6 0 2 2 4 2 6 0 2 2 4 2 6 0" />
              </svg>
              Hoist sails
            </button>
          )}
        </footer>
      </form>
    </Modal>
  );
}
