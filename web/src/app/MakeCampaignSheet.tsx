import { useEffect, useState } from 'preact/hooks';
import { call, fetchSettings, SLOW_TIMEOUT_MS } from './api';
import { Button } from './Button';
import type { Campaign } from './campaign';
import { noteCampaign } from './campaignsLink';
import { rememberFleet } from './fleets';
import { Icon } from './Icon';
import { campaignNote, createBody, createFailure, moveIssue, repoChoices, safeHref, type SheetIssue } from './makeCampaign';
import { campaignPath, go } from './router';
import { Sheet } from './Sheet';

interface Props {
  fleet: string;
  /** The key of the fleet's GitHub source, whose account creates the parent issue. */
  sourceKey?: string;
  issues: SheetIssue[];
  /** The fleet's projects; the choices are the issues' own repos when this is empty. */
  projects: string[];
  onClose: () => void;
}

interface Draft {
  title: string;
  body: string;
}

export function MakeCampaignSheet({ fleet, sourceKey, issues: given, projects, onClose }: Props) {
  const [issues, setIssues] = useState(given);
  const { choices, initial } = repoChoices(given.map((i) => i.url), projects);
  const [repo, setRepo] = useState(initial);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [drafted, setDrafted] = useState({ title: false, body: false });
  const [drafting, setDrafting] = useState(true);
  const [note, setNote] = useState('');
  const [account, setAccount] = useState<string | undefined>();
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');
  const [dragging, setDragging] = useState(-1);
  const [parentMade, setParentMade] = useState<{ parent: string; linked: string[] } | null>(null);

  useEffect(() => {
    let live = true;
    void (async () => {
      const r = await call<Draft>('POST', `/fleets/${encodeURIComponent(fleet)}/campaigns/draft`, { issues: given.map(({ url, title }) => ({ url, title })) }, undefined, { timeoutMs: SLOW_TIMEOUT_MS });
      if (!live) return;
      setDrafting(false);
      if (!r.ok) return setNote(`Couldn't draft a title and description: ${r.error}. Write your own.`);
      setTitle((t) => t || r.data.title);
      setBody((b) => b || r.data.body);
      setDrafted({ title: true, body: true });
    })();
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    if (!sourceKey) return;
    let live = true;
    void fetchSettings(sourceKey).then((r) => {
      const a = r.ok ? r.data.settings.account : undefined;
      if (live && typeof a === 'string') setAccount(a.trim());
    });
    return () => {
      live = false;
    };
  }, [sourceKey]);

  const create = async () => {
    if (creating || !title.trim()) return;
    setCreating(true);
    setError('');
    const r = await call<Campaign>('POST', '/campaigns/create', createBody(repo, title, body, issues), undefined, { timeoutMs: SLOW_TIMEOUT_MS });
    if (r.ok) {
      noteCampaign(r.data);
      rememberFleet(r.data.fleet);
      return go(campaignPath(r.data.id), `?fleet=${encodeURIComponent(r.data.fleet)}`);
    }
    setCreating(false);
    setParentMade(r.parent ? { parent: r.parent, linked: r.linked ?? [] } : null);
    setError(r.parent ? createFailure({ error: r.error, parent: r.parent, linked: r.linked }) : r.error);
  };

  const link = (url: string) => {
    const href = safeHref(url);
    return href ? (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {url}
      </a>
    ) : (
      <span>{url}</span>
    );
  };

  return (
    <Sheet label="Make campaign" kind="campaign-backdrop" onClose={onClose}>
      <div class="campaign-sheet">
        <p class="sheet-sub">{campaignNote(account)}</p>
        <div class="campaign-body">
          <label class="field">
            <span>Repo for the parent issue</span>
            <select aria-label="Repo for the parent issue" value={repo} onChange={(e) => setRepo(e.currentTarget.value)}>
              {choices.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </label>
          <label class="field">
            <span>
              Title {drafted.title ? <em class="campaign-tag">Drafted</em> : null}
            </span>
            <input
              type="text"
              aria-label="Title"
              value={title}
              placeholder={drafting ? 'Drafting a title…' : ''}
              aria-busy={drafting}
              onInput={(e) => {
                setTitle(e.currentTarget.value);
                setDrafted((d) => ({ ...d, title: false }));
              }}
            />
          </label>
          <label class="field">
            <span>
              Description {drafted.body ? <em class="campaign-tag">Drafted</em> : null}
            </span>
            <textarea
              aria-label="Description"
              rows={4}
              value={body}
              placeholder={drafting ? 'Drafting a description…' : ''}
              aria-busy={drafting}
              onInput={(e) => {
                setBody(e.currentTarget.value);
                setDrafted((d) => ({ ...d, body: false }));
              }}
            />
          </label>
          {note ? <p class="line">{note}</p> : null}
          <div class="field">
            <span id="campaign-subs">Sub-issues, in order</span>
            <ol class="campaign-subs" aria-label="Sub-issues">
              {issues.map((it, i) => (
                <li
                  key={it.url}
                  class={dragging === i ? 'dragging' : ''}
                  draggable
                  onDragStart={() => setDragging(i)}
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={() => {
                    if (dragging >= 0) setIssues(moveIssue(issues, dragging, i));
                    setDragging(-1);
                  }}
                  onDragEnd={() => setDragging(-1)}
                >
                  <span class="campaign-grip" aria-hidden="true">
                    ⠿
                  </span>
                  <span class="campaign-sub">
                    <span class="campaign-sub-title">{it.title}</span>
                    <span class="mono">{it.ref}</span>
                  </span>
                  <Button icon variant="quiet" label={`Move up ${it.title}`} disabled={i === 0} onClick={() => setIssues(moveIssue(issues, i, i - 1))}>
                    <span class="campaign-up">
                      <Icon name="chevron" size={16} />
                    </span>
                  </Button>
                  <Button icon variant="quiet" label={`Move down ${it.title}`} disabled={i === issues.length - 1} onClick={() => setIssues(moveIssue(issues, i, i + 1))}>
                    <Icon name="chevron" size={16} />
                  </Button>
                </li>
              ))}
            </ol>
          </div>
          {error ? (
            <div class="alert" role="alert">
              <p>{error}</p>
              {parentMade ? (
                <>
                  <p>Parent: {link(parentMade.parent)}</p>
                  {parentMade.linked.length > 0 ? (
                    <ul>
                      {parentMade.linked.map((u) => (
                        <li key={u}>{link(u)}</li>
                      ))}
                    </ul>
                  ) : null}
                </>
              ) : null}
            </div>
          ) : null}
        </div>
        <div class="launch-foot">
          <Button label="Cancel" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" label="Create campaign" busyLabel="Creating campaign" busy={creating} disabled={!title.trim() || creating} onClick={create}>
            Create campaign
          </Button>
        </div>
      </div>
    </Sheet>
  );
}
