import { useId, useState } from 'preact/hooks';
import { call } from './api';
import { Actions, Button } from './Button';
import { crewApi } from './crew';
import { ConnectGitHubLink, noScan, type Scan, scanLine, UnpublishConfirm, type SharingView } from './CrewShare';
import { usePollUntil } from './hooks/poll';
import { renderMarkdown } from './markdown';

interface Preview {
  source?: string;
  bytes: string;
  card: { name: string; role: string; 'based-on': string | null; skills: string[]; model: string | null; harness: string | null; 'pick-me-when': string; instructions?: string };
  owner: string;
  repo: string;
  path: string;
  commit: string;
  tag: string | null;
  sha256: string;
  clash: { name: string; suggested: string | null } | null;
  missing_skills: string[];
  scan: Scan;
}

interface Row {
  name: string;
  path: string;
  imported: string | null;
  published: boolean;
}

type Sharing = SharingView & { crew_sharing: { sources: string[] } };

const slash = (repo: string) => repo.split('/').map(encodeURIComponent).join('/');

function ScanNote({ scan }: { scan: Scan }) {
  if (noScan(scan)) return null;
  if (scan.state === 'clean') return <p class="note">{scanLine(scan)}</p>;
  if (scan.state === 'hit') {
    return (
      <>
        <p class="note">The secret scan found something in this file. You can still import it.</p>
        <ul class="crew-skills">
          {scan.findings.map((f, i) => (
            <li key={i}>Possible secret on line {f.line}: {f.rule}</li>
          ))}
        </ul>
      </>
    );
  }
  return <p class="note">Secret scan did not run: {scan.error || scan.state}.</p>;
}

