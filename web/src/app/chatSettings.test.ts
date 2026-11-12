import { describe, expect, it } from 'vitest';
import { CHAT_THRESHOLDS, switchHarness, chatErrorMessage, chatSaveBody, effortLabel, thresholdLabel, type ChatSettings } from './chatSettings';

const shown: ChatSettings = { harness: 'claude', model: 'sonnet', effort: '', context_threshold: 150000 };

describe('the AdmiralBot choices', () => {
  it('offer the thresholds the server accepts', () => {
    expect(CHAT_THRESHOLDS).toEqual([100000, 150000, 300000, 'never']);
  });
  it('label the harness default as Default and the thresholds as 100K, 150K, 300K and Never', () => {
    expect(['', 'low', 'medium', 'high'].map(effortLabel)).toEqual(['Default', 'Low', 'Medium', 'High']);
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

describe('switching harness', () => {
  const codex = { harness: 'codex', models: ['gpt-6.1-sol', 'gpt-6-luna'], efforts: ['', 'low', 'medium', 'high'] };
  const copilot = { harness: 'copilot', models: ['gpt-5.6-sol', 'gpt-5.6-luna'], efforts: [''] };
  it("resets a model the harness doesn't take to its first, the default", () => {
    expect(switchHarness(shown, codex)).toEqual({ ...shown, harness: 'codex', model: 'gpt-6.1-sol' });
  });
  it('keeps a model and effort the harness takes', () => {
    expect(switchHarness({ ...shown, harness: 'codex', model: 'gpt-6-luna', effort: 'high' }, codex).model).toBe('gpt-6-luna');
    expect(switchHarness({ ...shown, harness: 'codex', model: 'gpt-6-luna', effort: 'high' }, codex).effort).toBe('high');
  });
  it('drops an effort the harness lacks', () => {
    expect(switchHarness({ ...shown, effort: 'high' }, copilot)).toMatchObject({ harness: 'copilot', model: 'gpt-5.6-sol', effort: '' });
  });
});
