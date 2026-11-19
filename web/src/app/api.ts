// The only place the shell builds /api/v1 URLs.
import type { Limits } from './limitsForm';
import { signedOut } from './session';
export interface Dock {
  harness: string;
  executable: string;
  executable_found: boolean;
  warnings: string[];
}
export interface FleetChat {
  harness: string;
  model: string;
  effort: string;
  context_threshold: number | 'never';
  on: boolean;
  off_reason: string;
  /** The harnesses this fleet's chat may run on right now. */
  harnesses: string[];
  /** Every harness the fleet has a dock for, offered or not (with why not), and the models and efforts its chat takes. */
  harness_options: ChatHarnessOption[];
  /** The models and efforts of the fleet's own chat harness. */
  models: string[];
  efforts: string[];
}
export interface ChatHarnessOption {
  harness: string;
  offered: boolean;
  /** One line naming the harness and the missing capability; empty when offered. */
  reason: string;
  capability: string;
  models: string[];
  efforts: string[];
}
export interface Fleet {
  key: string;
  name: string;
  color: string;
  ink: string;
  color_index?: number;
  search_roots: string[];
  owners: string[];
  wake_reviewers: string[];
  clone_root: string;
  can_launch: boolean;
  docks: Dock[];
  chat: FleetChat;
  /** Whether this fleet pushes and alerts; on unless config.json says false. */
  notify: boolean;
  preset: 'work' | 'home';
  limits: Limits;
  /** One sentence per Limit in catalog order, then two fixed ones with an empty field. */
  sentences: { field: string; sentence: string }[];
  /** The Limits this fleet stores over its preset, as sentence fields. */
  overridden: string[];
}

export interface PRActor {
  login: string;
  bot: boolean;
}
export interface PRStackEntry {
  position: number;
  number: number;
  url: string;
  state: string;
  head: string;
}
export interface PRStack {
  number: number;
  size: number;
  base: string;
  url: string;
  entries: PRStackEntry[];
}
/** One PR a ship carries, read fresh from GitHub: GET /api/ships/<session>/pr?url=<PR URL>. */
export interface PRDetail {
  number: number;
  url: string;
  title: string;
  body: string;
  state: string;
  draft: boolean;
  head: string;
  base: string;
  review: string;
  checks: string;
  mergeable: string;
  merge_state: string;
  failing_checks: { name: string; url: string }[];
  author: PRActor;
  reviews: { author: PRActor; state: string }[];
  requested: PRActor[];
  threads: { path: string; line: number; resolved: boolean; comments: { author: PRActor; body: string; url: string; created_at: string }[] }[];
  comments: { author: PRActor; body: string; url: string; created_at: string }[];
  stack: PRStack | null;
  // The command Answer all reviews would send, only when the ship is moored and the whole stack has feedback.
  answer_all?: string;
}
/** A ship's PR shape: GET /api/ships/<session>/stack. `prs` is bottom to top for stacked and chained, the ship's own order otherwise. */
export interface StackShape {
  shape: 'stacked' | 'chained' | 'independent' | 'mixed' | 'none';
  stack_number?: number;
  prs: string[];
}
export type ShipStatus = 'needs_you' | 'underway' | 'moored' | 'anchored' | 'sailing_off';
export interface MooredPR {
  url: string;
  state: string;
  review: string;
  checks: string;
  mergeable: string;
  conflicts: boolean;
  /** The branch the whole stack conflicts with, set on its top PR when the server's check found it. */
  conflicts_with_base?: string;
  ready?: boolean;
  /** True while an order's PR is a draft; only orders carry it, a ship's PR tab reads its PR detail. */
  draft?: boolean;
  // New waking events since the ship's last round, and the command that answers them; both only on a moored ship's open PR.
  feedback?: number;
  answer?: string;
  author?: string;
  reviewing?: boolean;
  responded?: boolean;
  // Merge problems besides a conflict, on an open PR the ship opened.
  behind?: boolean;
  failing_checks?: string[];
  /** Every reason GitHub would refuse a merge now, by code, in the server's order. */
  merge_reasons?: string[];
}
/** What keeps one open PR from merging that its ship can fix. */
export interface PRProblems {
  pr: string;
  conflicts?: boolean;
  behind?: boolean;
  failing_checks?: string[];
  /** The branch the whole stack conflicts with, set on its top PR. */
  conflicts_with_base?: string;
}
/** The Fix PR / Fix stack offer on a moored ship: the PR the round is on, the command it sends and every PR's problems. */
export interface FixOffer {
  pr: string;
  /** The round covers more than one PR. */
  stack?: boolean;
  command: string;
  problems: PRProblems[];
}
export interface Ship {
  session: string;
  name: string;
  fleet: string;
  harness: string;
  status: ShipStatus;
  order_id: string;
  title: string;
  tail: string;
  fleet_name?: string;
  fleet_color: string;
  launched: string;
  // The first launch's stamp: a wake or a hand relaunch moves `launched` but not this.
  first_launched?: string;
  // "admiralbot" for a ship AdmiralBot launched.
  via?: string;
  // When a needs-you ship started waiting; omitted otherwise.
  stop_since?: string;
  // The engineer has looked at this stop; the ship still needs you, so its question can be answered.
  seen?: boolean;
  // Set from Return to port until the ship is stopped.
  returning?: boolean;
  // Set from Hand to Commodore until the ship is relaunched or the hand fails.
  handing?: boolean;
  // Why the ship cannot be handed to Commodore, when its own options say so.
  hand_refusal?: string;
  // Why the last hand failed on a ship that stayed live, until the next hand starts.
  hand_failed?: string;
  // The skill the ship was launched with; null when none.
  skill?: string | null;
  // `commodore` on a Commodore ship.
  skill_kind?: string;
  // Set while the ship drafts a campaign, until its campaign is followed, it is handed to Commodore, or it ends.
  drafting?: boolean;
  // Why a drafting ship's campaign was not followed; omitted when empty.
  drafting_notice?: string;
  // A live ship woken from its mooring: it returns to port by itself once its PRs are merged.
  carries_prs?: boolean;
  // A live ship whose report lists PRs GitHub confirms open: it can be moored.
  open_prs?: string[];
  crew_live: number;
  // Set on a moored ship; `stale` is omitted while false.
  moored_at?: string;
  polled_at?: string;
  stale?: boolean;
  no_resume?: boolean;
  waiting?: string;
  prs?: MooredPR[];
  fix?: FixOffer;
  stack_ready?: boolean;
  ready_up_to?: string;
  merge_method?: 'merge' | 'squash' | 'rebase';
  url?: string;
  // The main link of a ship launched without an order, from its first prompt; it claims no issue.
  link?: string;
  // Set while Admiral works out the ship's title.
  titling?: boolean;
  // Every issue a slice ship holds, `url` first; a single-issue ship omits it.
  issues?: string[];
  source?: string;
  dir: string;
  repo: string;
  reason?: string;
  exit_code: number | null;
  exit_signal: string | null;
  // Why Admiral ended the harness: wall_clock:<minutes>.
  stopped_by?: string;
  crew: CrewBoat[];
}

