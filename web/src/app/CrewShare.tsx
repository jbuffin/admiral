import { useId, useRef, useState } from 'preact/hooks';
import { call } from './api';
import { Actions, Button } from './Button';
import { crewApi, type CrewMember } from './crew';
import { usePollUntil } from './hooks/poll';
import { LineDiff } from './LineDiff';
import { renderMarkdown } from './markdown';
import { githubPath, go } from './router';

export interface Scan {
  state: string;
  findings: { line: number; rule: string }[];
  install?: string;
  error?: string;
}

interface Preview {
  path: string;
  repo: string;
  bytes: string;
  sha256: string;
  private: boolean;
  replaces_blob: string | null;
  published_bytes?: string | null;
  scan: Scan;
}

/** What the Crew page learns from the fleet's sharing view. */
export interface SharingView {
  connected: boolean;
  login: string;
  published_error: string;
  crew_sharing: { enabled: boolean; share_repo: string };
  repo?: { exists: boolean; private?: boolean; archived?: boolean; pushable?: boolean; error?: string };
}

const noScanText = (repo: string) => `This file goes to ${repo} without a secret scan. Anyone who can read that repo can read it. Read it for keys, tokens and passwords before you publish.`;
const GITLEAKS_DOCS = 'https://github.com/gitleaks/gitleaks#installing';
const CHOOSE_ANOTHER = 'Choose another repo';
const isNoAccess = (r: { status?: number; code?: string }) => r.status === 403 && r.code === 'no_access';

export const noScan = (scan: Scan) => scan.state === 'missing' || scan.state === 'too-old';

export function scanLine(scan: Scan): string {
  if (scan.state === 'clean') return 'Secret scan: nothing found.';
  if (scan.state === 'too-old') return 'Secret scan: not run, the installed gitleaks is too old.';
  if (scan.state === 'missing') return 'Secret scan: not run, gitleaks is not installed.';
  return `Secret scan: ${scan.state}.`;
}

/** The link to the fleet's GitHub page, for a fleet that isn't connected. */
export function ConnectGitHubLink({ fleetKey }: { fleetKey: string }) {
  return (
    <a
      href={githubPath(fleetKey)}
      onClick={(e) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        go(githubPath(fleetKey), '');
      }}
    >
      Connect GitHub
    </a>
  );
}

function RepoStep({ fleetKey, harness, login, onRepo }: { fleetKey: string; harness: string; login: string; onRepo: (repo: string) => void }) {
  const [name, setName] = useState('admiral-crew');
  const [description, setDescription] = useState('Crew members published from Admiral');
  const [existing, setExisting] = useState('');
  const [isPrivate, setPrivate] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [offer, setOffer] = useState('');
  const [readmeText, setReadmeText] = useState('');
  const id = useId();
  const owner = login || 'your-account';
  // The README is templated on the server, so what shows is what will be written.
  usePollUntil(
    async (isLive) => {
      const r = await call<{ readme: string }>('POST', crewApi(fleetKey, harness, '/sharing/repo'), { preview: true, owner, name: name.trim() });
      if (isLive()) setReadmeText(r.ok ? r.data.readme : '');
      return true;
    },
    [fleetKey, harness, owner, name],
    { everyMs: 1000 },
  );
  const send = async (body: object) => {
    setBusy(true);
    setError('');
    setOffer('');
    const r = await call<{ share_repo: string }>('POST', crewApi(fleetKey, harness, '/sharing/repo'), body);
    setBusy(false);
    if (r.ok) return onRepo(r.data.share_repo);
    setError(r.error);
    if (r.use_existing) setOffer(r.use_existing);
  };
  return (
    <form
      class="crew-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!busy) void send({ name: name.trim(), description: description.trim(), private: isPrivate });
      }}
    >
      <p class="note">Crew members are published to a GitHub repo you own. Admiral creates it under your account, or you can use one you have.</p>
      <div class="field crew-field">
        <span>Owner</span>
        <strong>{owner}</strong>
      </div>
      <div class="field crew-field">
        <label for={id}>Repo name</label>
        <input id={id} type="text" value={name} autocapitalize="none" autocomplete="off" onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} />
      </div>
      <div class="field crew-field">
        <label for={`${id}-description`}>Description</label>
        <input id={`${id}-description`} type="text" value={description} autocomplete="off" onInput={(e) => setDescription((e.currentTarget as HTMLInputElement).value)} />
      </div>
      <fieldset class="field crew-field">
        <legend>Visibility</legend>
        <label class="check">
          <input type="radio" name={`${id}-visibility`} checked={!isPrivate} onChange={() => setPrivate(false)} />
          <span>Public</span>
        </label>
        <p class="note">Anyone can read and import your crew members.</p>
        <label class="check">
          <input type="radio" name={`${id}-visibility`} checked={isPrivate} onChange={() => setPrivate(true)} />
          <span>Private</span>
        </label>
        <p class="note">Only you and people you add to the repo can read and import them.</p>
      </fieldset>
      {readmeText ? (
        <>
          <p class="note">The README Admiral will write first:</p>
          <div class="crew-readme md" dangerouslySetInnerHTML={{ __html: renderMarkdown(readmeText) }} />
        </>
      ) : null}
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      <Actions class="crew-actions">
        <Button type="submit" variant="primary" label={isPrivate ? 'Create private repo' : 'Create public repo'} busy={busy} disabled={!name.trim()} />
      </Actions>
      {offer ? (
        <Actions class="crew-actions">
          <Button label={`Use ${offer}`} disabled={busy} onClick={() => void send({ repo: offer })} />
        </Actions>
      ) : null}
      <div class="field crew-field">
        <label for={`${id}-existing`}>Use an existing repo</label>
        <input id={`${id}-existing`} type="text" value={existing} placeholder="owner/name" autocapitalize="none" autocomplete="off" onInput={(e) => setExisting((e.currentTarget as HTMLInputElement).value)} />
      </div>
      <Actions class="crew-actions">
        <Button label="Use this repo" disabled={busy || !existing.trim()} onClick={() => void send({ repo: existing.trim() })} />
      </Actions>
    </form>
  );
}