/** Previews a shared member by ref or URL, imports it, and manages the repos the fleet imports from. */
export function CrewImport({ fleetKey, harness, reload, onClose }: { fleetKey: string; harness: string; reload: () => Promise<void>; onClose: () => void }) {
  const id = useId();
  const [view, setView] = useState<Sharing | null>(null);
  const [sources, setSources] = useState<string[]>([]);
  const [ref, setRef] = useState('');
  const [preview, setPreview] = useState<Preview | null>(null);
  // What the shown preview was asked for, which a Browse row sets without touching the box.
  const [previewed, setPreviewed] = useState('');
  const [name, setName] = useState('');
  const [newSource, setNewSource] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState('');
  const [browsing, setBrowsing] = useState<{ repo: string; rows: Row[] } | null>(null);
  const [unpublishing, setUnpublishing] = useState('');
  // The role of each browse row whose file has been read, by repo and path.
  const [roles, setRoles] = useState<Record<string, string>>({});

  usePollUntil(
    async (isLive) => {
      const r = await call<Sharing>('GET', crewApi(fleetKey, harness, '/sharing'));
      if (!isLive()) return true;
      if (!r.ok) setError(r.error);
      else {
        setView(r.data);
        setSources(r.data.crew_sharing.sources ?? []);
      }
      return true;
    },
    [fleetKey, harness],
    { everyMs: 1000 },
  );

  const loadPreview = async (text: string) => {
    setBusy(true);
    setError('');
    setDone('');
    const r = await call<Preview>('POST', crewApi(fleetKey, harness, '/imports/preview'), { ref: text });
    setBusy(false);
    if (!r.ok) {
      setPreview(null);
      setError(r.error);
      return;
    }
    setPreview(r.data);
    setPreviewed(text);
    setName(r.data.clash?.suggested ?? '');
    if (!r.data.source) setRoles((was) => ({ ...was, [`${r.data.owner}/${r.data.repo}/${r.data.path}`]: r.data.card.role }));
  };

  const putSources = async (next: string[]) => {
    setBusy(true);
    setError('');
    const r = await call<{ sources: string[] }>('PUT', crewApi(fleetKey, harness, '/sources'), { sources: next });
    setBusy(false);
    if (!r.ok) {
      setError(r.error);
      return false;
    }
    setSources(r.data.sources);
    return true;
  };

  const browse = async (repo: string) => {
    setError('');
    const r = await call<{ members: Row[] }>('GET', crewApi(fleetKey, harness, `/sources/${slash(repo)}`));
    if (!r.ok) return setError(r.error);
    setBrowsing({ repo, rows: r.data.members });
  };

  const doImport = async (p: Preview) => {
    setBusy(true);
    setError('');
    const typed = name.trim();
    const r = await call<{ name: string; missing_skills: string[] }>('POST', crewApi(fleetKey, harness, '/imports'), { repo: `${p.owner}/${p.repo}`, path: p.path, commit: p.commit, sha256: p.sha256, ...(typed ? { name: typed } : {}) });
    setBusy(false);
    if (!r.ok) {
      if (r.suggested !== undefined) {
        setPreview({ ...p, clash: { name: typed || p.card.name, suggested: r.suggested || null } });
        setName(r.suggested);
        setError(r.error);
        return;
      }
      if (r.status === 409) await loadPreview(previewed);
      setError(r.error);
      return;
    }
    setPreview(null);
    setRef('');
    setDone(`Imported as ${r.data.name}.${r.data.missing_skills.length > 0 ? ` Not installed here: ${r.data.missing_skills.join(', ')}.` : ''}`);
    await reload();
    if (browsing) await browse(browsing.repo);
  };

  const shareRepo = view?.crew_sharing.share_repo ?? '';
  const repos = [...(shareRepo ? [shareRepo] : []), ...sources];
  return (
    <section class="crew-import" aria-label="Import">
      {view && !view.connected ? (
        <p class="note">
          Connect GitHub (github.com) for this fleet first. <ConnectGitHubLink fleetKey={fleetKey} />
        </p>
      ) : (
        <>
          <form
            class="crew-form"
            onSubmit={(e) => {
              e.preventDefault();
              if (!busy && ref.trim()) void loadPreview(ref.trim());
            }}
          >
            <div class="field crew-field">
              <label for={id}>Ref or URL</label>
              <input id={id} type="text" value={ref} placeholder="owner/repo/name or a github.com URL" autocapitalize="none" autocomplete="off" onInput={(e) => setRef((e.currentTarget as HTMLInputElement).value)} />
            </div>
            <Actions class="crew-actions">
              <Button type="submit" variant="primary" label="Preview" busy={busy} disabled={!ref.trim()} />
            </Actions>
          </form>
          {error ? (
            <p class="alert" role="alert">
              {error}
            </p>
          ) : null}
          {done ? <p class="note">{done}</p> : null}
          {preview?.source ? (
            <Actions class="crew-actions">
              <Button
                label="Add as a crew source"
                disabled={busy}
                onClick={async () => {
                  const added = preview.source!;
                  if (await putSources([...sources, added])) {
                    setPreview(null);
                    setRef('');
                    await browse(added);
                  }
                }}
              />
            </Actions>
          ) : null}
          {preview && !preview.source ? (
            <div class="crew-preview">
              <p>
                Shared by {preview.owner}/{preview.repo}
              </p>
              <p class="muted">
                Commit {preview.commit.slice(0, 7)}
                {preview.tag ? ` (tag ${preview.tag})` : ''}
              </p>
              <div class="crew-card" role="group" aria-label="Member card">
                <h3>{preview.card.name}</h3>
                <p class="muted">{preview.card.role}</p>
                <dl class="crew-facts">
                  {[
                    ['Based on', preview.card['based-on']],
                    ['Skills', preview.card.skills.length > 0 ? preview.card.skills.join(', ') : null],
                    ['Model', preview.card.model],
                    ['Harness', preview.card.harness],
                  ].map(([label, value]) =>
                    value ? (
                      <div key={label}>
                        <dt>{label}</dt>
                        <dd>{value}</dd>
                      </div>
                    ) : null,
                  )}
                </dl>
                <div class="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(preview.card['pick-me-when']) }} />
                {preview.card.instructions ? <div class="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(preview.card.instructions) }} /> : null}
              </div>
              <pre class="line-diff" aria-label="File to import">
                {preview.bytes}
              </pre>
              {preview.missing_skills.map((s) => (
                <p class="note" key={s}>
                  Not installed: {s}
                </p>
              ))}
              <ScanNote scan={preview.scan} />
              {preview.clash ? (
                <>
                  <p class="note">The name {preview.clash.name} is already taken in this fleet. Choose another name.</p>
                  <div class="field crew-field">
                    <label for={`${id}-name`}>Name in this fleet</label>
                    <input id={`${id}-name`} type="text" value={name} autocapitalize="none" autocomplete="off" onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} />
                  </div>
                </>
              ) : null}
              <Actions class="crew-actions">
                <Button variant="primary" label="Import" busy={busy} disabled={!!preview.clash && !name.trim()} onClick={() => void doImport(preview)} />
              </Actions>
            </div>
          ) : null}
          <ul class="crew-sources">
            {shareRepo ? (
              <li>
                <span>Your share repo</span> <code>{shareRepo}</code>
                <Button label="Browse" onClick={() => void browse(shareRepo)} />
              </li>
            ) : null}
            {sources.map((s) => (
              <li key={s}>
                <code>{s}</code>
                <Button label="Browse" onClick={() => void browse(s)} />
                <Button
                  label="Remove"
                  disabled={busy}
                  onClick={async () => {
                    if (await putSources(sources.filter((x) => x !== s)) && browsing?.repo === s) setBrowsing(null);
                  }}
                />
              </li>
            ))}
          </ul>
          <form
            class="crew-form"
            onSubmit={async (e) => {
              e.preventDefault();
              if (!busy && newSource.trim() && (await putSources([...sources, newSource.trim()]))) setNewSource('');
            }}
          >
            <div class="field crew-field">
              <label for={`${id}-source`}>Source repo</label>
              <input id={`${id}-source`} type="text" value={newSource} placeholder="owner/repo" autocapitalize="none" autocomplete="off" onInput={(e) => setNewSource((e.currentTarget as HTMLInputElement).value)} />
            </div>
            <Actions class="crew-actions">
              <Button type="submit" label="Add source" busy={busy} disabled={!newSource.trim()} />
            </Actions>
          </form>
          {browsing ? (
            <ul class="crew-browse" aria-label={browsing.repo}>
              {browsing.rows.map((m) => (
                <li key={m.name}>
                  <span>{m.name}</span>
                  {roles[`${browsing.repo}/${m.path}`] ? <span class="muted">{roles[`${browsing.repo}/${m.path}`]}</span> : null}
                  {m.imported ? <span class="pill">Imported</span> : <Button label="Import" disabled={busy} onClick={() => void loadPreview(`${browsing.repo}/${m.name}`)} />}
                  {browsing.repo === shareRepo ? <Button label="Unpublish" onClick={() => setUnpublishing(m.name)} /> : null}
                </li>
              ))}
            </ul>
          ) : null}
          {unpublishing ? (
            <UnpublishConfirm
              name={unpublishing}
              fleetKey={fleetKey}
              harness={harness}
              onCancel={() => setUnpublishing('')}
              onDone={async () => {
                setUnpublishing('');
                if (browsing) await browse(browsing.repo);
                await reload();
              }}
              onGone={async () => {
                setUnpublishing('');
                if (browsing) await browse(browsing.repo);
                await reload();
              }}
            />
          ) : null}
        </>
      )}
      <Actions class="crew-actions">
        <Button label="Close" onClick={onClose} />
      </Actions>
    </section>
  );
}