export interface CrewBoat {
  name: string;
  role?: string;
  conversation: boolean;
  harness: string;
  reason?: string;
  status: string;
  launched?: string;
  last_activity?: string;
  exit_code: number | null;
  exit_signal: string | null;
}

export interface ToolCall {
  name: string;
  summary?: string;
  input?: string;
  output?: string;
  error?: boolean;
  done: boolean;
  /** Told by the Codex app-server, which also marks the one call an approval it waits on names. */
  live?: boolean;
  approving?: boolean;
}
export interface Question {
  header?: string;
  text: string;
  options?: { label: string; description?: string }[];
  answer?: string;
  pending: boolean;
  multiSelect?: boolean;
}
export type ItemKind = 'user' | 'assistant' | 'tool_call' | 'question' | 'turn_end' | 'compaction' | 'notice' | 'round';
export interface Item {
  id: string;
  kind: ItemKind;
  at?: string;
  text?: string;
  meta?: boolean;
  tool?: ToolCall;
  question?: Question;
  durationMs?: number;
  round?: number;
  state?: 'none' | 'gone' | 'unreadable';
}
export type ConversationState = 'ok' | 'none' | 'unreadable' | 'gone';
export interface ConversationPage {
  state: ConversationState;
  items: Item[] | null;
  more: boolean;
}

const API = '/api/v1';

/** A ship's URL, or its crew boat's when one is named. */
export const shipUrl = (session: string, crew?: string) => `${API}/ships/${encodeURIComponent(session)}${crew === undefined ? '' : `/crew/${encodeURIComponent(crew)}`}`;

export const conversationUrl = (session: string, before?: string, crew?: string) =>
  `${shipUrl(session, crew)}/conversation${before === undefined ? '' : `?before=${encodeURIComponent(before)}`}`;

/** An ended drafting ship's conversation, read through its Inbox item since the ship is gone. */
export const itemConversationUrl = (session: string, id: string, before?: string) =>
  `${API}/inbox/${encodeURIComponent(session)}/${encodeURIComponent(id)}/conversation${before === undefined ? '' : `?before=${encodeURIComponent(before)}`}`;

