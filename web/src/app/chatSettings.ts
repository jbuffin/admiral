import type { ChatHarnessOption } from './api';
import { fieldMessage } from './fleetSettings';

export interface ChatSettings {
  harness: string;
  model: string;
  effort: string;
  context_threshold: number | 'never';
}

export const CHAT_THRESHOLDS: ChatSettings['context_threshold'][] = [100000, 150000, 300000, 'never'];

/** Choosing a harness keeps the model and effort it takes and resets the rest to its defaults (its lists lead with them). */
export function switchHarness(settings: ChatSettings, option: Pick<ChatHarnessOption, 'harness' | 'models' | 'efforts'>): ChatSettings {
  return {
    ...settings,
    harness: option.harness,
    model: option.models.includes(settings.model) ? settings.model : (option.models[0] ?? settings.model),
    effort: option.efforts.includes(settings.effort) ? settings.effort : (option.efforts[0] ?? ''),
  };
}

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

export const harnessLabel = capital;
export const modelLabel = capital;
export const effortLabel = (e: string) => (e === '' ? 'Default' : capital(e));
export const thresholdLabel = (t: ChatSettings['context_threshold']) => (t === 'never' ? 'Never' : `${t / 1000}K`);

/** What a save sends: only the keys that changed, or null when none did. */
export function chatSaveBody(shown: ChatSettings, edited: ChatSettings): { chat: Partial<ChatSettings> } | null {
  const chat: Partial<ChatSettings> = {};
  if (shown.harness !== edited.harness) chat.harness = edited.harness;
  if (shown.model !== edited.model) chat.model = edited.model;
  if (shown.effort !== edited.effort) chat.effort = edited.effort;
  if (shown.context_threshold !== edited.context_threshold) chat.context_threshold = edited.context_threshold;
  return Object.keys(chat).length > 0 ? { chat } : null;
}

/** The server's refusal without its `fleets.<key>.chat.<field>: ` path. */
export const chatErrorMessage = fieldMessage;
