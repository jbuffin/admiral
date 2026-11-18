import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { call, fetchFleetRepos, fetchSettings, fetchSources, LAUNCH_TIMEOUT_MS, type Result, SLOW_TIMEOUT_MS } from './api';
import { Button } from './Button';
import type { Campaign } from './campaign';
import { openCampaign } from './campaignsLink';
import { capabilities, capabilitiesLoaded, loadCapabilities } from './capabilities';
import type { DraftingPick } from './draftingPick';
import { draftingOff, MAX_PROSE_RUNES } from './filing';
import { CheckoutSelect, cap, DockPicker } from './LaunchPickers';
import { accountForHost, type CreateBody, initialProject, projectChoices, safeHref, sourceHost, textCampaignNote, type Project } from './makeCampaign';
import { runes } from './standingOrders';

const REMEMBERED = 'admiral.campaign-repo';

const remembered = (): string | null => {
  try {
    return localStorage.getItem(REMEMBERED);
  } catch {
    return null;
  }
};
const remember = (id: string) => {
  try {
    localStorage.setItem(REMEMBERED, id);
  } catch {
    // A blocked storage only costs the next start its remembered repo.
  }
};

export interface Confirmed {
  title: string;
  body: string;
  drafted: { title: boolean; body: boolean };
  note: string;
}

interface TextProps {
  fleet: string;
  text: string;
  onText: (t: string) => void;
  onBack: () => void;
  onConfirm: (c: Confirmed) => void;
  onBusy: (b: boolean) => void;
  /** The fleet's drafting skill pick; null when the fleet has no dock to run a drafting ship on. */
  pick?: DraftingPick | null;
  onLaunched?: (session: string, skill: string) => void;
}

const NO_SKILL = '';

function DraftingSkill({ pick }: { pick: DraftingPick }) {
  const [changing, setChanging] = useState(false);
  const mark = pick.skills?.mark ?? '/';
  const missing = (n: string) => !pick.loading && !pick.options.find((o) => o.name === n)?.installed;
  const checkouts = pick.dirs ? { ...pick.dirs, clone: null } : null;
  const repo = checkouts?.entries.find((e) => e.path === pick.dir)?.name ?? pick.dir;
  return (
    <>
      <label class="field">
        <span>Drafting skill</span>
        <select aria-label="Drafting skill" value={pick.skill} onChange={(e) => pick.setSkill(e.currentTarget.value)}>
          <option value={NO_SKILL}>No skill: Admiral drafts a title and description</option>
          {pick.options.map(({ name: n }) => (
            <option key={n} value={n}>
              {`${mark}${n}: ${missing(n) ? 'not installed' : 'a drafting ship runs it with you'}`}
            </option>
          ))}
        </select>
      </label>
      {pick.error ? (
        <p class="alert" role="alert">
          {pick.error}
        </p>
      ) : null}
      {pick.skill ? (
        <>
          <p class="note">
            {`Ship: ${cap(pick.harness)} · ${repo} · own worktree${!pick.loading && !pick.installed ? ' · not installed' : ''} · `}
            <button type="button" class="btn link ship-change" aria-expanded={changing} onClick={() => setChanging(!changing)}>
              Change
            </button>
          </p>
          {changing ? (
            <>
              <DockPicker docks={pick.docks} harness={pick.harness} onChange={pick.setHarness} />
              <CheckoutSelect dir={pick.dir} dirs={checkouts} allowClone={false} onChange={pick.setDir} />
            </>
          ) : null}
          {pick.ambiguous ? (
            <fieldset class="checks">
              <legend>Which installed skill runs?</legend>
              {pick.names.map((n) => (
                <label key={n} class="check">
                  <input type="radio" name="drafting-name" checked={pick.picked === n} onChange={() => pick.setPicked(n)} />
                  {n}
                </label>
              ))}
            </fieldset>
          ) : null}
        </>
      ) : null}
    </>
  );
}