const fleetsOf = (body: { fleets?: Fleet[] | null }): Fleet[] =>
  (body.fleets ?? []).map((f) => ({
    ...f,
    search_roots: f.search_roots ?? [],
    owners: f.owners ?? [],
    wake_reviewers: f.wake_reviewers ?? [],
    docks: (f.docks ?? []).map((d) => ({ ...d, warnings: d.warnings ?? [] })),
    chat: { ...f.chat, harnesses: f.chat?.harnesses ?? [], harness_options: f.chat?.harness_options ?? [], models: f.chat?.models ?? [], efforts: f.chat?.efforts ?? [] },
    notify: f.notify ?? true,
    preset: f.preset ?? 'work',
    sentences: f.sentences ?? [],
    overridden: f.overridden ?? [],
  }));

export async function fetchFleets(): Promise<Fleet[]> {
  const res = await fetch(`${API}/config`);
  if (!res.ok) throw new Error(`config: ${res.status}`);
  return fleetsOf(await res.json());
}

export const logoUrl = (harness: string) => `${API}/logos/${encodeURIComponent(harness)}`;

export type Result<T> = { ok: true; data: T } | { ok: false; error: string; status?: number; field?: string; note?: string; kind?: string; reason?: string; parent?: string; linked?: string[]; output?: string; commands?: string[]; aborted?: boolean };

export const DEFAULT_TIMEOUT_MS = 30_000;
export const SLOW_TIMEOUT_MS = 3 * 60_000;
/** Above the server's 45 s budget for a re-request, so its timeout message reaches the page. */
const REREQUEST_TIMEOUT_MS = 60_000;
export const LAUNCH_TIMEOUT_MS = 10 * 60_000;

const limitText = (ms: number) => (ms % 60_000 === 0 ? `${ms / 60_000} min` : `${ms / 1000} s`);

export interface CallOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

export async function call<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>, opts: CallOptions = {}): Promise<Result<T>> {
  const limit = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const ctl = new AbortController();
  let timedOut = false;
  const onCallerAbort = () => ctl.abort();
  if (opts.signal?.aborted) ctl.abort();
  else opts.signal?.addEventListener('abort', onCallerAbort);
  const timer = setTimeout(() => {
    timedOut = true;
    ctl.abort();
  }, limit);
  try {
    const res = await fetch(path.startsWith(`${API}/`) ? path : API + path, {
      method,
      headers: body === undefined && headers === undefined ? undefined : { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: ctl.signal,
    });
    const text = await res.text();
    let data: unknown = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      /* not JSON: the status speaks */
    }
    if (res.status === 401) signedOut.value = true;
    if (!res.ok) {
      const body = data as { error?: string; field?: string; note?: unknown; kind?: unknown; reason?: unknown; parent?: unknown; linked?: unknown; output?: unknown; commands?: unknown } | null;
      return {
        ok: false,
        status: res.status,
        error: body?.error ?? `${method} ${path}: ${res.status}`,
        ...(body?.field && { field: body.field }),
        ...(typeof body?.note === 'string' && { note: body.note }),
        ...(typeof body?.kind === 'string' && { kind: body.kind }),
        ...(typeof body?.reason === 'string' && { reason: body.reason }),
        ...(typeof body?.parent === 'string' && { parent: body.parent }),
        ...(Array.isArray(body?.linked) && body.linked.every((x) => typeof x === 'string') && { linked: body.linked as string[] }),
        ...(typeof body?.output === 'string' && { output: body.output }),
        ...(Array.isArray(body?.commands) && body.commands.every((x) => typeof x === 'string') && { commands: body.commands as string[] }),
      };
    }
    return { ok: true, data: data as T };
  } catch (e) {
    if (timedOut) return { ok: false, error: `Admiral didn't answer in ${limitText(limit)}. It may still finish, so check before trying again.` };
    if (ctl.signal.aborted) return { ok: false, error: 'The request was stopped.', aborted: true };
    return { ok: false, error: e instanceof Error ? e.message : 'The request failed.' };
  } finally {
    clearTimeout(timer);
    opts.signal?.removeEventListener('abort', onCallerAbort);
  }
}

const mergeToken = () => ({ 'X-Admiral-Merge-Token': document.querySelector<HTMLMetaElement>('meta[name="admiral-merge-token"]')?.content ?? '' });

/** Merges a moored ship's single PR, its whole stack, or with `pr` one independent PR by URL. The token is the one the server put in this page; it changes when Admiral restarts. */
export function mergeShip(session: string, pr?: string): Promise<Result<unknown>> {
  return call('POST', `/ships/${encodeURIComponent(session)}/merge`, pr ? { pr } : undefined, mergeToken(), { timeoutMs: SLOW_TIMEOUT_MS });
}

/** Updates a moored ship's PR branch with its base, through the fleet's gh. Sends the merge token like Merge. */
export function updateBranch(session: string, pr: string): Promise<Result<unknown>> {
  return call('POST', `/ships/${encodeURIComponent(session)}/update-branch`, { pr }, mergeToken(), { timeoutMs: SLOW_TIMEOUT_MS });
}

