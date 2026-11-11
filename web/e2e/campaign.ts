import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { type Locator, type Page, expect } from '@playwright/test';
import type { Admiral } from './fixtures';
import { ORDERS, seedFleets } from './shell';

export const issueUrl = (n: number) => `https://github.com/acme/widgets/issues/${n}`;

type Tree = Record<string, unknown>;

interface NodeOpts {
  kids?: Tree[];
  labels?: string[];
  assignees?: string[];
  /** Open blockers, by issue number. */
  blockedBy?: number[];
  closed?: boolean;
}

const page1 = { hasNextPage: false, endCursor: null };

/** An issue as the GraphQL tree read answers it. */
export function issueNode(n: number, title: string, o: NodeOpts = {}): Tree {
  const kids = o.kids ?? [];
  return {
    __typename: 'Issue',
    number: n,
    title,
    url: issueUrl(n),
    state: o.closed ? 'CLOSED' : 'OPEN',
    body: '',
    repository: { nameWithOwner: 'acme/widgets' },
    labels: { nodes: (o.labels ?? []).map((name) => ({ name })), pageInfo: page1 },
    assignees: { nodes: (o.assignees ?? []).map((login) => ({ login })) },
    blockedBy: { nodes: (o.blockedBy ?? []).map((b) => ({ url: issueUrl(b), title: `Issue ${b}`, state: 'OPEN' })), pageInfo: page1 },
    subIssues: { totalCount: kids.length, nodes: kids, pageInfo: page1 },
  };
}

/** The canned `gh` answers of one test: tree reads by issue (needle `number=NN`, two digits so needles don't overlap) and an empty "Part of" search. */
export function fakeGhTrees(a: Admiral) {
  const dir = join(a.home, 'fakegh');
  const search = join(dir, 'search');
  mkdirSync(search, { recursive: true });
  writeFileSync(join(dir, 'token'), 'fake-token-default-1\n');
  writeFileSync(join(search, 'partof.match'), 'Part of');
  writeFileSync(join(search, 'partof.json'), JSON.stringify({ data: { search: { nodes: [], pageInfo: page1 } } }));
  return {
    tree(node: Tree) {
      const n = node.number as number;
      writeFileSync(join(search, `tree${n}.match`), `number=${n}`);
      writeFileSync(join(search, `tree${n}.json`), JSON.stringify({ data: { repository: { issueOrPullRequest: node } } }));
    },
    /** A canned tree for the issue and a read of each of its children. */
    campaign(root: Tree) {
      this.tree(root);
      for (const k of (root.subIssues as { nodes: Tree[] }).nodes) this.tree(k);
    },
  };
}

export const MAP = 20;
export const ANNEX = 30;
/** The annex's children, all `ready-for-agent`: Ready, Without you and Commodore. */
export const ANNEX_KIDS: [number, string][] = [
  [31, 'Annex doors'],
  [32, 'Annex lights'],
  [33, 'Annex vents'],
  [34, 'Annex pumps'],
];
export const T = {
  map: 'Moonbase map',
  grill: 'Grill the landing',
  survey: 'Survey the craters',
  rover: 'Prototype the rover',
  airlock: 'Plan the airlock',
  habitat: 'Assign the habitat',
  site: 'Sign off the site',
  annex: 'Moonbase annex',
  hangar: 'Paint the hangar',
};

/**
 * Westwind with a built-in GitHub source (no views) and the Orders test source, a checkout of acme/widgets, and canned
 * trees: the map (ready with you 21 and 23, ready without you 22, blocked 24, claimed with no ship 25, done 26) and an
 * annex of four plain ready children that an order offers to follow.
 */
