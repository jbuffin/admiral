import { describe, expect, it } from 'vitest';
import { joinSequence, type JoinMsg } from './bridgeReplay';

const join = (over: Partial<JoinMsg>): JoinMsg => ({
  type: 'join',
  screen: 'one\ntwo',
  scrollback: '',
  cursor: { x: 3, y: 1, visible: true },
  modes: { alternate: false, application_cursor: false, bracketed_paste: false, mouse: false },
  size: { cols: 80, rows: 2 },
  ship: { status: 'underway', exit_code: null, exit_signal: null },
  ...over,
});

describe('joinSequence', () => {
  it('draws history above the screen, then puts the cursor back', () => {
    const s = joinSequence(join({ scrollback: 'old' }));
    expect(s.indexOf('old')).toBeLessThan(s.indexOf('one\r\ntwo'));
    expect(s).toContain('\x1b[2;4H');
    expect(s).toContain('\x1b[?25h');
  });

  it('restores the alternate screen and the input modes, without history', () => {
    const s = joinSequence(
      join({
        scrollback: 'old',
        modes: { alternate: true, application_cursor: true, bracketed_paste: true, mouse: false },
        cursor: { x: 0, y: 0, visible: false },
      }),
    );
    expect(s).not.toContain('old');
    expect(s.indexOf('\x1b[?1049h')).toBeLessThan(s.indexOf('one'));
    expect(s).toContain('\x1b[?1h');
    expect(s).toContain('\x1b[?2004h');
    expect(s).toContain('\x1b[?25l');
  });
});