/** What marking one PR ready for review came to. */
export interface ReadyForReviewResult {
  url: string;
  outcome: 'marked_ready' | 'already_ready_for_review' | 'failed';
  error?: string;
}

/** Takes the named draft PRs, bottom to top, out of draft through the fleet's gh, and answers each one's outcome. Sends the merge token like Merge. */
export function markReadyForReview(session: string, prs: string[]): Promise<Result<{ results: ReadyForReviewResult[] }>> {
  return call('POST', `/ships/${encodeURIComponent(session)}/ready-for-review`, { prs }, mergeToken(), { timeoutMs: SLOW_TIMEOUT_MS });
}

/** How a ship's PRs relate on GitHub; 404 for a ship that is anchored or has no PRs. */
export function fetchStackShape(session: string): Promise<Result<StackShape>> {
  return call<StackShape>('GET', `/ships/${encodeURIComponent(session)}/stack`);
}

/** Links the ship's chained PRs into one GitHub stack and answers the shape it then reads. */
export function linkStack(session: string): Promise<Result<StackShape>> {
  return call<StackShape>('POST', `/ships/${encodeURIComponent(session)}/stack`, undefined, mergeToken(), { timeoutMs: SLOW_TIMEOUT_MS });
}

/** Links a source's chained PRs, bottom to top, into one GitHub stack and answers the shape it then reads. */
export function linkSourceStack(source: string, prs: string[]): Promise<Result<StackShape>> {
  return call<StackShape>('POST', `${src(source)}/stack`, { prs }, mergeToken(), { timeoutMs: SLOW_TIMEOUT_MS });
}

/** One PR a ship carries, read from GitHub: 404 when the ship doesn't carry it, 502 when GitHub fails. */
export function fetchPRDetail(session: string, url: string): Promise<Result<PRDetail>> {
  return call<PRDetail>('GET', `/ships/${encodeURIComponent(session)}/pr?url=${encodeURIComponent(url)}`);
}

/** Ask GitHub to request another review from `login`, a GitHub reviewer who already reviewed the PR. */
export function reRequestReview(session: string, pr: string, login: string): Promise<Result<unknown>> {
  return call<unknown>('POST', `/ships/${encodeURIComponent(session)}/pr/rerequest`, { pr, login }, undefined, { timeoutMs: REREQUEST_TIMEOUT_MS });
}

export interface Source {
  key: string;
  name: string;
  plugin: string;
  fleet: string;
  icon_url: string;
  has_schema: boolean;
  state: string;
  error: string | null;
  warnings: string[];
  /** The kinds of event the source raises; absent until it has listed them. */
  events?: { kind: string; title: string; panel?: string }[];
  /** A GitHub source's sign-in status; absent for any other source. */
  connection?: Connection;
}
export type ConnectionState = 'checking' | 'connected' | 'signed_out' | 'gh_missing' | 'unreachable';
export interface Connection {
  state: ConnectionState;
  host: string;
  account?: string;
  login?: string;
  /** RFC 3339; absent until a check has run. */
  checked_at?: string;
  /** The commands that turn a signed-out or gh-missing state into a connected one, in order. */
  fix: string[];
  message?: string;
}
export interface Repos {
  repos: string[];
  owners: string[];
  default: boolean;
  warnings: string[];
}
export interface PluginInfo {
  name: string;
  description: string;
  icon_url: string;
  valid: boolean;
  error: string;
}