/** The step that asks what the campaign is for and has the drafter title it. */
export function TextStep({ fleet, text, onText, onBack, onConfirm, onBusy, pick, onLaunched }: TextProps) {
  const [busy, setBusy] = useState(false);
  const [launchError, setLaunchError] = useState('');
  const [refused, setRefused] = useState(false);
  const live = useRef(true);
  const area = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    live.current = true;
    area.current?.focus();
    void loadCapabilities();
    return () => {
      live.current = false;
    };
  }, []);
  const judge = capabilities.value.find((f) => f.key === fleet)?.judge;
  const plain = draftingOff(judge, refused);
  const size = runes(text);
  const tooLong = size > MAX_PROSE_RUNES;
  const own = (note = ''): Confirmed => ({
    title: '',
    body: text,
    drafted: { title: false, body: false },
    note,
  });

  const next = async () => {
    if (busy || text.trim() === '' || tooLong) return;
    if (plain) return onConfirm(own());
    setBusy(true);
    onBusy(true);
    const r = await call<{ title: string; body: string }>('POST', `/fleets/${encodeURIComponent(fleet)}/campaigns/draft`, { text }, undefined, { timeoutMs: SLOW_TIMEOUT_MS });
    if (!live.current) return;
    setBusy(false);
    onBusy(false);
    if (r.ok)
      return onConfirm({
        title: r.data.title,
        body: r.data.body,
        drafted: { title: true, body: true },
        note: '',
      });
    if (r.status === 409) {
      setRefused(true);
      void loadCapabilities();
      return onConfirm(own());
    }
    onConfirm(own(`Couldn't draft a title: ${r.error}. Write your own.`));
  };

  const drafts = !!pick && pick.skill !== NO_SKILL;
  const canLaunch = !!pick?.launchable && text.trim() !== '' && !tooLong;
  // A refusal belongs to the pick that drew it.
  useEffect(() => setLaunchError(''), [pick?.skill, pick?.harness, pick?.dir]);
  const launch = async () => {
    if (!pick || busy || !canLaunch) return;
    setBusy(true);
    onBusy(true);
    setLaunchError('');
    const r = await call<{ ship: { session: string } }>('POST', '/ships', { drafting: true, fleet, harness: pick.harness, dir: pick.dir, text, skill: { kind: 'skill', name: pick.name } }, undefined, { timeoutMs: LAUNCH_TIMEOUT_MS });
    if (!live.current) return;
    setBusy(false);
    onBusy(false);
    if (!r.ok) return setLaunchError(r.error);
    onLaunched?.(r.data.ship.session, pick.name);
  };

  return (
    <>
      <div class="launch-body">
        <label class="field">
          <span>What is this campaign for?</span>
          <textarea ref={area} aria-label="What is this campaign for?" rows={6} value={text} readOnly={busy} onInput={(e) => onText(e.currentTarget.value)} />
        </label>
        <p class={tooLong ? 'alert' : 'note'}>
          {size.toLocaleString('en-US')} / {MAX_PROSE_RUNES.toLocaleString('en-US')}
        </p>
        {pick ? <DraftingSkill pick={pick} /> : null}
        {launchError ? (
          <p class="alert" role="alert">
            {launchError}
          </p>
        ) : null}
      </div>
      <div class="launch-foot">
        <Button label="Back" disabled={busy} onClick={onBack}>
          Back
        </Button>
        {drafts ? (
          <Button variant="primary" label="Launch drafting ship" busyLabel="Launching" busy={busy} disabled={!canLaunch} onClick={() => void launch()}>
            Launch drafting ship
          </Button>
        ) : (
        <Button variant="primary" label={plain ? 'Next' : 'Draft'} busyLabel="Drafting" busy={busy} disabled={text.trim() === '' || tooLong || !capabilitiesLoaded.value || !!pick?.pending} onClick={() => void next()}>
          {plain ? 'Next' : 'Draft'}
        </Button>
        )}
      </div>
    </>
  );
}

