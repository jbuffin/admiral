import { useEffect, useState } from 'preact/hooks';
import { fetchUpdateStatus, type UpdateStatus } from './api';
import { CopyCommand } from './GitHubPage';
import { Button } from './Button';
import { Sheet } from './Sheet';

const REFRESH_MS = 5 * 60_000;
// The first check comes about a minute after Admiral starts, so ask again soon until one has landed.
const UNCHECKED_MS = 5_000;

/** Beside the build version: shown only while the server says a newer public release is out. */
export function UpdateBadge() {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [open, setOpen] = useState(false);
  const waiting = !status || (status.enabled && !status.checked_at);
  useEffect(() => {
    let live = true;
    const load = () =>
      void fetchUpdateStatus().then((s) => {
        if (live && s) setStatus(s);
      });
    load();
    const t = setInterval(load, waiting ? UNCHECKED_MS : REFRESH_MS);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [waiting]);
  if (!status?.update_available || !status.latest_version) return null;
  return (
    <>
      <button type="button" class="update-badge" onClick={() => setOpen(true)}>
        Update available
      </button>
      {open ? <UpdateSheet status={status} onClose={() => setOpen(false)} /> : null}
    </>
  );
}

function UpdateSheet({ status, onClose }: { status: UpdateStatus; onClose: () => void }) {
  return (
    <Sheet label="Update available" onClose={onClose} wide kind="update-sheet">
      <p class="sheet-sub">
        Admiral {status.latest_version} is out. You are running {status.current_version}.
      </p>
      <p class="sheet-sub">Upgrade with these steps, in order:</p>
      <ol class="update-steps">
        {status.upgrade_commands.map((c) => (
          <li key={c.command}>
            <span class="update-step-label">{c.label}</span>
            <CopyCommand command={c.command} />
          </li>
        ))}
      </ol>
      <p class="sheet-sub">{status.restart_note}</p>
      <p class="sheet-sub">
        Admiral is running from <code>{status.executable_path}</code>.
      </p>
      <div class="launch-foot">
        <Button label="View the release on GitHub" href={status.release_url} external />
        <Button label="Close" variant="primary" onClick={onClose} />
      </div>
    </Sheet>
  );
}