// The server names icons by their unversioned path; the shell asks for them under /api/v1.
export const iconSrc = (url: string) => (url ? url.replace(/^\/api\//, `${API}/`) : '');

const src = (k: string) => `/sources/${encodeURIComponent(k)}`;

export async function fetchSources(): Promise<Result<Source[]>> {
  const r = await call<Source[] | null>('GET', '/sources');
  return r.ok ? { ok: true, data: (r.data ?? []).map((s) => ({ ...s, warnings: s.warnings ?? [] })) } : r;
}
export interface FleetRepo {
  path: string;
  name: string;
  kind: string;
  /** `host/owner/repo` ids. */
  remotes: string[] | null;
}
export const fetchFleetRepos = (fleet: string) => call<{ repos: FleetRepo[] | null }>('GET', `/fleets/${encodeURIComponent(fleet)}/repos`);
export const fetchPlugins = () => call<PluginInfo[]>('GET', '/plugins');
export const fetchSchema = (key: string) => call<unknown>('GET', `${src(key)}/schema`);
export const fetchSettings = (key: string) => call<{ name: string; settings: Record<string, unknown> }>('GET', `${src(key)}/settings`);
export const saveSource = (key: string, body: { name?: string; fleet?: string; settings?: Record<string, unknown> }) => call<Source>('PUT', src(key), body);
export const restartSource = (key: string) => call<unknown>('POST', `${src(key)}/restart`);
/** Asks the source to search again now; the new answer arrives with the usual stream. */
export const refreshSource = (key: string) => call<unknown>('POST', `${src(key)}/refresh`);
/** Asks GitHub now whether gh is signed in; a refusal with no `connection` (the source isn't running) keeps the last one. */
export const checkConnection = (key: string) => call<Connection>('POST', `${src(key)}/connection/check`);
/** How many results a search has, as GitHub counts them. */
export const countSearch = (key: string, query: string, signal?: AbortSignal) => call<{ count: number }>('POST', `${src(key)}/search-count`, { query }, undefined, { signal });
export const fetchRepos = (key: string) => call<Repos>('GET', `${src(key)}/repos`);
export const removeSource = (key: string) => call<unknown>('DELETE', src(key));
export const addSource = (plugin: string, fleet: string) => call<Source>('POST', '/sources', { plugin, fleet });

/** Saves the fleet fields in the body; the server answers the new config view. */
export async function saveFleet(key: string, body: Partial<Pick<Fleet, 'search_roots' | 'owners' | 'wake_reviewers' | 'clone_root' | 'color' | 'notify' | 'preset'>> | { limits: Record<string, unknown> } | { chat: Partial<Omit<FleetChat, 'on' | 'off_reason' | 'harnesses' | 'harness_options' | 'models' | 'efforts'>> }): Promise<Result<Fleet[]>> {
  const r = await call<{ fleets?: Fleet[] | null }>('PUT', `/fleets/${encodeURIComponent(key)}`, body);
  return r.ok ? { ok: true, data: fleetsOf(r.data) } : r;
}
export interface ChatNote {
  id: string;
  kind: string;
  text: string;
  ts: string;
  via?: string;
}
export interface ChatCommitment {
  id: string;
  text: string;
  trigger: { kind: 'ship_sailed' | 'ship_anchored' | 'pr_merged' | 'pr_closed' | 'issue_closed' | 'time' | 'none' | 'campaign'; subject?: string; launched?: string };
  action: 'launch' | 'remind' | 'work';
  launch?: unknown;
  harness?: string;
  launched?: number;
  waiting_children?: number;
  proposal?: { url: string; question: string };
  ts: string;
  via?: string;
  waiting?: boolean;
  wait_reason?: string;
  shown?: boolean;
}
export interface Instructions {
  text: string;
  max: number;
}
const chatPath = (fleet: string, rest: string) => `/chat/${encodeURIComponent(fleet)}/${rest}`;
export async function getNotes(fleet: string): Promise<Result<ChatNote[]>> {
  const r = await call<{ notes?: ChatNote[] | null }>('GET', chatPath(fleet, 'notes'));
  return r.ok ? { ok: true, data: r.data.notes ?? [] } : r;
}
export const getInstructions = (fleet: string) => call<Instructions>('GET', chatPath(fleet, 'instructions'));
export const saveInstructions = (fleet: string, text: string) => call<Instructions>('PUT', chatPath(fleet, 'instructions'), { text });
export const deleteNote = (fleet: string, id: string) => call<unknown>('DELETE', chatPath(fleet, `notes/${encodeURIComponent(id)}`));
export async function getCommitments(fleet: string): Promise<Result<ChatCommitment[]>> {
  const r = await call<{ commitments?: ChatCommitment[] | null }>('GET', chatPath(fleet, 'commitments'));
  return r.ok ? { ok: true, data: r.data.commitments ?? [] } : r;
}
export const cancelCommitment = (fleet: string, id: string) => call<unknown>('DELETE', chatPath(fleet, `commitments/${encodeURIComponent(id)}`));
export const promoteNote = (fleet: string, id: string) => call<Instructions>('POST', chatPath(fleet, `notes/${encodeURIComponent(id)}/promote`));
export const startSetup = () => call<unknown>('POST', '/setup');
export interface AllowRule {
  harness: string;
  /** The remote's owner/repo or the checkout's path; a Codex rule has none, since it covers every repo of the fleet. */
  repo?: string;
  rule?: string;
  pattern?: string[];
  added?: string;
}
/** One rule of an allow rule proposal in the Inbox. */
export interface AllowRuleCard extends AllowRule {
  id: string;
  count: number;
  prompts?: { ship?: string; id?: string; command?: string }[];
  reason?: string;
  every_repo?: boolean;
}
export interface AllowRules {
  rules: AllowRule[];
  skipped: AllowRule[];
}
const allowRulesPath = (fleet: string, rest = '') => `/fleets/${encodeURIComponent(fleet)}/allow-rules${rest}`;
export const getAllowRules = (fleet: string) => call<AllowRules>('GET', allowRulesPath(fleet));
export const removeAllowRule = (fleet: string, rule: AllowRule) => call<AllowRules>('POST', allowRulesPath(fleet, '/remove'), { rule });
export const unskipAllowRule = (fleet: string, rule: AllowRule) => call<AllowRules>('POST', allowRulesPath(fleet, '/skipped/remove'), { rule });

export type CapabilityStatus = 'passed' | 'failed' | 'not_checked';
export interface Capability {
  id: string;
  label: string;
  status: CapabilityStatus;
  checked_at: string | null;
  detail: string;
}
export interface Feature {
  id: string;
  label: string;
  capabilities: Capability[];
}
export interface HarnessCapabilities {
  harness: string;
  version: string;
  detected_at: string | null;
  features: Feature[];
}
/** The fleet's judge harness with its models, or `harness: null` and why there is none. */
export interface JudgeHarness {
  harness: string | null;
  reason?: string;
  lookout_model?: string;
  strong_model?: string;
  json_schema?: boolean;
  call_cap?: boolean;
}
export interface FleetCapabilities {
  key: string;
  name: string;
  harnesses: HarnessCapabilities[];
  judge?: JudgeHarness;
  /** True when a harness in the fleet has proven both restricted-ship capabilities, the guard and the fleet has an advisor. */
  restricted?: boolean;
}
export type CheckAnswer = HarnessCapabilities & { judge?: JudgeHarness };

export async function fetchCapabilities(): Promise<FleetCapabilities[]> {
  const res = await fetch(`${API}/harness-capabilities`);
  if (!res.ok) throw new Error(`harness-capabilities: ${res.status}`);
  const body = (await res.json()) as { fleets?: FleetCapabilities[] | null };
  return (body.fleets ?? []).map((f) => ({ ...f, harnesses: f.harnesses ?? [] }));
}

/** Runs the probes and waits for them, which can take minutes. */
export async function checkHarness(fleet: string, harness: string): Promise<CheckAnswer> {
  const res = await fetch(`${API}/harness-capabilities/check`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ fleet, harness }),
  }).catch(() => {
    throw new Error("Couldn't reach Admiral. Try again.");
  });
  const body = (await res.json().catch(() => ({}))) as CheckAnswer & { error?: string };
  if (!res.ok) throw new Error(body.error || `check: ${res.status}`);
  return body;
}

