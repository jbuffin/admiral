// What the bridge and the setup terminal send for keys a browser can't type itself.

// Shift+Enter in the terminal and the Newline key: ESC CR, Alt+Enter to a TUI, which the harnesses
// take as a new line in their input. A bare CR would submit.
export const NEWLINE = '\x1b\r';

// What the key buttons send. The labels and the accessible names are the prototype's, plus Newline.
export const KEYS = [
  ['Enter', '⏎ Enter', '\r'],
  ['Newline', '⇧⏎ Newline', NEWLINE],
  ['Esc', 'Esc', '\x1b'],
  ['Tab', 'Tab', '\t'],
  ['Up', '↑', '\x1b[A'],
  ['Down', '↓', '\x1b[B'],
  ['Ctrl-C', '^C', '\x03'],
] as const;

// A touch screen's keyboard has no Shift+Return, Esc or arrows: the key buttons are for it alone,
// and the order input's Return starts a new line there.
export const touchKeyboard = () => matchMedia('(pointer: coarse)').matches;