interface Loaded {
  projects: Project[];
  accounts: { key: string; settings: Record<string, unknown> }[];
  problem: string;
}

export interface Filed {
  parent: string;
  error: string;
}

interface ConfirmProps {
  fleet: string;
  start: Confirmed;
  /** The parent issue a failed create already filed; kept above the step so a return to it can't file a second. */
  filed: Filed | null;
  onFiled: (f: Filed) => void;
  onClose: () => void;
  /** Absent where the step has nowhere to go back to; the Back button is then not shown. */
  onBack?: () => void;
  onBusy: (b: boolean) => void;
  onCreated?: () => void;
  /** Runs the create itself: the step then offers github.com projects only, remembers nothing and leaves following to the caller. */
  onCreate?: (body: { repo: string; title: string; body: string }) => Promise<Result<unknown>>;
  /** The repo ("owner/name") to preselect, matched without regard to case; none is selected when it is not offered. */
  repoSeed?: string;
  /** False where the step opens from a link, so the page keeps the focus. */
  autoFocus?: boolean;
  /** Buttons that sit before Create in the foot. */
  foot?: ComponentChildren;
}

function ParentLink({ url }: { url: string }) {
  const href = safeHref(url);
  return href ? (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {url}
    </a>
  ) : (
    <span>{url}</span>
  );
}

