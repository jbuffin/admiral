import type { HarnessName } from './config';

// Dock labels and captain colors are the prototype's (index.html:625, 964).
export const harnesses: Record<HarnessName, { label: string; bot: string }> = {
  claude: { label: 'Claude', bot: '#7FC8FF' },
  copilot: { label: 'Copilot', bot: '#FFC23D' },
  codex: { label: 'Codex', bot: '#9BE08A' },
};

export const logoId = (h: HarnessName) => `logo-${h}`;
