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

export const logoUrl = (harness: string) => `${API}/logos/${harness}`;
