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
