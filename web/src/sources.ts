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

export interface PanelAutopilot {
  enabled: boolean;
  paused: boolean;
  max_ships: number;
}

export interface Panel {
  id: string;
  title: string;
  orders: Order[];
  error?: string | null;
  autopilot?: PanelAutopilot | null;
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
// Whether some panel runs autopilot, paused or not: the header then shows the ship cap.
export const anyAutopilot = computed(() => !!sources.value?.some((s) => s.panels.some((p) => p.autopilot?.enabled)));

onMessage('sources', (msg) => (sources.value = msg.sources));

// The filter text is not stored: a reload starts with every order visible.
export const filter = signal('');

// Whether the source declares the capability, and whether the control may be
// used now: a read-only source keeps its last orders but refuses commands.
export const declares = (s: Source, capability: string) => s.capabilities.includes(capability);
export const can = (s: Source, capability: string) => declares(s, capability) && !s.read_only;
export const isWebUrl = (u?: string) => !!u && /^https?:\/\//i.test(u);

// A web address reduced to what tells two pages apart: no scheme, www.,
// query, fragment or trailing slash.
const bareUrl = (u: string) =>
  u.toLowerCase().replace(/^https?:\/\/(www\.)?/, '').replace(/[?#].*$/, '').replace(/[.,;:!]+$/, '').replace(/\/+$/, '');

// Every web address an order carries: its link and any in its text.
const urlsOf = (o: Order) =>
  [o.url, ...(`${o.title} ${o.text ?? ''}`.match(/https?:\/\/[^\s<>"')\]]+/gi) ?? [])].filter(isWebUrl).map((u) => bareUrl(u!));

// One address names the other's page or a page under it, segment by segment.
const sameOrUnder = (a: string, b: string) => a === b || a.startsWith(`${b}/`) || b.startsWith(`${a}/`);

// A pasted web address matches an order that carries it, a page under it, or
// a page it is under; anything else matches the ID or title.
export const matches = (o: Order, q: string) => {
  if (!q) return true;
  if (isWebUrl(q)) {
    const want = bareUrl(q);
    return urlsOf(o).some((u) => sameOrUnder(u, want));
  }
  return `${o.id} ${o.title}`.toLowerCase().includes(q);
};

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
  return list.flatMap((s) =>
    s.panels.map((panel) => ({
      key: `${s.key}/${panel.id}`,
      source: s,
      panel,
      title: panel.title,
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
function readRaw(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function writeRaw(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // not remembered
  }
}
const read = (key: string) => readRaw(key) === '1';
const write = (key: string, on: boolean) => writeRaw(key, on ? '1' : null);

const panelKey = (k: string) => `admiral.collapsed.${k}`;
const collapsedPanels = signal<Record<string, boolean>>({});
export const panelCollapsed = (k: string) => collapsedPanels.value[k] ?? read(panelKey(k));
export function setPanelCollapsed(k: string, on: boolean): void {
  collapsedPanels.value = { ...collapsedPanels.value, [k]: on };
  write(panelKey(k), on);
}

// Too narrow for the panel beside the sea (styles.css), the panel is a drawer over
// the sea. It starts closed, so it does not hide the ships until it is opened, and
// it has its own memory: '0' is closed and '1' is open.
const narrowQuery = typeof matchMedia === 'function' ? matchMedia('(max-width: 1000px)') : null;
const narrow = signal(narrowQuery?.matches ?? false);
narrowQuery?.addEventListener('change', (e) => (narrow.value = e.matches));
const drawerOpen = signal(readRaw('admiral.sidebar.drawer') === '1');

const rememberedCollapsed = signal(read('admiral.sidebar.collapsed'));
export const sidebarCollapsed = computed(() => (narrow.value ? !drawerOpen.value : rememberedCollapsed.value));
export function setSidebarCollapsed(on: boolean): void {
  if (narrow.value) {
    drawerOpen.value = !on;
    writeRaw('admiral.sidebar.drawer', on ? '0' : '1');
    return;
  }
  rememberedCollapsed.value = on;
  write('admiral.sidebar.collapsed', on);
}