export interface UpdateStatus {
  current_version: string;
  latest_version: string | null;
  update_available: boolean;
  checked_at: string | null;
  enabled: boolean;
  install_method: 'homebrew' | 'script' | 'source';
  upgrade_commands: { label: string; command: string }[];
  restart_note: string;
  executable_path: string;
  release_url: string;
}

/** The update check's answer, or null when the server cannot give one. */
export async function fetchUpdateStatus(): Promise<UpdateStatus | null> {
  try {
    const res = await fetch(`${API}/update`, { cache: 'no-store' });
    return res.ok ? ((await res.json()) as UpdateStatus) : null;
  } catch {
    return null;
  }
}

export const shipsSocketUrl = () => `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws/v1`;

const PROXY_DOWN = new Set([502, 503, 504]);

export type ShipsProbe = 'ok' | 'signed-out' | 'down' | 'unreachable';

// A failed WebSocket handshake doesn't say why, so a reconnect asks over HTTP first: 401 is a changed token.
export async function probeShips(): Promise<ShipsProbe> {
  try {
    const res = await fetch(`${API}/ships`, { cache: 'no-store' });
    if (res.status === 401) return 'signed-out';
    // Behind tailscale serve a stopped Admiral answers 502 from the proxy, which is as unreachable as a refused connection.
    if (PROXY_DOWN.has(res.status)) return 'unreachable';
    return res.ok ? 'ok' : 'down';
  } catch {
    // A rejected fetch is a network failure: no answer at all, unlike a 401 or a 5xx.
    return 'unreachable';
  }
}