/** The step that shows the title, description and repo before the parent issue is filed. */
export function ConfirmStep({ fleet, start, filed, onFiled, onClose, onBack, onBusy, onCreated, onCreate, repoSeed, autoFocus = true, foot }: ConfirmProps) {
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [loadError, setLoadError] = useState('');
  const [repoId, setRepoId] = useState('');
  const [title, setTitle] = useState(start.title);
  const [body, setBody] = useState(start.body);
  const [drafted, setDrafted] = useState(start.drafted);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState(filed?.error ?? '');
  const parentMade = filed?.parent ?? '';
  const titleBox = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  useEffect(() => () => void (alive.current = false), []);

  useEffect(() => {
    if (loaded && autoFocus) titleBox.current?.focus();
  }, [!!loaded]);

  useEffect(() => {
    let live = true;
    void (async () => {
      const [repos, sources] = await Promise.all([fetchFleetRepos(fleet), fetchSources()]);
      if (!repos.ok) return live && setLoadError(repos.error);
      if (!sources.ok) return live && setLoadError(sources.error);
      const github = sources.data.filter((s) => s.fleet === fleet && s.plugin === 'github');
      const settled = await Promise.all(
        github.map(async (s) => ({
          key: s.key,
          r: await fetchSettings(s.key),
        })),
      );
      if (!live) return;
      const accounts = settled.flatMap(({ key, r }) => (r.ok ? [{ key, settings: r.data.settings }] : []));
      const failed = settled.flatMap(({ key, r }) => (r.ok ? [] : [`${key}: ${r.error}`]));
      const all = projectChoices(repos.data.repos ?? [], new Set(accounts.map((a) => sourceHost(a.settings))));
      // The create-from-item body carries no host, so the server routes on github.com.
      const projects = onCreate ? all.filter((p) => p.host === 'github.com') : all;
      const seeded = repoSeed === undefined ? undefined : projects.find((p) => p.repo.toLowerCase() === repoSeed.toLowerCase());
      setRepoId(repoSeed !== undefined ? (seeded?.id ?? '') : initialProject(projects, remembered()));
      setLoaded({ projects, accounts, problem: failed.length ? `Couldn't read a GitHub source's settings (${failed.join('; ')}), so some projects may be missing.` : '' });
    })();
    return () => {
      live = false;
    };
  }, [fleet]);

  const project = loaded?.projects.find((p) => p.id === repoId);
  const account = project && loaded ? accountForHost(loaded.accounts, project.host)?.account : undefined;

  const submit = async () => {
    if (creating || parentMade || !project || !title.trim()) return;
    setCreating(true);
    onBusy(true);
    setError('');
    const create: CreateBody = { repo: project.repo, host: project.host, fleet, title, body, issues: [] };
    const r = onCreate ? await onCreate({ repo: project.repo, title, body }) : await call<Campaign>('POST', '/campaigns/create', create, undefined, { timeoutMs: SLOW_TIMEOUT_MS });
    if (!alive.current) return;
    onBusy(false);
    if (r.ok) {
      if (!onCreate) {
        remember(project.id);
        openCampaign(r.data as Campaign);
        onCreated?.();
      }
      return onClose();
    }
    setCreating(false);
    setError(r.status === 409 && !r.parent ? 'This draft is being created already. Wait a moment and try again.' : r.error);
    if (r.parent) onFiled({ parent: r.parent, error: r.error });
  };

  if (!loaded) {
    return (
      <>
        <div class="launch-body">
          {loadError ? (
            <p class="alert" role="alert">
              {loadError}
            </p>
          ) : (
            <p class="note">Loading the fleet's projects…</p>
          )}
        </div>
        <div class="launch-foot">
          {onBack ? (
            <Button label="Back" onClick={onBack}>
              Back
            </Button>
          ) : null}
          {foot}
          <Button variant="primary" label="Create campaign" disabled>
            Create campaign
          </Button>
        </div>
      </>
    );
  }

  return (
    <>
      <div class="launch-body">
        <p class="note">{textCampaignNote(account)}</p>
        <label class="field">
          <span>Repo for the parent issue</span>
          <select aria-label="Repo for the parent issue" value={repoId} onChange={(e) => setRepoId(e.currentTarget.value)}>
            {repoId === '' ? (
              <option value="" disabled>
                Choose a repo
              </option>
            ) : null}
            {loaded.projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        {repoSeed !== undefined && repoId === '' ? <p class="note">{`${repoSeed} isn't one of this fleet's projects. Pick the repo for the parent issue.`}</p> : null}
        {loaded.problem ? <p class="note">{loaded.problem}</p> : null}
        {loaded.projects.length === 0 && !loaded.problem ? <p class="note">No project of this fleet is on a host it has a GitHub source for.</p> : null}
        <label class="field">
          <span>Title {drafted.title ? <em class="campaign-tag">Drafted</em> : null}</span>
          <input
            ref={titleBox}
            type="text"
            aria-label="Title"
            value={title}
            onInput={(e) => {
              setTitle(e.currentTarget.value);
              setDrafted((d) => ({ ...d, title: false }));
            }}
          />
        </label>
        <label class="field">
          <span>Description {drafted.body ? <em class="campaign-tag">Drafted</em> : null}</span>
          <textarea
            aria-label="Description"
            rows={6}
            value={body}
            onInput={(e) => {
              setBody(e.currentTarget.value);
              setDrafted((d) => ({ ...d, body: false }));
            }}
          />
        </label>
        {start.note ? <p class="line">{start.note}</p> : null}
        {error ? (
          <div class="alert" role="alert">
            <p>{error}</p>
            {parentMade ? (
              <p>
                The parent issue was filed at <ParentLink url={parentMade} />, but it wasn't followed. Nothing was retried; follow it from Add campaign.
              </p>
            ) : null}
          </div>
        ) : null}
      </div>
      <div class="launch-foot">
        {onBack ? (
          <Button label="Back" disabled={creating} onClick={onBack}>
            Back
          </Button>
        ) : null}
        {foot}
        <Button variant="primary" label="Create campaign" busyLabel="Creating campaign" busy={creating} disabled={!title.trim() || !project || creating || !!parentMade} onClick={() => void submit()}>
          Create campaign
        </Button>
      </div>
    </>
  );
}
