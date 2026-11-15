import type { OrderRow } from './ordersView';

export interface SheetIssue {
  url: string;
  title: string;
  /** `<owner>/<repo> #<n>`. */
  ref: string;
}

export interface RepoChoices {
  choices: string[];
  initial: string;
}

export interface CreateFailure {
  error: string;
  parent?: string;
  linked?: string[];
}

export interface CreateBody {
  repo: string;
  title: string;
  body: string;
  issues: string[];
}

const ISSUE_URL = /^https:\/\/[^/]+\/([^/]+)\/([^/]+)\/issues\/(\d+)$/;

function parse(url: string): { owner: string; repo: string; n: string } | undefined {
  const m = ISSUE_URL.exec(url);
  return m ? { owner: m[1], repo: m[2], n: m[3] } : undefined;
}

/** A deep link never fetches an issue, so a ref no loaded order holds is titled by its place. */
export function sheetIssues(refs: string[], rows: OrderRow[]): SheetIssue[] {
  const titles = new Map(rows.map((r) => [r.order.url, r.order.title]));
  return refs.map((url) => {
    const p = parse(url);
    const ref = p ? `${p.owner}/${p.repo} #${p.n}` : url;
    return { url, title: titles.get(url) || ref, ref };
  });
}

/** The server refuses a parent repo of another owner, so a fleet project counts only under the issues' owner. */
export function repoChoices(issueUrls: string[], projects: string[]): RepoChoices {
  const own = issueUrls.flatMap((u) => {
    const p = parse(u);
    return p ? [{ owner: p.owner, repo: `${p.owner}/${p.repo}` }] : [];
  });
  const owner = own[0]?.owner;
  const choices = [...new Set([...own.map((o) => o.repo), ...projects.filter((p) => p.split('/')[0] === owner)])].sort();
  const counts = new Map<string, number>();
  for (const o of own) counts.set(o.repo, (counts.get(o.repo) ?? 0) + 1);
  let initial = own[0]?.repo ?? '';
  for (const [repo, n] of counts) if (n > (counts.get(initial) ?? 0)) initial = repo;
  return { choices, initial };
}

export function moveIssue<T>(list: readonly T[], from: number, to: number): T[] {
  const out = [...list];
  if (from < 0 || from >= out.length || to < 0 || to >= out.length) return out;
  out.splice(to, 0, ...out.splice(from, 1));
  return out;
}

export function createBody(repo: string, title: string, body: string, issues: readonly { url: string }[]): CreateBody {
  return { repo, title, body, issues: issues.map((i) => i.url) };
}

export function createFailure(e: CreateFailure): string {
  if (!e.parent) return e.error;
  const linked = e.linked?.length ? ` Linked so far, in order: ${e.linked.join(', ')}.` : ' None of the issues were linked.';
  return `The parent issue was made at ${e.parent}, but some links failed: ${e.error}.${linked} Nothing was retried; finish the links on GitHub.`;
}

export function campaignNote(account: string | undefined): string {
  const who = account ? `@${account}` : "the fleet's gh account";
  return `Admiral creates a parent issue on GitHub as ${who} and links these issues under it. Then it follows the campaign. Nothing launches.`;
}

export const safeHref = (url: string): string | undefined => (url.startsWith('https://') ? url : undefined);
