import { describe, expect, it } from 'vitest';
import { CHAT_EFFORTS, CHAT_MODELS, CHAT_THRESHOLDS, chatErrorMessage, chatSaveBody, effortLabel, thresholdLabel, type ChatSettings } from './chatSettings';

const shown: ChatSettings = { harness: 'claude', model: 'sonnet', effort: '', context_threshold: 150000 };

describe('the AdmiralBot choices', () => {
  it('offer the models, efforts and thresholds the server accepts', () => {
    expect(CHAT_MODELS).toEqual(['sonnet', 'opus', 'haiku']);
    expect(CHAT_EFFORTS).toEqual(['', 'low', 'medium', 'high']);
    expect(CHAT_THRESHOLDS).toEqual([100000, 150000, 300000, 'never']);
  });
  it('label the harness default as Default and the thresholds as 100K, 150K, 300K and Never', () => {
    expect(CHAT_EFFORTS.map(effortLabel)).toEqual(['Default', 'Low', 'Medium', 'High']);
    expect(CHAT_THRESHOLDS.map(thresholdLabel)).toEqual(['100K', '150K', '300K', 'Never']);
  });
});

describe('a save', () => {
  it('sends only what changed', () => {
    expect(chatSaveBody(shown, { ...shown, model: 'opus' })).toEqual({ chat: { model: 'opus' } });
    expect(chatSaveBody(shown, { ...shown, effort: 'high', context_threshold: 'never' })).toEqual({ chat: { effort: 'high', context_threshold: 'never' } });
  });
  it('sends an empty effort to go back to the harness default', () => {
    expect(chatSaveBody({ ...shown, effort: 'high' }, shown)).toEqual({ chat: { effort: '' } });
  });
  it('sends nothing when nothing changed', () => {
    expect(chatSaveBody(shown, { ...shown })).toBeNull();
  });
});

describe('a refusal', () => {
  it("shows the server's message without its path", () => {
    expect(chatErrorMessage('fleets.harbor.chat.model: "gpt" is not one of sonnet, opus, haiku', 'harbor')).toBe('"gpt" is not one of sonnet, opus, haiku');
  });
  it('shows any other message as it came', () => {
    expect(chatErrorMessage("Couldn't reach Admiral. Try again.", 'harbor')).toBe("Couldn't reach Admiral. Try again.");
  });
});
