// The only place the shell builds /api/v1 URLs.
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
}

export type ShipStatus = 'needs_you' | 'underway' | 'moored' | 'anchored' | 'sailing_off';
export interface MooredPR {
  url: string;
  state: string;
  review: string;
  checks: string;
  mergeable: string;
  conflicts: boolean;
  ready?: boolean;
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
  // When a needs-you ship started waiting; omitted otherwise.
  stop_since?: string;
  crew_live: number;
  // Set on a moored ship; `stale` is omitted while false.
  moored_at?: string;
  polled_at?: string;
  stale?: boolean;
  no_resume?: boolean;
  waiting?: string;
  prs?: MooredPR[];
  stack_ready?: boolean;
  ready_up_to?: string;
  merge_method?: 'merge' | 'squash' | 'rebase';
  url?: string;
  // Every issue a slice ship holds, `url` first; a single-issue ship omits it.
  issues?: string[];
  source?: string;
  dir: string;
  repo: string;
  reason?: string;
  exit_code: number | null;
  exit_signal: string | null;
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

export interface ChangedFile {
  path: string;
  status: string;
  added: number;
  removed: number;
}
export interface Changes {
  state: 'ok' | 'none';
  files: ChangedFile[];
  diff: string;
  truncated: boolean;
}

const fleetsOf = (body: { fleets?: Fleet[] | null }): Fleet[] =>
  (body.fleets ?? []).map((f) => ({
    ...f,
    search_roots: f.search_roots ?? [],
    owners: f.owners ?? [],
    wake_reviewers: f.wake_reviewers ?? [],
    docks: (f.docks ?? []).map((d) => ({ ...d, warnings: d.warnings ?? [] })),
    chat: { ...f.chat, harnesses: f.chat?.harnesses ?? [], harness_options: f.chat?.harness_options ?? [], models: f.chat?.models ?? [], efforts: f.chat?.efforts ?? [] },
    notify: f.notify ?? true,
  }));

export async function fetchFleets(): Promise<Fleet[]> {
  const res = await fetch(`${API}/config`);
  if (!res.ok) throw new Error(`config: ${res.status}`);
  return fleetsOf(await res.json());
}

export const logoUrl = (harness: string) => `${API}/logos/${encodeURIComponent(harness)}`;

export type Result<T> = { ok: true; data: T } | { ok: false; error: string; status?: number; field?: string; note?: string; kind?: string; reason?: string; aborted?: boolean };

export async function call<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>, signal?: AbortSignal): Promise<Result<T>> {
  try {
    const res = await fetch(path.startsWith(`${API}/`) ? path : API + path, {
      method,
      headers: body === undefined && headers === undefined ? undefined : { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
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
      const body = data as { error?: string; field?: string; note?: unknown; kind?: unknown; reason?: unknown } | null;
      return {
        ok: false,
        status: res.status,
        error: body?.error ?? `${method} ${path}: ${res.status}`,
        ...(body?.field && { field: body.field }),
        ...(typeof body?.note === 'string' && { note: body.note }),
        ...(typeof body?.kind === 'string' && { kind: body.kind }),
        ...(typeof body?.reason === 'string' && { reason: body.reason }),
      };
    }
    return { ok: true, data: data as T };
  } catch (e) {
    if (e instanceof Error && e.name === 'AbortError') return { ok: false, error: 'The request was stopped.', aborted: true };
    return { ok: false, error: e instanceof Error ? e.message : 'The request failed.' };
  }
}

/** Merges a moored ship's single PR. The token is the one the server put in this page; it changes when Admiral restarts. */
export function mergeShip(session: string): Promise<Result<unknown>> {
  const token = document.querySelector<HTMLMetaElement>('meta[name="admiral-merge-token"]')?.content ?? '';
  return call('POST', `/ships/${encodeURIComponent(session)}/merge`, undefined, { 'X-Admiral-Merge-Token': token });
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
export const fetchPlugins = () => call<PluginInfo[]>('GET', '/plugins');
export const fetchSchema = (key: string) => call<unknown>('GET', `${src(key)}/schema`);
export const fetchSettings = (key: string) => call<{ name: string; settings: Record<string, unknown> }>('GET', `${src(key)}/settings`);
export const saveSource = (key: string, body: { name?: string; fleet?: string; settings?: Record<string, unknown> }) => call<Source>('PUT', src(key), body);
export const restartSource = (key: string) => call<unknown>('POST', `${src(key)}/restart`);
export const removeSource = (key: string) => call<unknown>('DELETE', src(key));
export const addSource = (plugin: string, fleet: string) => call<Source>('POST', '/sources', { plugin, fleet });

/** Saves the fleet fields in the body; the server answers the new config view. */
export async function saveFleet(key: string, body: Partial<Pick<Fleet, 'search_roots' | 'owners' | 'wake_reviewers' | 'clone_root' | 'color' | 'notify'>> | { chat: Partial<Omit<FleetChat, 'on' | 'off_reason' | 'harnesses' | 'harness_options' | 'models' | 'efforts'>> }): Promise<Result<Fleet[]>> {
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
  trigger: { kind: 'ship_sailed' | 'ship_anchored' | 'pr_merged' | 'pr_closed' | 'issue_closed' | 'time' | 'none'; subject?: string; launched?: string };
  action: 'launch' | 'remind';
  launch?: unknown;
  ts: string;
  via?: string;
  waiting?: boolean;
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
export const promoteNote =(fleet: string, id: string) => call<Instructions>('POST', chatPath(fleet, `notes/${encodeURIComponent(id)}/promote`));
export const startSetup = () => call<unknown>('POST', '/setup');

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
  pr?: Omit<MooredPR, 'url'>;
  title: string;
  text?: string;
  repo?: string;
  url?: string;
  sub_issues?: number;
}
export interface OrderView {
  id: string;
  title: string;
  orders: Order[];
}
export interface Source {
  key: string;
  name: string;
  fleet: string;
  views: OrderView[];
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
export const runTailscaleSetup = () => call<{ ok: boolean; checks: RemoteCheck[] }>('POST', '/remote/setup', { confirm: true });
export const turnOffTailscale = () => call<RemoteView>('POST', '/remote/off', { confirm: true });
