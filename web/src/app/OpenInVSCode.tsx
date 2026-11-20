import { useEffect, useState } from 'preact/hooks';
import { call, type Ship } from './api';
import { Button } from './Button';

interface Editors {
  local: boolean;
  vscode: { installed: boolean };
}

// null while loading or when the request failed: neither may show a reason that might be wrong.
function useEditors(): Editors | null {
  const [editors, setEditors] = useState<Editors | null>(null);
  useEffect(() => {
    let live = true;
    void call<Editors>('GET', '/editors').then((r) => {
      if (live && r.ok) setEditors(r.data);
    });
    return () => {
      live = false;
    };
  }, []);
  return editors;
}

/** The folder the ship works in: its worktree when it has one, else its checkout. */
const folder = (ship: Ship) => ship.worktree || ship.dir;

// VS Code's folder form is vscode://file/<absolute path>/; each segment is encoded so a space or # survives.
export function vscodeUrl(path: string): string | null {
  if (!path.startsWith('/')) return null;
  return `vscode://file${path.split('/').map(encodeURIComponent).join('/').replace(/\/+$/, '')}/`;
}

// The control and its note are separate so the note can sit under the header's actions.
export function OpenInVSCode({ ship }: { ship: Ship }) {
  const editors = useEditors();
  const url = vscodeUrl(folder(ship) ?? '');
  if (!url) return null;
  const open = !!editors && editors.local && editors.vscode.installed;
  return <Button label="Open in VS Code" class="open-in-vscode" href={open ? url : undefined} disabled={!open} />;
}

export function OpenInVSCodeNote({ ship }: { ship: Ship }) {
  const editors = useEditors();
  if (!editors || !vscodeUrl(folder(ship) ?? '')) return null;
  if (!editors.local) return <p class="note hand-note">Not available for remote users yet.</p>;
  if (!editors.vscode.installed) return <p class="note hand-note">VS Code isn&apos;t installed on this Mac.</p>;
  return null;
}
