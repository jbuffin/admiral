import type { HarnessName } from './config';

// `field` is the solid color drawn behind an official logo, set by the vendors' usage rules.
// The lettered placeholders carry their own field (the harness.*.flag tokens).
export const harnesses: Record<HarnessName, { label: string; bot: string; field: string }> = {
  claude: { label: 'Claude', bot: '#7FC8FF', field: '#F5F0E6' },
  copilot: { label: 'Copilot', bot: '#FFC23D', field: '#24292F' },
  codex: { label: 'Codex', bot: '#9BE08A', field: '#FFFFFF' },
};

export const logoId = (h: HarnessName) => `logo-${h}`;