export function seedCampaignFleet(a: Admiral) {
  seedFleets(a, { live: true });
  // The server has no Commodore; a slice runs it, so the claude dock's identity gets a fake one.
  const commodore = join(a.home, 'identity', 'zulu-claude', 'skills', 'commodore');
  mkdirSync(commodore, { recursive: true });
  writeFileSync(join(commodore, 'SKILL.md'), '---\nname: commodore\ndescription: a fake skill\n---\n');
  const file = join(a.home, 'config.json');
  const config = JSON.parse(readFileSync(file, 'utf8'));
  config.fleets.zulu.owners = ['acme'];
  config.sources.github = { plugin: 'github', name: 'GitHub', fleet: 'zulu', settings: { views: [] } };
  a.writeConfig(config);

  const widgets = join(a.home, 'widgets');
  mkdirSync(widgets, { recursive: true });
  spawnSync('git', ['init', '-q'], { cwd: widgets });
  spawnSync('git', ['remote', 'add', 'origin', 'https://github.com/acme/widgets.git'], { cwd: widgets });

  const gh = fakeGhTrees(a);
  gh.campaign(
    issueNode(MAP, T.map, {
      kids: [
        issueNode(21, T.grill, { labels: ['wayfinder:grilling'] }),
        issueNode(22, T.survey, { labels: ['wayfinder:research'] }),
        issueNode(23, T.rover, { labels: ['wayfinder:prototype'] }),
        issueNode(24, T.airlock, { labels: ['wayfinder:grilling'], blockedBy: [25] }),
        issueNode(25, T.habitat, { labels: ['wayfinder:task'], assignees: ['someone-else'] }),
        issueNode(26, T.site, { closed: true }),
      ],
    }),
  );
  gh.campaign(issueNode(ANNEX, T.annex, { kids: ANNEX_KIDS.map(([n, title]) => issueNode(n, title, { labels: ['ready-for-agent'] })) }));

  // Orders the Ships screen offers Follow on: one with sub-issues, one without.
  a.flags('orders').orders({
    orders: [
      ...ORDERS,
      { id: 'ord-annex', title: T.annex, text: 'Follow it.', url: issueUrl(ANNEX), sub_issues: ANNEX_KIDS.length },
      { id: 'ord-hangar', title: T.hangar, text: 'Paint it.', url: issueUrl(99) },
    ],
  });
}

/** Waits until the GitHub source has read its (empty) views, so a follow's tree read has a token and a route. */
export async function githubReady(a: Admiral) {
  await expect
    .poll(async () => {
      const body = (await (await fetch(`${a.url}api/v1/sources`)).json()) as { key: string; state: string }[] | { sources: { key: string; state: string }[] };
      const list = Array.isArray(body) ? body : body.sources;
      return list.find((s) => s.key === 'github')?.state;
    })
    .toBe('ok');
}

/** Follows by pasting into the Campaigns field, opening it first when it is behind the Add campaign button. */
export async function followByPaste(page: Page, press: (l: Locator) => Promise<void>, text: string) {
  const field = page.getByRole('textbox', { name: /campaign/i });
  if (!(await field.isVisible())) await press(page.getByRole('button', { name: 'Add campaign' }));
  await field.fill(text);
  await field.press('Enter');
}

export const campaignIdOf = (page: Page) => new URL(page.url()).pathname.replace(/\/$/, '').split('/').pop()!;

/** Launches a child of the campaign through the API, as a second window would, and answers the ship's name. */
export async function launchBehindThePage(a: Admiral, campaign: string, child: number): Promise<string> {
  const res = await fetch(`${a.url}api/v1/campaigns/${campaign}/launch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ child_url: issueUrl(child), harness: 'claude', dir: join(a.home, 'widgets') }),
  });
  expect(res.status).toBe(201);
  return ((await res.json()) as { ship: { name: string } }).ship.name;
}

/** Launches a slice of the campaign's children through the API, as a second window would, and answers the ship's name. */
export async function launchSliceBehindThePage(a: Admiral, campaign: string, children: number[]): Promise<string> {
  const res = await fetch(`${a.url}api/v1/campaigns/${campaign}/launch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ issues: children.map(issueUrl), harness: 'claude', dir: join(a.home, 'widgets') }),
  });
  expect(res.status).toBe(201);
  return ((await res.json()) as { ship: { name: string } }).ship.name;
}
