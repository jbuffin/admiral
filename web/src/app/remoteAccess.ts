export const formatCode = (code: string): string => {
  const c = code.replace(/[^A-Za-z0-9]/g, '').toUpperCase();
  return `${c.slice(0, 4)}-${c.slice(4)}`;
};

export function expiryState(expires: string, nowMs: number): { expired: boolean; remaining: string } {
  const left = Math.max(0, Math.ceil((Date.parse(expires) - nowMs) / 1000));
  return { expired: left === 0, remaining: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}` };
}

export const thisDeviceMark = (d: { current?: boolean }): string | null => (d.current ? 'This device' : null);

export const SIGNED_OUT_LINE = 'This device was signed out. Pair it again with a new code.';
export const signedOutLine = (r: { ok: boolean; status?: number }): string | null => (!r.ok && r.status === 401 ? SIGNED_OUT_LINE : null);

type Row = { current?: boolean; thisMac?: boolean };

/** Whether the browser is on the Mac itself (it has no paired row, or its row is This Mac) or a paired device, which is remote. Unknown until the list answers; a failed list reads as local. */
export function routeOf(devices: Row[] | null, listFailed: boolean): { local: boolean; remote: boolean } {
  if (devices === null) return { local: listFailed, remote: false };
  const remote = devices.some((d) => d.current && !d.thisMac);
  return { local: !remote, remote };
}
