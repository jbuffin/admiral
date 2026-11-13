import { describe, expect, it } from 'vitest';
import { CHAT_THRESHOLDS, INSTRUCTIONS_MAX, canSaveInstructions, instructionsCount, memoryNotRead, noteAge, readsMemory, switchHarness, chatErrorMessage, chatSaveBody, effortLabel, modelLabel, thresholdLabel, type ChatSettings } from './chatSettings';

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

describe('the model picker', () => {
  it('capitalises an alias and shows a harness model id as given', () => {
    expect(['sonnet', 'gpt-6.1-sol', 'gpt-6-luna'].map(modelLabel)).toEqual(['Sonnet', 'gpt-6.1-sol', 'gpt-6-luna']);
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

describe('the instructions count', () => {
  it('is 2000, the server cap', () => {
    expect(INSTRUCTIONS_MAX).toBe(2000);
  });
  it('counts what the server counts: code points of the trimmed text', () => {
    expect(instructionsCount('')).toBe(0);
    expect(instructionsCount('  be terse \n')).toBe(8);
    expect(instructionsCount('a😀b')).toBe(3);
    expect(instructionsCount('😀'.repeat(2000))).toBe(2000);
  });
});

describe('Save instructions', () => {
  it('is enabled for a change that fits', () => {
    expect(canSaveInstructions('', 'be terse')).toBe(true);
    expect(canSaveInstructions('be terse', '')).toBe(true);
    expect(canSaveInstructions('be terse', 'be terse and kind')).toBe(true);
  });
  it('is disabled when the text is unchanged, even by surrounding whitespace', () => {
    expect(canSaveInstructions('be terse', 'be terse!')).toBe(true);
    expect(canSaveInstructions('be terse', 'be terse')).toBe(false);
    expect(canSaveInstructions('be terse', '  be terse\n')).toBe(false);
    expect(canSaveInstructions('', '   ')).toBe(false);
  });
  it('is disabled over the cap and enabled at it, counting a surrogate pair once', () => {
    expect(canSaveInstructions('', '😀'.repeat(2000))).toBe(true);
    expect(canSaveInstructions('', '😀'.repeat(2001))).toBe(false);
    expect(canSaveInstructions('x', 'x'.repeat(2001))).toBe(false);
  });
});

describe('a note\'s age', () => {
  const now = Date.parse('2026-11-12T12:00:00Z');
  it('grows from minutes to days and shows none for a time that does not parse', () => {
    expect(noteAge('2026-11-12T11:59:40Z', now)).toBe('just now');
    expect(noteAge('2026-11-12T11:15:00Z', now)).toBe('45m ago');
    expect(noteAge('2026-11-12T07:00:00Z', now)).toBe('5h ago');
    expect(noteAge('2026-11-09T12:00:00Z', now)).toBe('3d ago');
    expect(noteAge('soon', now)).toBe('');
  });
});

describe('what a chat reads', () => {
  it('is the instructions, the notes and the scheduled refresh on Claude Code alone', () => {
    expect(readsMemory('claude')).toBe(true);
    expect(readsMemory('codex')).toBe(false);
    expect(readsMemory('copilot')).toBe(false);
  });
  it('says so under the instructions and notes of a chat that does not, and nothing on Claude Code', () => {
    expect(memoryNotRead('claude')).toBe('');
    expect(memoryNotRead('codex')).toBe("AdmiralBot on Codex doesn't read the instructions or the notes yet. They are kept here for when it does.");
    expect(memoryNotRead('copilot')).toContain('Copilot');
  });
});