export interface Order {
  id: string;
  // A pull request's state, as a ship's prs[] entry gives it, without the url.
  pr?: Omit<MooredPR, 'url'> & { failing_check_count?: number };
  title: string;
  text?: string;
  repo?: string;
  url?: string;
  sub_issues?: number;
  /** An issue's GitHub parent; `id` has the form of an order's id. Never on a pull request. */
  parent?: { id: string; title: string; url: string; repo?: string };
  /** A pull request's GitHub stack; `position` sorts layers ascending from the bottom and `size` is GitHub's own count. Never on an issue. */
  stack?: { number: number; url: string; position: number; size: number; /** The base branch the whole stack conflicts with, set when the server's check found it. */ conflicts_with_base?: string };
  /** A pull request chained by branch to others in its view but not in a GitHub stack; `id` is the bottom PR's URL and `position` counts from 1 at the bottom. Never with `stack`. */
  chain?: { id: string; position: number; size: number; head: string; base: string; mine: boolean; /** The base branch the whole chain conflicts with, set when the server's check found it. */ conflicts_with_base?: string };
  labels?: string[];
  hot?: boolean;
  author?: string;
  /** RFC 3339. */
  opened_at?: string;
  /** The start of the body as one line. */
  excerpt?: string;
  /** RFC 3339: when the viewer was last asked to review this pull request. */
  review_requested_at?: string;
  reviewed_since_push?: boolean;
}
export interface OrderView {
  id: string;
  title: string;
  orders: Order[];
  error?: string;
  /** RFC 3339: the view's last successful answer; absent before the first. */
  refreshed_at?: string;
}
export type PageState = 'starting' | 'ready' | 'auth_required' | 'error';
export interface PageAction {
  id: string;
  label: string;
  confirm?: string;
  summary?: string;
  url?: string;
  /** A built-in action: Admiral does it, not the module. Absent on a module action. */
  builtin?: 'open' | 'launch' | 'message';
}
export interface PageChip {
  label: string;
  tone?: string;
}
export interface PageRow {
  id: string;
  title: string;
  subtitle?: string;
  chips?: PageChip[];
  reason?: string;
  action?: PageAction;
  actions?: PageAction[];
}
export interface PageGroup {
  id: string;
  title: string;
  rows: PageRow[];
}
export interface PageNotice {
  id: string;
  text: string;
  tone?: string;
  link?: { label: string; url: string } | null;
  action?: PageAction;
}
export interface ModulePageData {
  state: PageState;
  groups: PageGroup[];
  waiting: number;
  notices?: PageNotice[];
  auth?: { message: string; url?: string; code?: string };
  error?: string;
}
export interface RowLink {
  label: string;
  url: string;
}
export interface RowStep {
  id: string;
  title: string;
  chips?: PageChip[];
  action?: PageAction;
  actions?: PageAction[];
}
export type RowSection =
  | { kind: 'links'; title?: string; links?: RowLink[] }
  | { kind: 'steps'; title?: string; steps?: RowStep[] }
  | { kind: 'fields'; title?: string; fields?: { label: string; value: string }[] };
export interface RowDetail {
  sections: RowSection[];
}
export const fetchModuleRow = (key: string, id: string) => call<RowDetail>('GET', `${src(key)}/rows/${encodeURIComponent(id)}`);
export interface FormField {
  id: string;
  label: string;
  type: 'choice' | 'checklist' | 'duration';
  options?: { id: string; label: string }[];
  default?: string | string[];
  min?: string;
}
export interface ModuleForm {
  title?: string;
  fields: FormField[];
}
export interface RunAnswer {
  message: string;
  url?: string;
}
/** Above the server's 60 s run timeout, so its own timeout message reaches the page. */
const RUN_TIMEOUT_MS = 90_000;
/** What a module action hangs on: a row, or a notice. */
export interface ActionTarget {
  notice?: boolean;
  id: string;
}
const actionPath = (key: string, t: ActionTarget, action: string, leaf: string) =>
  `${src(key)}/${t.notice ? 'notices' : 'rows'}/${encodeURIComponent(t.id)}/actions/${encodeURIComponent(action)}/${leaf}`;
export const fetchModuleForm = (key: string, target: ActionTarget, action: string) => call<ModuleForm>('POST', actionPath(key, target, action, 'form'), {});
export const runModuleAction = (key: string, target: ActionTarget, action: string, values: Record<string, string | string[]>) =>
  call<RunAnswer>('POST', actionPath(key, target, action, 'run'), { values }, undefined, { timeoutMs: RUN_TIMEOUT_MS });
export interface LaunchTarget {
  url: string;
  fleet: string;
}
export interface MessagePreview {
  text: string;
  fleet: string;
  chat: string;
}
export const fetchLaunchTarget = (key: string, row: string, action: string) => call<LaunchTarget>('GET', actionPath(key, { id: row }, action, 'launch'));
export const fetchMessagePreview = (key: string, row: string, action: string) => call<MessagePreview>('GET', actionPath(key, { id: row }, action, 'message'));
/** Posts exactly the text the confirm showed; the server refuses it with 409 when the composed text has moved on. */
export const sendMessage = (key: string, row: string, action: string, text: string) => call<{ sent: true; chat: string }>('POST', actionPath(key, { id: row }, action, 'message'), { text });
export const fetchModulePage = (key: string) => call<ModulePageData>('GET', `${src(key)}/page`);

export interface Source {
  key: string;
  name: string;
  fleet: string;
  /** What the source can do, such as `refresh`. */
  capabilities?: string[];
  auth?: { message: string; url?: string; code?: string } | null;
  /** A module's page summary; absent for a source with no page. */
  page?: { state: PageState; waiting: number; version: number; rows?: string[] };
  /** True while the source pulls orders, asked for or on its timer; absent from an older server. */
  refreshing?: boolean;
  /** The login the source's account has on its service; only the GitHub source serves it, so a plugin's layers count as not yours. */
  viewer?: string;
  views: OrderView[];
  /** A GitHub source's visible views in tab order, For you included. */
  tabs?: { id: string; title: string }[];
}
export interface Resource {
  name: string;
  capacity: number;
  in_use: number;
}
export interface DirEntry {
  path: string;
  name: string;
  kind: string;
  ships_here: number;
  /** Each remote's identity, host/owner/name. */
  remotes: string[];
}
export interface DirsAnswer {
  entries: DirEntry[];
  selected: string | null;
  clone: { repo: string; path: string; exists?: boolean } | null;
}

