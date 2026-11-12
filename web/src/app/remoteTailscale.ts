export type LoginCheck = { ok: true; login: string } | { ok: false; error: string };

/** The login a person typed, trimmed, or why it can't be added. */
export function checkLogin(input: string, existing: string[]): LoginCheck {
  const login = input.trim();
  if (!login) return { ok: false, error: 'Type a login to allow.' };
  if (existing.includes(login)) return { ok: false, error: `${login} is already allowed.` };
  return { ok: true, login };
}

/** True when the login being removed is the one this device came in on, so removing it signs this device out. */
export const removingOwnLogin = (login: string, devices: { id?: string; login: string; current?: boolean }[]): boolean =>
  login !== '' && devices.some((d) => d.current && d.login === login);

/** 'copied' when the clipboard took the text; 'select' when it refused or isn't there and the person must copy by hand. */
export async function copyOutcome(text: string, clipboard: { writeText(t: string): Promise<void> } | undefined): Promise<'copied' | 'select'> {
  if (!clipboard) return 'select';
  try {
    await clipboard.writeText(text);
    return 'copied';
  } catch {
    return 'select';
  }
}

/** Where a move message sends this page: the address it carries, unless it is empty, not http(s) or the page's own. */
export function moveTarget(msg: { type: string; url?: string }, here: string): string | null {
  if (!msg.url) return null;
  try {
    const to = new URL(msg.url);
    if (to.protocol !== 'http:' && to.protocol !== 'https:') return null;
    return to.href === new URL(here).href ? null : to.href;
  } catch {
    return null;
  }
}
