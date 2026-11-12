// The harnesses Admiral reads a conversation from; the others show "No conversation for this harness".
export function hasConversationAdapter(harness: string): boolean {
  return harness === 'claude' || harness === 'codex' || harness === 'copilot';
}
