import { useId, useState } from 'preact/hooks';
import { call } from './api';
import { Actions, Button } from './Button';
import { crewApi, type CrewMember } from './crew';
import { usePollUntil } from './hooks/poll';
import { LineDiff } from './LineDiff';

interface Scan {
  state: string;
  findings: { line: number; rule: string }[];
  install?: string;
}

interface Preview {
  path: string;
  repo: string;
  bytes: string;
  sha256: string;
  replaces_blob: string | null;
  published_bytes?: string | null;
  scan: Scan;
}

const NO_SCAN = 'This file goes public without a secret scan. Read it for keys, tokens and passwords before you publish.';

function RepoStep({ fleetKey, harness, onRepo }: { fleetKey: string; harness: string; onRepo: (repo: string) => void }) {
  const [name, setName] = useState('admiral-crew');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [existing, setExisting] = useState('');
  const id = useId();
  const send = async (body: object) => {
    setBusy(true);
    setError('');
    setExisting('');
    const r = await call<{ share_repo: string }>('POST', crewApi(fleetKey, harness, '/sharing/repo'), body);
    setBusy(false);
    if (r.ok) return onRepo(r.data.share_repo);
    setError(r.error);
    if (r.use_existing) setExisting(r.use_existing);
  };
  return (
    <form
      class="crew-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!busy) void send({ name: name.trim() });
      }}
    >
      <p class="note">Crew members are published to a public GitHub repo. Admiral creates it under your account, or you can use one you have.</p>
      <div class="field crew-field">
        <label for={id}>Repo name</label>
        <input id={id} type="text" value={name} autocapitalize="none" autocomplete="off" onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} />
      </div>
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      <Actions class="crew-actions">
        <Button type="submit" variant="primary" label="Create repo" busy={busy} disabled={!name.trim()} />
      </Actions>
      {existing ? (
        <Actions class="crew-actions">
          <Button label={`Use ${existing}`} disabled={busy} onClick={() => void send({ repo: existing })} />
        </Actions>
      ) : null}
    </form>
  );
}

function PreviewStep({ member, fleetKey, harness, onDone, onClose }: { member: CrewMember; fleetKey: string; harness: string; onDone: () => Promise<void>; onClose: () => void }) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [skip, setSkip] = useState(false);
  const [busy, setBusy] = useState(false);
  const [commit, setCommit] = useState('');
  const base = `/members/${encodeURIComponent(member.name)}/share`;
  // One read of the file that would go public; returning true ends the poll after it.
  usePollUntil(
    async (isLive) => {
      const r = await call<Preview>('POST', crewApi(fleetKey, harness, `${base}/preview`));
      if (!isLive()) return true;
      setLoading(false);
      if (r.ok) setPreview(r.data);
      else setError(r.error);
      return true;
    },
    [fleetKey, harness, base],
    { everyMs: 1000 },
  );
  const missing = preview?.scan.state === 'missing';
  const publish = async () => {
    if (!preview || busy) return;
    setBusy(true);
    setError('');
    const r = await call<{ commit_url: string }>('POST', crewApi(fleetKey, harness, base), { sha256: preview.sha256, repo: preview.repo, replaces_blob: preview.replaces_blob, ...(skip && { skip_secret_scan: true }) });
    setBusy(false);
    if (!r.ok) return setError(r.error);
    setCommit(r.data.commit_url);
  };
  return (
    <>
      {loading ? <p class="line">Checking the file…</p> : null}
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      {preview ? (
        <>
          <p class="note">
            {preview.path} in {preview.repo}
          </p>
          {preview.replaces_blob && preview.published_bytes != null ? <LineDiff before={preview.published_bytes} after={preview.bytes} /> : <pre class="line-diff wrap">{preview.bytes}</pre>}
          {missing ? (
            <>
              <p class="note">{preview.scan.install}</p>
              <label class="check">
                <input type="checkbox" checked={skip} onChange={(e) => setSkip((e.currentTarget as HTMLInputElement).checked)} />
                {NO_SCAN}
              </label>
            </>
          ) : null}
        </>
      ) : null}
      {commit ? (
        <p class="note">
          <a href={commit} target="_blank" rel="noopener noreferrer">
            View the commit
          </a>
        </p>
      ) : null}
      <Actions class="crew-actions">
        {preview && !commit ? <Button variant="primary" label="Publish" busy={busy} disabled={missing && !skip} onClick={() => void publish()} /> : null}
        <Button label={commit ? 'Done' : 'Cancel'} onClick={() => void (commit ? onDone() : onClose())} />
      </Actions>
    </>
  );
}

/** Shows exactly the file that would go public and publishes it; with no share repo it first creates or adopts one. It stands in for the member's facts, so the file shows once. */
export function SharePanel({ member, fleetKey, harness, repo, onRepo, onPublished, onClose }: { member: CrewMember; fleetKey: string; harness: string; repo: string; onRepo: (repo: string) => void; onPublished: () => Promise<void>; onClose: () => void }) {
  const done = async () => {
    await onPublished();
    onClose();
  };
  return (
    <section class="crew-share" aria-label={`Share ${member.name}`}>
      <h3>Share {member.name}</h3>
      {repo ? <PreviewStep member={member} fleetKey={fleetKey} harness={harness} onDone={done} onClose={onClose} /> : <RepoStep fleetKey={fleetKey} harness={harness} onRepo={onRepo} />}
    </section>
  );
}

/** Takes the published file out of the share repo; the blob is the one the page read, so a changed file is refused. */
export function UnpublishConfirm({ name, repo, blob, fleetKey, harness, onDone, onCancel }: { name: string; repo: string; blob: string; fleetKey: string; harness: string; onDone: () => Promise<void>; onCancel: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const confirm = async () => {
    setBusy(true);
    setError('');
    const r = await call('DELETE', crewApi(fleetKey, harness, `/published/${encodeURIComponent(name)}`), { blob });
    setBusy(false);
    if (!r.ok) return setError(r.error);
    await onDone();
  };
  return (
    <div class="crew-confirm">
      <p>{`Delete \`crew/${name}.md\` from \`${repo}\`? People who imported it keep their copy; they'll see it as removed upstream. It stays in the repo's history.`}</p>
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      <Actions class="crew-actions">
        <Button variant="danger" label="Confirm unpublish" busy={busy} onClick={() => void confirm()} />
        <Button label="Cancel" onClick={onCancel} />
      </Actions>
    </div>
  );
}
