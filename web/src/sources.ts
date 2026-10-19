import { computed, signal } from '@preact/signals';
import { onMessage } from './connection';

export interface Order {
  id: string;
  title: string;
  text?: string;
  url?: string;
  hot?: boolean;
  repo?: string;
}

export interface Panel {
  id: string;
  title: string;
  orders: Order[];
  error?: string | null;
}

export interface Auth {
  message: string;
  url?: string;
  code?: string;
}

export type SourceState = 'starting' | 'ok' | 'auth_required' | 'error';

export interface Source {
  key: string;
  name: string;
  plugin: string;
  icon_url: string;
  capabilities: string[];
  has_schema: boolean;
  state: SourceState;
  read_only: boolean;
  error: string | null;
  auth: Auth | null;
  panels: Panel[];
  settings?: Record<string, unknown>;
}

export interface PluginInfo {
  name: string;
  description: string;
  icon_url: string;
  valid: boolean;
  error: string;
}

// null until the server's first `sources` message arrives; sourcesLoaded tells
// the two states apart.
export const sources = signal<Source[] | null>(null);
export const sourcesLoaded = computed(() => sources.value !== null);

onMessage('sources', (msg) => (sources.value = msg.sources));

// The filter text is not stored: a reload starts with every order visible.
export const filter = signal('');

// Whether the source declares the capability, and whether the control may be
// used now: a read-only source keeps its last orders but refuses commands.
export const declares = (s: Source, capability: string) => s.capabilities.includes(capability);
export const can = (s: Source, capability: string) => declares(s, capability) && !s.read_only;
export const isWebUrl = (u?: string) => !!u && /^https?:\/\//i.test(u);

export const matches = (o: Order, q: string) => !q || `${o.id} ${o.title}`.toLowerCase().includes(q);

// A panel as the sidebar shows it: its title, its orders after the filter, and
// the colour that tells its source apart.
export interface PanelView {
  key: string;
  source: Source;
  panel: Panel;
  title: string;
  orders: Order[];
  color: string;
}

const fleetColors = [0, 1, 2, 3, 4].map((i) => ['blue', 'orange', 'violet', 'teal', 'rose'][i]);
const known = ['jira', 'github', 'linear'];

// The source's colour: the token for a plugin the tokens name, else the fleet
// palette in source order.
export function sourceColor(list: Source[], s: Source): { bg: string; ink: string } {
  if (known.includes(s.plugin)) return { bg: `var(--source-${s.plugin})`, ink: '#FFFFFF' };
  const name = fleetColors[list.indexOf(s) % fleetColors.length];
  return { bg: `var(--fleet-sequence-${name}-color)`, ink: `var(--fleet-sequence-${name}-ink)` };
}

export const panelViews = computed<PanelView[]>(() => {
  const list = sources.value ?? [];
  const q = filter.value.trim().toLowerCase();
  const titles = new Map<string, number>();
  for (const s of list) for (const p of s.panels) titles.set(p.title, (titles.get(p.title) ?? 0) + 1);
  return list.flatMap((s) =>
    s.panels.map((panel) => ({
      key: `${s.key}/${panel.id}`,
      source: s,
      panel,
      title: s.panels.length > 1 || titles.get(panel.title)! > 1 ? `${s.name} · ${panel.title}` : panel.title,
      orders: panel.orders.filter((o) => matches(o, q)),
      color: sourceColor(list, s).bg,
    })),
  );
});

// A request to the server: the data on success, the server's message on refusal.
export async function api<T = unknown>(
  method: string,
  path: string,
  body?: unknown,
): Promise<{ ok: true; data: T } | { ok: false; error: string }> {
  try {
    const res = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const data = await res.json().catch(() => null);
    if (res.ok) return { ok: true, data };
    return { ok: false, error: data?.error ?? `The server answered ${res.status}` };
  } catch {
    return { ok: false, error: 'Could not reach Admiral' };
  }
}

const enc = encodeURIComponent;
export const sourcePath = (key: string, rest = '') => `/api/sources/${enc(key)}${rest}`;

// Per-browser memory of the collapses. localStorage can be missing or throw
// (private windows), so every use is guarded and the page works without it.
function read(key: string): boolean {
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    return false;
  }
}
function write(key: string, on: boolean): void {
  try {
    if (on) localStorage.setItem(key, '1');
    else localStorage.removeItem(key);
  } catch {
    // not remembered
  }
}

const panelKey = (k: string) => `admiral.collapsed.${k}`;
const collapsedPanels = signal<Record<string, boolean>>({});
export const panelCollapsed = (k: string) => collapsedPanels.value[k] ?? read(panelKey(k));
export function setPanelCollapsed(k: string, on: boolean): void {
  collapsedPanels.value = { ...collapsedPanels.value, [k]: on };
  write(panelKey(k), on);
}

export const sidebarCollapsed = signal(read('admiral.sidebar.collapsed'));
export function setSidebarCollapsed(on: boolean): void {
  sidebarCollapsed.value = on;
  write('admiral.sidebar.collapsed', on);
}