function PreviewStep({ member, fleetKey, harness, onDone, onClose, onChoose }: { member: CrewMember; fleetKey: string; harness: string; onDone: () => Promise<void>; onClose: () => void; onChoose: () => void }) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [repoProblem, setRepoProblem] = useState(false);
  const [skip, setSkip] = useState(false);
  const [busy, setBusy] = useState(false);
  const [commit, setCommit] = useState('');
  const base = `/members/${encodeURIComponent(member.name)}/share`;
  // usePollUntil as a one-shot load: it runs while `loading` is true and a returned true ends it; the repo has no load hook of its own.
  usePollUntil(
    async (isLive) => {
      const r = await call<Preview>('POST', crewApi(fleetKey, harness, `${base}/preview`));
      if (!isLive()) return true;
      setLoading(false);
      if (r.ok) {
        setPreview(r.data);
      } else {
        setPreview(null);
        setError(r.error);
        setRepoProblem(r.status === 409 || isNoAccess(r));
      }
      return true;
    },
    [fleetKey, harness, base],
    { everyMs: 1000, active: loading },
  );
  const reload = () => {
    setLoading(true);
    setSkip(false);
  };
  const unscanned = preview ? noScan(preview.scan) : false;
  const publish = async () => {
    if (!preview || busy) return;
    setBusy(true);
    setError('');
    const r = await call<{ commit_url: string }>('POST', crewApi(fleetKey, harness, base), { sha256: preview.sha256, repo: preview.repo, private: preview.private, replaces_blob: preview.replaces_blob, ...(skip && { skip_secret_scan: true }) });
    setBusy(false);
    if (r.ok) return setCommit(r.data.commit_url);
    setError(r.error);
    if (isNoAccess(r)) return setRepoProblem(true);
    if (r.status === 409) {
      // The share repo changed, or the member or the published file did; either way the preview is stale.
      setRepoProblem(r.error.includes(CHOOSE_ANOTHER));
      reload();
    }
  };
  const commitLink = commit.startsWith('https://github.com/') ? commit : '';
  return (
    <>
      {loading ? <p class="line">Checking the file…</p> : null}
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      {repoProblem ? (
        <Actions class="crew-actions">
          <Button label={CHOOSE_ANOTHER} onClick={onChoose} />
        </Actions>
      ) : null}
      {preview && !loading ? (
        <>
          <p class="note">
            {preview.path} in {preview.repo}
          </p>
          {preview.replaces_blob && preview.published_bytes != null ? <p class="note">Replaces the published version</p> : null}
          <p class="note">{preview.private ? `${preview.repo} is private. Anyone with access to it can read this file.` : `${preview.repo} is public. Anyone can read this file.`}</p>
          <p class="note">The file that goes to {preview.repo}, exactly:</p>
          <pre class="line-diff" aria-label="File to publish">
            {preview.bytes}
          </pre>
          {preview.replaces_blob && preview.published_bytes != null ? (
            <>
              <p class="note">What changes:</p>
              <LineDiff before={preview.published_bytes} after={preview.bytes} />
            </>
          ) : null}
          <p class="note">{scanLine(preview.scan)}</p>
          {unscanned ? (
            <>
              <p class="note">
                {preview.scan.install} <a href={GITLEAKS_DOCS} target="_blank" rel="noopener noreferrer">gitleaks install guide</a>
              </p>
              <label class="check">
                <input type="checkbox" checked={skip} onChange={(e) => setSkip((e.currentTarget as HTMLInputElement).checked)} />
                {noScanText(preview.repo)}
              </label>
            </>
          ) : null}
        </>
      ) : null}
      {commitLink ? (
        <p class="note">
          <a href={commitLink} target="_blank" rel="noopener noreferrer">
            View the commit
          </a>
        </p>
      ) : null}
      {commit && !commitLink ? <p class="note">Published.</p> : null}
      <Actions class="crew-actions">
        {preview && !commit && !loading ? <Button variant="primary" label="Publish" busy={busy} disabled={unscanned && !skip} onClick={() => void publish()} /> : null}
        <Button label={commit ? 'Done' : 'Cancel'} onClick={() => void (commit ? onDone() : onClose())} />
      </Actions>
    </>
  );
}

