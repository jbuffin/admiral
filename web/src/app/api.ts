// The only place the shell builds /api/v1 URLs.
export interface Dock {
  harness: string;
  executable: string;
  executable_found: boolean;
  warnings: string[];
}
export interface Fleet {
  key: string;
  name: string;
  color: string;
  ink: string;
  color_index?: number;
  search_roots: string[];
  owners: string[];
  clone_root: string;
  can_launch: boolean;
  docks: Dock[];
}

export type ShipStatus = 'needs_you' | 'underway' | 'anchored' | 'sailing_off';
export interface Ship {
  session: string;
  name: string;
  fleet: string;
  harness: string;
  status: ShipStatus;
  order_id: string;
  title: string;
  tail: string;
  fleet_color: string;
  launched: string;
  crew_live: number;
}

const API = '/api/v1';

const fleetsOf = (body: { fleets?: Fleet[] | null }): Fleet[] =>
  (body.fleets ?? []).map((f) => ({
    ...f,
    search_roots: f.search_roots ?? [],
    owners: f.owners ?? [],
    docks: (f.docks ?? []).map((d) => ({ ...d, warnings: d.warnings ?? [] })),
  }));

export async function fetchFleets(): Promise<Fleet[]> {
  const res = await fetch(`${API}/config`);
  if (!res.ok) throw new Error(`config: ${res.status}`);
  return fleetsOf(await res.json());
}

export const logoUrl = (harness: string) => `${API}/logos/${encodeURIComponent(harness)}`;

export type Result<T> = { ok: true; data: T } | { ok: false; error: string; status?: number };

export async function call<T>(method: string, path: string, body?: unknown): Promise<Result<T>> {
  try {
    const res = await fetch(API + path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    let data: unknown = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      /* not JSON: the status speaks */
    }
    if (!res.ok) return { ok: false, status: res.status, error: (data as { error?: string } | null)?.error ?? `${method} ${path}: ${res.status}` };
    return { ok: true, data: data as T };
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'The request failed.' };
  }
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
export async function saveFleet(key: string, body: Partial<Pick<Fleet, 'search_roots' | 'owners' | 'clone_root' | 'color'>>): Promise<Result<Fleet[]>> {
  const r = await call<{ fleets?: Fleet[] | null }>('PUT', `/fleets/${encodeURIComponent(key)}`, body);
  return r.ok ? { ok: true, data: fleetsOf(r.data) } : r;
}
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

export type ShipsProbe = 'ok' | 'signed-out' | 'down';

// A failed WebSocket handshake doesn't say why, so a reconnect asks over HTTP first: 401 is a changed token.
export async function probeShips(): Promise<ShipsProbe> {
  try {
    const res = await fetch(`${API}/ships`, { cache: 'no-store' });
    if (res.status === 401) return 'signed-out';
    return res.ok ? 'ok' : 'down';
  } catch {
    return 'down';
  }
}

export interface Order {
  id: string;
  title: string;
  text?: string;
  repo?: string;
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
