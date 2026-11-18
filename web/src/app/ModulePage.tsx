import { useEffect, useState } from 'preact/hooks';
import { Button } from './Button';
import { fetchModulePage, restartSource, type ModulePageData } from './api';
import { sources } from './shipsLink';

const isWebUrl = (u?: string) => !!u && /^https?:\/\//i.test(u);

function SignIn({ auth }: { auth: NonNullable<ModulePageData['auth']> }) {
  return (
    <div class="signin">
      <strong>Sign-in needed</strong>
      <p>{auth.message}</p>
      {isWebUrl(auth.url) ? (
        <a href={auth.url} target="_blank" rel="noopener noreferrer">
          {auth.url}
        </a>
      ) : null}
      {auth.code ? (
        <div class="signin-code">
          <code>{auth.code}</code>
        </div>
      ) : null}
    </div>
  );
}

/** The page's place while its route is still pending or its plugin is starting: nothing is fetched. */
export function ModuleSkeleton({ name, title, phone }: { name: string; title: boolean; phone?: boolean }) {
  return (
    <main class={phone ? 'panel module-page phone' : 'panel module-page'}>
      {title ? <h1>{name}</h1> : null}
      <div class="skeleton" role="status" aria-label="Loading" />
    </main>
  );
}

export function ModulePage({ sourceKey, title, phone }: { sourceKey: string; title: boolean; phone?: boolean }) {
  const source = sources.value.find((s) => s.key === sourceKey);
  const version = source?.page?.version;
  const [page, setPage] = useState<ModulePageData | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [problem, setProblem] = useState('');

  // Refetch whenever the page version moves.
  useEffect(() => {
    let stale = false;
    void fetchModulePage(sourceKey).then((r) => {
      if (stale) return;
      if (r.ok) {
        setPage(r.data);
        setProblem('');
      } else setProblem(r.error);
    });
    return () => {
      stale = true;
    };
  }, [sourceKey, version]);

  const restart = async () => {
    setRestarting(true);
    const r = await restartSource(sourceKey);
    setRestarting(false);
    setProblem(r.ok ? '' : r.error);
  };

  return (
    <main class={phone ? 'panel module-page phone' : 'panel module-page'}>
      {title ? <h1>{source?.name ?? sourceKey}</h1> : null}
      {(!page && !problem) || page?.state === 'starting' ? <div class="skeleton" role="status" aria-label="Loading" /> : null}
      {problem ? (
        <p class="note error" role="alert">
          {problem}
        </p>
      ) : null}
      {page?.state === 'auth_required' && page.auth ? <SignIn auth={page.auth} /> : null}
      {page?.state === 'error' ? (
        <div class="note error" role="alert">
          <p>{page.error}</p>
          <Button label="Restart" busy={restarting} onClick={() => void restart()} />
        </div>
      ) : null}
      {page?.groups.map((g) => (
        <section key={g.id} class="module-group">
          <h2>{g.title}</h2>
          <ul>
            {g.rows.map((r) => (
              <li key={r.id}>{r.title}</li>
            ))}
          </ul>
        </section>
      ))}
    </main>
  );
}
