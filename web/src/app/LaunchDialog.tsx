import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type DirsAnswer, type Fleet } from './api';
import { modeLabel, skillLabel, type Mode, type SkillView } from './campaign';
import {
  bareSkillName,
  campaignLaunch,
  campaignOrder,
  sliceOrder,
  type CrewAnswer,
  crewOptions,
  defaultSkill,
  effortsFor,
  launchBody,
  memberRun,
  type SkillChoice,
  type SkillsAnswer,
  togglePin,
} from './launchForm';
import { CheckoutSelect, CLONE, DockPicker, ModelEffortPair, NO_DIR, ResourcesPicker, SkillSelect, skillValue } from './LaunchPickers';

export interface OrderRef {
  source: string;
  id: string;
  title: string;
  text: string;
}

/** A campaign child launched by hand: the server picks its skill, text and autopilot. */
export interface CampaignChildRef {
  campaignId: string;
  url: string;
  /** A slice's children in row order, `url` the first; `title` then names the slice. */
  issues?: string[];
  title: string;
  mode: Mode;
  skill?: SkillView;
  startAnyway: boolean;
}

const NONE = NO_DIR;
const toValue = skillValue;

export function LaunchForm({ fleet, order: given, campaign, onClose, onLaunched }: { fleet: Fleet; order?: OrderRef; campaign?: CampaignChildRef; onClose: () => void; onLaunched?: (session: string) => void }) {
  const order: OrderRef | undefined = campaign ? { ...(campaign.issues ? sliceOrder(campaign.campaignId, campaign.issues) : campaignOrder(campaign.campaignId, campaign.url)), title: campaign.title, text: '' } : given;
  const [harness, setHarness] = useState(fleet.docks[0]?.harness ?? '');
  const [dirs, setDirs] = useState<DirsAnswer | null>(null);
  const [dir, setDir] = useState(NONE);
  const picked = useRef(false);
  const [text, setText] = useState(order?.text ?? '');
  const [skills, setSkills] = useState<SkillsAnswer | null>(null);
  const [skill, setSkill] = useState('none');
  const skillPicked = useRef(false);
  const [custom, setCustom] = useState('');
  const [models, setModels] = useState<Record<string, string>>({});
  const [effort, setEffort] = useState('');
  const [crewAnswer, setCrewAnswer] = useState<CrewAnswer | null>(null);
  const [crewFailed, setCrewFailed] = useState(false);
  const [pinned, setPinned] = useState<string[]>([]);
  const [membersAnswer, setMembersAnswer] = useState<CrewAnswer | null>(null);
  const [membersFailed, setMembersFailed] = useState(false);
  const [member, setMember] = useState('');
  const [held, setHeld] = useState<string[]>([]);
  const [autopilot, setAutopilot] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const dirsError = useRef('');
  const skillNow = useRef(skill);
  skillNow.current = skill;

  // The preselection: an order's repo, else a URL in the text, else the dock's last directory; a freeform text is asked again as it changes.
  useEffect(() => {
    let live = true;
    const t = setTimeout(
      async () => {
        const r = await call<DirsAnswer>('POST', '/launch-orders/dirs', order ? { fleet: fleet.key, harness, source: order.source, order_id: order.id } : { fleet: fleet.key, harness, text });
        if (!live) return;
        if (!r.ok) {
          dirsError.current = r.error;
          return setError(r.error);
        }
        const stale = dirsError.current;
        dirsError.current = '';
        setError((cur) => (cur === stale ? '' : cur));
        setDirs(r.data);
        if (picked.current) return;
        const clone = r.data.clone;
        if (clone && !clone.exists) setDir(CLONE);
        else setDir(r.data.selected ?? (order ? (r.data.entries[0]?.path ?? NONE) : NONE));
      },
      order || !dirs ? 0 : 400,
    );
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [harness, order ? '' : text]);

  const dirForSkills = dir && dir !== CLONE ? dir : (dirs?.entries[0]?.path ?? '');
  useEffect(() => {
    if (!dirForSkills || campaign) return;
    let live = true;
    const q = new URLSearchParams({ fleet: fleet.key, harness, dir: dirForSkills });
    void call<SkillsAnswer>('GET', `/launch-orders/skills?${q}`).then((r) => {
      if (!live || !r.ok) return;
      setSkills(r.data);
      // A pick this dock doesn't offer falls back to the default, so the select shows what is sent.
      const cur = skillNow.current;
      const offered = cur === 'commodore' ? r.data.commodore.found : !cur.startsWith('skill:') || r.data.skills.some((s) => s.favorite && `skill:${s.name}` === cur);
      if (!skillPicked.current || !offered) {
        skillPicked.current = false;
        setSkill(toValue(defaultSkill(r.data)));
      }
    });
    return () => {
      live = false;
    };
  }, [dirForSkills, harness]);

  // A clone launch has no repo yet, so it offers the built-in and fleet members only.
  const crewDir = dir && dir !== CLONE ? dir : '';
  const pinning = skill === 'commodore';
  useEffect(() => {
    if (!pinning) return;
    let live = true;
    const q = new URLSearchParams({ fleet: fleet.key, harness, ...(crewDir && { dir: crewDir }) });
    void call<CrewAnswer>('GET', `/launch-orders/crew?${q}`).then((r) => {
      if (!live) return;
      setCrewFailed(!r.ok);
      setCrewAnswer(r.ok ? r.data : null);
    });
    return () => {
      live = false;
    };
  }, [pinning, harness, crewDir]);

  // Launch as lists the members of a Just launch, with its own fetch.
  const launchingAs = skill === 'none';
  useEffect(() => {
    if (!launchingAs) return;
    let live = true;
    const q = new URLSearchParams({ fleet: fleet.key, harness, ...(crewDir && { dir: crewDir }) });
    void call<CrewAnswer>('GET', `/launch-orders/crew?${q}`).then((r) => {
      if (!live) return;
      setMembersFailed(!r.ok);
      setMembersAnswer(r.ok ? r.data : null);
    });
    return () => {
      live = false;
    };
  }, [launchingAs, harness, crewDir]);

  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    box.current?.focus();
    return () => opener?.isConnected && opener.focus();
  }, []);

  const efforts = effortsFor(harness);
  const model = models[harness] ?? '';
  const clone = dirs?.clone ?? null;
  const cloning = dir === CLONE && !!clone;
  const crewOffered = crewOptions(crewAnswer?.members ?? [], harness);
  const crew = pinned.filter((n) => crewOffered.some((o) => o.name === n));
  const memberViews = membersAnswer?.members ?? [];
  const as = launchingAs ? memberRun(memberViews, harness, member) : null;
  const favorites = (skills?.skills ?? []).filter((s) => s.favorite);

  const chosen = (): SkillChoice | string => {
    if (skill === 'commodore' || skill === 'none') return { kind: skill };
    if (skill === 'custom') return /\s/.test(custom) ? 'Skill names have no spaces.' : bareSkillName(custom) ? { kind: 'skill', name: custom } : 'Type a skill name, or pick another.';
    return { kind: 'skill', name: skill.slice(6) };
  };

  const keys = (e: KeyboardEvent) => {
    if (e.key === 'Escape') return onClose();
    if (e.key !== 'Tab' || !box.current) return;
    const items = [...box.current.querySelectorAll<HTMLElement>('button, input, select, textarea, [tabindex]:not([tabindex="-1"])')].filter((i) => !(i as HTMLButtonElement).disabled && i.offsetParent !== null && !(i instanceof HTMLInputElement && i.type === 'radio' && !i.checked));
    if (items.length === 0) return;
    // Safari's default Tab order skips buttons and checkboxes and can leave the dialog, so Tab moves focus here, in a ring.
    e.preventDefault();
    const at = items.indexOf(document.activeElement as HTMLElement);
    const step = e.shiftKey ? -1 : 1;
    items[at < 0 ? (e.shiftKey ? items.length - 1 : 0) : (at + step + items.length) % items.length]!.focus();
  };

  const submit = async (e: Event) => {
    e.preventDefault();
    const s = campaign ? { kind: 'none' as const } : chosen();
    if (typeof s === 'string') return setError(s);
    setBusy(true);
    setError('');
    const effortNow = efforts.includes(effort) ? effort : '';
    const sent = campaign
      ? campaignLaunch({ campaignId: campaign.campaignId, ...(campaign.issues ? { issues: campaign.issues } : { childUrl: campaign.url }), harness, dir: cloning ? '' : dir, clone: cloning, cloneRepo: cloning ? clone!.repo : undefined, startAnyway: campaign.startAnyway, model, effort: effortNow })
      : { path: '/ships', body: launchBody({
      fleet: fleet.key,
      harness,
      dir: cloning ? '' : dir,
      clone: cloning,
      cloneRepo: cloning ? clone!.repo : undefined,
      skill: s,
      text,
      model,
      effort: effortNow,
      autopilot,
      resources: held,
      source: order?.source,
      orderId: order?.id,
      crew,
      member: as ? member : undefined,
      memberHarness: as?.harness,
    }) };
    const r = await call<{ ship: { session: string } }>('POST', sent.path, sent.body);
    setBusy(false);
    if (!r.ok) return setError(r.error);
    onLaunched?.(r.data.ship.session);
    onClose();
  };

  return (
    <div class="backdrop launch-backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} class="sheet launch-form" role="dialog" aria-modal="true" aria-label="Launch" tabIndex={-1} onKeyDown={keys}>
      <form class="launch-inner" onSubmit={submit}>
        <h2 class="sheet-title">Launch</h2>
        {order ? <p class="launch-order">{order.title}</p> : null}
        <div class="launch-body">
          <DockPicker docks={fleet.docks} harness={harness} onChange={setHarness} />

          {campaign ? null : (
          <label class="field">
            <span>Text</span>
            <textarea
              rows={4}
              value={text}
              readOnly={!!order}
              placeholder={order ? '' : 'Optional. Leave empty to start the harness with no prompt.'}
              onInput={(e) => setText((e.target as HTMLTextAreaElement).value)}
            />
          </label>
          )}

          <CheckoutSelect
            dir={dir}
            dirs={dirs}
            allowClone
            onChange={(v) => {
              picked.current = true;
              setDir(v);
            }}
          />
          {cloning ? <p class="note">{`Clone ${clone!.repo} into ${clone!.path} before launch.`}</p> : null}
          {clone?.exists ? <p class="note warn">{`A folder already exists at ${clone.path}, so cloning there will be refused.`}</p> : null}
          {(dirs?.entries.find((d) => d.path === dir)?.ships_here ?? 0) > 0 ? <p class="note warn">Another ship already runs here.</p> : null}

          {campaign ? (
            <p class="note">
              {modeLabel(campaign.mode)} · {skillLabel(campaign.skill)}
            </p>
          ) : (
          <SkillSelect
            value={skill}
            skills={skills}
            onChange={(v) => {
              skillPicked.current = true;
              setSkill(v);
            }}
          />
          )}
          {!campaign && skill === 'custom' ? (
            <label class="field">
              <span>Skill name</span>
              <input type="text" autocomplete="off" autocapitalize="off" spellcheck={false} value={custom} onInput={(e) => setCustom((e.target as HTMLInputElement).value)} />
            </label>
          ) : null}

          {launchingAs && membersFailed ? <p class="note warn">Couldn't load crew members</p> : null}
          {launchingAs && memberViews.length > 0 ? (
            <label class="field">
              <span>Launch as</span>
              <select value={as ? member : ''} onChange={(e) => setMember((e.target as HTMLSelectElement).value)}>
                <option value="">None</option>
                {memberViews.map((m) => (
                  <option key={m.name} value={m.name}>
                    {`${m.name} (${m.role}, ${m.layer})`}
                  </option>
                ))}
              </select>
            </label>
          ) : null}
          {as?.hint ? <p class="note">{as.hint}</p> : null}

          {pinning && crewFailed ? <p class="note warn">Couldn't load crew members</p> : null}
          {pinning && crewAnswer && crewAnswer.errors.length > 0 ? (
            <p class="note warn">{`Crew members can't be pinned: ${crewAnswer.errors.map((e) => `${e.layer}${e.file ? `/${e.file}` : ''}: ${e.message}`).join('; ')}`}</p>
          ) : null}
          {pinning && crewOffered.length > 0 ? (
            <fieldset class="checks">
              <legend>Crew</legend>
              {crewOffered.map((m) => (
                <label key={m.name} class="check">
                  <input type="checkbox" checked={crew.includes(m.name)} onChange={() => setPinned(togglePin(crew, crewOffered, m.name))} />
                  {m.name}
                  <span class="meta">{m.hint ? `${m.role}, ${m.layer}, ${m.hint}` : `${m.role}, ${m.layer}`}</span>
                </label>
              ))}
            </fieldset>
          ) : null}

          {as && as.harness !== harness ? (
            <p class="note">{`Model and effort are the ${as.harness} default; the member sets its own model.`}</p>
          ) : (
          <ModelEffortPair harness={harness} model={model} effort={effort} onModel={(m) => setModels({ ...models, [harness]: m })} onEffort={setEffort} />
          )}

          {campaign ? null : <ResourcesPicker held={held} onChange={setHeld} />}

          {campaign ? null : (
          <label class="check">
            <input type="checkbox" checked={autopilot} onChange={(e) => setAutopilot((e.target as HTMLInputElement).checked)} />
            <span>
              Autopilot <span class="meta">runs under the guard and counts against max_ships</span>
            </span>
          </label>
          )}
          {error ? <p role="alert" class="launch-error">{error}</p> : null}
        </div>
        <div class="launch-foot">
          <button type="button" class="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" class="btn primary" disabled={busy || (!cloning && !dir)}>
            Launch
          </button>
        </div>
      </form>
      </div>
    </div>
  );
}
