import type { HarnessName } from './config';

// Dock labels and captain colors are the prototype's (index.html:625, 964).
//
// `field` is the solid color drawn behind an official logo, from the usage rules
// in the spec's Logos section: Claude on ivory, Copilot on dark, Codex on white.
// The lettered placeholders carry their own field (the harness.*.flag tokens).
export const harnesses: Record<HarnessName, { label: string; bot: string; field: string }> = {
  claude: { label: 'Claude', bot: '#7FC8FF', field: '#F5F0E6' },
  copilot: { label: 'Copilot', bot: '#FFC23D', field: '#24292F' },
  codex: { label: 'Codex', bot: '#9BE08A', field: '#FFFFFF' },
};

export const logoId = (h: HarnessName) => `logo-${h}`;
