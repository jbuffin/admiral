// What a joining browser is sent (internal/bridge) and the escape sequences that put xterm.js in
// the same state: scrollback, screen, cursor and modes, so typing works at once.

export interface JoinMsg {
  type: 'join';
  screen: string;
  scrollback: string;
  cursor: { x: number; y: number; visible: boolean };
  modes: { alternate: boolean; application_cursor: boolean; bracketed_paste: boolean; mouse: boolean };
  size: { cols: number; rows: number };
  ship: { status: string; exit_code: number | null; exit_signal: string | null };
}

const ESC = '\x1b';
const lines = (s: string) => s.split('\n').join('\r\n');

// The terminal is reset before this is written. History goes in first and is pushed up out of the
// visible rows, then the screen is drawn from the top (in the alternate buffer when the pane is in
// it, which has no history), then the cursor and the modes.
export function joinSequence(j: JoinMsg): string {
  let out = '';
  if (j.scrollback && !j.modes.alternate) out += `${ESC}[0m${lines(j.scrollback)}${'\r\n'.repeat(j.size.rows)}`;
  if (j.modes.alternate) out += `${ESC}[?1049h`;
  out += `${ESC}[0m${ESC}[H${ESC}[2J${lines(j.screen)}${ESC}[0m`;
  out += `${ESC}[${j.cursor.y + 1};${j.cursor.x + 1}H`;
  if (j.modes.application_cursor) out += `${ESC}[?1h`;
  if (j.modes.bracketed_paste) out += `${ESC}[?2004h`;
  if (j.modes.mouse) out += `${ESC}[?1003h${ESC}[?1006h`;
  out += j.cursor.visible ? `${ESC}[?25h` : `${ESC}[?25l`;
  return out;
}
