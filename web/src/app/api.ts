// The only place the shell builds /api/v1 URLs.
export interface Dock {
  harness: string;
}
export interface Fleet {
  key: string;
  name: string;
  color: string;
  ink: string;
  color_index?: number;
  docks: Dock[];
}

const API = '/api/v1';

export async function fetchFleets(): Promise<Fleet[]> {
  const res = await fetch(`${API}/config`);
  if (!res.ok) throw new Error(`config: ${res.status}`);
  const body = (await res.json()) as { fleets?: Fleet[] | null };
  return (body.fleets ?? []).map((f) => ({ ...f, docks: f.docks ?? [] }));
}

export const logoUrl = (harness: string) => `${API}/logos/${encodeURIComponent(harness)}`;

export type Result<T> = { ok: true; data: T } | { ok: false; error: string };

async function call<T>(method: string, path: string, body?: unknown): Promise<Result<T>> {
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
    if (!res.ok) return { ok: false, error: (data as { error?: string } | null)?.error ?? `${method} ${path}: ${res.status}` };
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
