export interface Build {
  version: string;
  commit: string;
}

/** Reads the `<version> <commit>` stamp the server writes into the shell page; none outside the Go server. */
export function parseBuildStamp(content: string | null): Build | null {
  if (!content) return null;
  const [version, commit] = content.trim().split(/\s+/);
  return version && commit ? { version, commit } : null;
}

const same = (a: Build, b: Build) => a.version === b.version && a.commit === b.commit;

/** One reload per mismatched server build: the guard names the build already reloaded for, and clears once the shell matches. */
export function reloadDecision(stamp: Build | null, server: Build, guard: string | null): { reload: boolean; guard: string | null } {
  if (!stamp || same(stamp, server)) return { reload: false, guard: null };
  const key = `${server.version} ${server.commit}`;
  return guard === key ? { reload: false, guard } : { reload: true, guard: key };
}

const GUARD_KEY = 'admiral-reloaded-for';

/** The build the server runs, or null on anything but a 200: /api/version stays behind auth, so a 401 is no check. */
export async function fetchServerBuild(): Promise<Build | null> {
  const res = await fetch('/api/version', { cache: 'no-store' });
  return res.status === 200 ? ((await res.json()) as Build) : null;
}

/** Asks the server which build it runs and reloads the shell once when it is not the shell's own. */
export async function checkBuild(): Promise<void> {
  const stamp = parseBuildStamp(document.querySelector('meta[name="admiral-build"]')?.getAttribute('content') ?? null);
  if (!stamp) return;
  try {
    const server = await fetchServerBuild();
    if (!server) return;
    const d = reloadDecision(stamp, server, sessionStorage.getItem(GUARD_KEY));
    if (d.guard === null) sessionStorage.removeItem(GUARD_KEY);
    else sessionStorage.setItem(GUARD_KEY, d.guard);
    if (d.reload) location.reload();
  } catch {
    /* unreachable or storage blocked: the next socket open asks again */
  }
}
