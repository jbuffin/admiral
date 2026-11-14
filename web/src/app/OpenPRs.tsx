import { prLabel } from './moored';

/** A Moor sheet's lead-in: the ship's name, how many open PRs it carries, and each as a link. */
export function OpenPRs({ name, urls }: { name: string; urls: string[] }) {
  return (
    <>
      <p class="line">
        {name} carries {urls.length === 1 ? '1 open PR' : `${urls.length} open PRs`}.
      </p>
      <ul class="pr-list">
        {urls.map((url) => (
          <li key={url}>
            <a class="pr-link" href={url} target="_blank" rel="noreferrer">
              {prLabel(url)}
            </a>
          </li>
        ))}
      </ul>
    </>
  );
}
