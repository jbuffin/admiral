import { useEffect, useRef, useState } from 'preact/hooks';
import { call, fetchFleetRepos, fetchSettings, fetchSources, SLOW_TIMEOUT_MS } from './api';
import { Button } from './Button';
import type { Campaign } from './campaign';
import { openCampaign } from './campaignsLink';
import { capabilities, capabilitiesLoaded, loadCapabilities } from './capabilities';
import { draftingOff, MAX_PROSE_RUNES } from './filing';
import { accountForHost, initialProject, projectChoices, safeHref, sourceHost, textCampaignNote, type Project } from './makeCampaign';
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
}

/** The step that asks what the campaign is for and has the drafter title it. */
export function TextStep({ fleet, text, onText, onBack, onConfirm, onBusy }: TextProps) {
  const [busy, setBusy] = useState(false);
  const [refused, setRefused] = useState(false);
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
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

  return (
    <>
      <div class="launch-body">
        <label class="field">
          <span>What is this campaign for?</span>
          <textarea aria-label="What is this campaign for?" rows={6} value={text} onInput={(e) => onText(e.currentTarget.value)} />
        </label>
        <p class={tooLong ? 'alert' : 'note'}>
          {size.toLocaleString('en-US')} / {MAX_PROSE_RUNES.toLocaleString('en-US')}
        </p>
      </div>
      <div class="launch-foot">
        <Button label="Back" disabled={busy} onClick={onBack}>
          Back
        </Button>
        <Button variant="primary" label={plain ? 'Next' : 'Draft'} busyLabel="Drafting" busy={busy} disabled={text.trim() === '' || tooLong || !capabilitiesLoaded.value} onClick={() => void next()}>
          {plain ? 'Next' : 'Draft'}
        </Button>
      </div>
    </>
  );
}

interface Loaded {
  projects: Project[];
  accounts: { key: string; settings: Record<string, unknown> }[];
}

interface ConfirmProps {
  fleet: string;
  start: Confirmed;
  onClose: () => void;
  onBack: () => void;
  onBusy: (b: boolean) => void;
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
export function ConfirmStep({ fleet, start, onClose, onBack, onBusy }: ConfirmProps) {
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [loadError, setLoadError] = useState('');
  const [repoId, setRepoId] = useState('');
  const [title, setTitle] = useState(start.title);
  const [body, setBody] = useState(start.body);
  const [drafted, setDrafted] = useState(start.drafted);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');
  const [parentMade, setParentMade] = useState('');
  const alive = useRef(true);
  useEffect(() => () => void (alive.current = false), []);

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
      const projects = projectChoices(repos.data.repos ?? [], new Set(accounts.map((a) => sourceHost(a.settings))));
      setRepoId(initialProject(projects, remembered()));
      setLoaded({ projects, accounts });
    })();
    return () => {
      live = false;
    };
  }, [fleet]);

  const project = loaded?.projects.find((p) => p.id === repoId);
  const account = project && loaded ? accountForHost(loaded.accounts, project.host)?.account : undefined;

  const create = async () => {
    if (creating || parentMade || !project || !title.trim()) return;
    setCreating(true);
    onBusy(true);
    setError('');
    const r = await call<Campaign>(
      'POST',
      '/campaigns/create',
      {
        repo: project.repo,
        host: project.host,
        fleet,
        title,
        body,
        issues: [],
      },
      undefined,
      { timeoutMs: SLOW_TIMEOUT_MS },
    );
    if (!alive.current) return;
    onBusy(false);
    if (r.ok) {
      remember(project.id);
      openCampaign(r.data);
      return onClose();
    }
    setCreating(false);
    setParentMade(r.parent ?? '');
    setError(r.error);
  };

  if (!loaded) {
    return loadError ? (
      <div class="launch-body">
        <p class="alert" role="alert">
          {loadError}
        </p>
      </div>
    ) : (
      <div class="launch-body">
        <p class="note">Loading the fleet's projects…</p>
      </div>
    );
  }

  return (
    <>
      <div class="launch-body">
        <p class="note">{textCampaignNote(account)}</p>
        <label class="field">
          <span>Repo for the parent issue</span>
          <select aria-label="Repo for the parent issue" value={repoId} onChange={(e) => setRepoId(e.currentTarget.value)}>
            {loaded.projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        {loaded.projects.length === 0 ? <p class="note">No project of this fleet is on a host it has a GitHub source for.</p> : null}
        <label class="field">
          <span>Title {drafted.title ? <em class="campaign-tag">Drafted</em> : null}</span>
          <input
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
        <Button label="Back" disabled={creating} onClick={onBack}>
          Back
        </Button>
        <Button variant="primary" label="Create campaign" busyLabel="Creating campaign" busy={creating} disabled={!title.trim() || !project || creating || !!parentMade} onClick={() => void create()}>
          Create campaign
        </Button>
      </div>
    </>
  );
}
