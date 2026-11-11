import { describe, expect, it } from 'vitest';
import { hasConversationAdapter } from './conversationGate';

// The Conversation tab, ShipView's retryNone and its hasConversation share this gate, so no harness is named twice.
describe('hasConversationAdapter', () => {
  it('is true for the harnesses Admiral reads a conversation from', () => {
    expect(hasConversationAdapter('claude')).toBe(true);
    expect(hasConversationAdapter('codex')).toBe(true);
    expect(hasConversationAdapter('copilot')).toBe(true);
  });

  it('is false for a harness with no adapter, so its tab says "No conversation for this harness"', () => {
    expect(hasConversationAdapter('gemini')).toBe(false);
    expect(hasConversationAdapter('')).toBe(false);
    expect(hasConversationAdapter('unknown')).toBe(false);
  });
});
