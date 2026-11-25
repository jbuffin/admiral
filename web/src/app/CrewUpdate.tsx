import { useState } from 'preact/hooks';
import { call } from './api';
import { Actions, Button } from './Button';
import { crewApi } from './crew';
import { ScanNote } from './CrewImport';
import { usePollUntil } from './hooks/poll';
import { LineDiff } from './LineDiff';

interface UpdatePreview {
  repo: string;
  current_sha256: string;
  current_bytes: string;
  bytes: string;
  sha256: string;
  commit: string;
  tag: string | null;
  missing_skills: string[];
  scan: Parameters<typeof ScanNote>[0]['scan'];
  up_to_date: boolean;
}

/** Previews what the member's upstream changed and applies it only from that preview. */
export function CrewUpdate({ name, fleetKey, harness, reload, onApplied, onClose }: { name: string; fleetKey: string; harness: string; reload: () => Promise<void>; onApplied: () => Promise<void>; onClose: () => void }) {
  const [preview, setPreview] = useState<UpdatePreview | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  // Bumped to read the preview again after the files changed under it.
  const [round, setRound] = useState(0);
  const path = `/members/${encodeURIComponent(name)}/update`;

  usePollUntil(
    async (isLive) => {
      setBusy(true);
      if (round > 0) setPreview(null);
      const r = await call<UpdatePreview>('POST', crewApi(fleetKey, harness, `/members/${encodeURIComponent(name)}/update/preview`));
      if (!isLive()) return true;
      setBusy(false);
      if (!r.ok) {
        setPreview(null);
        setError(r.error);
        return true;
      }
      setPreview(r.data);
      return true;
    },
    [name, fleetKey, harness, round],
    { everyMs: 1000 },
  );

  const apply = async (p: UpdatePreview) => {
    setBusy(true);
    setError('');
    const r = await call('POST', crewApi(fleetKey, harness, path), { commit: p.commit, sha256: p.sha256, current_sha256: p.current_sha256 });
    setBusy(false);
    if (r.ok) {
      onClose();
      await onApplied();
      return;
    }
    await reload();
    if (r.kind === 'changed') setRound((n) => n + 1);
    setError(r.error);
  };

  return (
    <section class="crew-update" aria-label="Update">
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      {busy && !preview ? <p class="line">Reading the update…</p> : null}
      {preview ? (
        <div class="crew-preview">
          <p>Update from {preview.repo}</p>
          <p class="muted">
            Commit {preview.commit.slice(0, 7)}
            {preview.tag ? ` (tag ${preview.tag})` : ''}
          </p>
          {preview.up_to_date ? (
            <p class="note">Up to date</p>
          ) : (
            <>
              <LineDiff before={preview.current_bytes} after={preview.bytes} />
              {preview.missing_skills.map((s) => (
                <p class="note" key={s}>
                  Not installed: {s}
                </p>
              ))}
              <ScanNote scan={preview.scan} />
            </>
          )}
        </div>
      ) : null}
      <Actions class="crew-actions">
        {preview && !preview.up_to_date ? <Button variant="primary" label="Apply" busy={busy} onClick={() => void apply(preview)} /> : null}
        <Button label="Close" onClick={onClose} />
      </Actions>
    </section>
  );
}