/** Shows exactly the file that would go to the share repo and publishes it; with no share repo it first creates or adopts one. It stands in for the member's facts, so the file shows once. */
export function SharePanel({ member, fleetKey, harness, repo, login, onRepo, onPublished, onClose }: { member: CrewMember; fleetKey: string; harness: string; repo: string; login: string; onRepo: (repo: string) => void; onPublished: () => Promise<void>; onClose: () => void }) {
  const [choosing, setChoosing] = useState(false);
  const done = async () => {
    await onPublished();
    onClose();
  };
  return (
    <section class="crew-share" aria-label={`Share ${member.name}`}>
      <h3>Share {member.name}</h3>
      {repo && !choosing ? (
        <PreviewStep member={member} fleetKey={fleetKey} harness={harness} onDone={done} onClose={onClose} onChoose={() => setChoosing(true)} />
      ) : (
        <RepoStep
          fleetKey={fleetKey}
          harness={harness}
          login={login}
          onRepo={(next) => {
            setChoosing(false);
            onRepo(next);
          }}
        />
      )}
    </section>
  );
}

interface PublishedFile {
  name: string;
  repo: string;
  bytes: string;
  blob: string;
}

/** Takes the published file out of the share repo. It shows the file as GitHub holds it now, and deletes that blob, so a changed file is refused and reloaded. */
export function UnpublishConfirm({ name, fleetKey, harness, onDone, onGone, onCancel }: { name: string; fleetKey: string; harness: string; onDone: () => Promise<void>; onGone: () => Promise<void>; onCancel: () => void }) {
  const [file, setFile] = useState<PublishedFile | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const gone = useRef(onGone);
  gone.current = onGone;
  const url = crewApi(fleetKey, harness, `/published/${encodeURIComponent(name)}`);
  // One-shot load, as in the preview; setting `loading` again re-runs it after a 409.
  usePollUntil(
    async (isLive) => {
      const r = await call<PublishedFile>('GET', url);
      if (!isLive()) return true;
      setLoading(false);
      if (r.ok) setFile(r.data);
      else {
        setFile(null);
        setError(r.error);
        if (r.status === 404) await gone.current();
      }
      return true;
    },
    [url],
    { everyMs: 1000, active: loading },
  );
  const confirm = async () => {
    if (!file || busy) return;
    setBusy(true);
    setError('');
    const r = await call('DELETE', url, { blob: file.blob, repo: file.repo });
    setBusy(false);
    if (r.ok) return void (await onDone());
    if (r.status === 404) return void (await onGone());
    setError(r.error);
    if (r.status === 409) {
      setLoading(true);
    }
  };
  return (
    <div class="crew-confirm">
      {loading ? <p class="line">Reading the published file…</p> : null}
      {file && !loading ? (
        <>
          <p>Delete crew/{name}.md from {file.repo}? People who imported it keep their copy; they'll see it as removed upstream. It stays in the repo's history.</p>
          <pre class="line-diff" aria-label="Published file">
            {file.bytes}
          </pre>
        </>
      ) : null}
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      <Actions class="crew-actions">
        <Button variant="danger" label="Confirm unpublish" busy={busy} disabled={!file || loading} onClick={() => void confirm()} />
        <Button label="Cancel" onClick={onCancel} />
      </Actions>
    </div>
  );
}