/** A bridge link's reconnect check, over /api/v1: which ships are listed, or why not. A crew link lists its ship only while the boat is in its crew. */
export async function probeShipList(crew?: string): Promise<{ kind: 'ok'; ships: { session: string }[] } | { kind: 'signed-out' } | { kind: 'down' }> {
  try {
    const res = await fetch(`${API}/ships`, { cache: 'no-store' });
    if (res.status === 401) return { kind: 'signed-out' };
    if (!res.ok) return { kind: 'down' };
    const body = (await res.json()) as { ships?: { session: string; crew?: { name: string }[] | null }[] | null };
    const listed = (body.ships ?? []).filter((s) => crew === undefined || (s.crew ?? []).some((c) => c.name === crew));
    return { kind: 'ok', ships: listed.map((s) => ({ session: s.session })) };
  } catch {
    return { kind: 'down' };
  }
}
/** A chat terminal's reconnect check: the fleet's chat session is listed only while it is running, so a session that ended returns to the conversation. */
export async function probeChatSession(fleet: string): Promise<{ kind: 'ok'; ships: { session: string }[] } | { kind: 'signed-out' } | { kind: 'down' }> {
  try {
    const res = await fetch(`${API}/chat/${encodeURIComponent(fleet)}/session`, { cache: 'no-store' });
    if (res.status === 401) return { kind: 'signed-out' };
    // A fleet that is gone or whose chat is off has no session to rejoin.
    if (res.status === 404 || res.status === 409) return { kind: 'ok', ships: [] };
    if (!res.ok) return { kind: 'down' };
    const body = (await res.json()) as { running?: boolean };
    return { kind: 'ok', ships: body.running ? [{ session: `_chat-${fleet}` }] : [] };
  } catch {
    return { kind: 'down' };
  }
}
export interface Device {
  id: string;
  name: string;
  login: string;
  pairedAt: string;
  lastSeen: string | null;
  current?: boolean;
  thisMac?: boolean;
  notifications: boolean;
}
export interface PairingCode {
  code: string;
  url: string;
  expires: string;
}

export async function fetchDevices(): Promise<Result<Device[]>> {
  const r = await call<{ devices?: Device[] | null }>('GET', '/devices');
  return r.ok ? { ok: true, data: r.data.devices ?? [] } : r;
}

/** The browser's PushSubscription as JSON: what the Mac encrypts to. */
export interface PushSubscriptionJSON {
  endpoint: string;
  keys: { p256dh: string; auth: string };
}
export const fetchPushKey = () => call<{ key: string }>('GET', '/push/key');
/** Ok (204) when the server holds this endpoint for this device, a 404 failure when it does not. */
export const checkPushSubscription = (endpoint: string) => call<null>('GET', `/push/subscription?endpoint=${encodeURIComponent(endpoint)}`);
export const savePushSubscription = (sub: PushSubscriptionJSON) => call<null>('PUT', '/push/subscription', sub);
export const dropPushSubscription = (endpoint: string) => call<null>('DELETE', '/push/subscription', { endpoint });
export const mintPairingCode = () => call<PairingCode>('POST', '/pairing-codes');
export const renameDevice = (id: string, name: string) => call<unknown>('PATCH', `/devices/${encodeURIComponent(id)}`, { name });
export const revokeDevice = (id: string) => call<unknown>('DELETE', `/devices/${encodeURIComponent(id)}`);
export const revokeAllDevices = () => call<unknown>('DELETE', '/devices');

export interface RemoteCheck {
  id: string;
  ok: boolean;
  detail: string;
}
export interface RemoteView {
  tailscale: boolean;
  allowedLogins: string[];
  url: string;
  /** The command that stops serve, empty when none is known. */
  serveOff: string;
  /** True when --listen fixed Admiral's address, so the mode doesn't move it. */
  listenExplicit: boolean;
  checks: RemoteCheck[];
}
export const fetchRemote = () => call<RemoteView>('GET', '/remote');
export const editLogins = (change: { add: string } | { remove: string }) => call<RemoteView>('POST', '/remote/logins', change);
export const runTailscaleSetup = () => call<{ ok: boolean; checks: RemoteCheck[] }>('POST', '/remote/setup', { confirm: true }, undefined, { timeoutMs: SLOW_TIMEOUT_MS });
export const turnOffTailscale = () => call<RemoteView>('POST', '/remote/off', { confirm: true });
