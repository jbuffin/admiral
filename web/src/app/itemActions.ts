// What the row says once an action on a file item has gone through, as the old inbox did.

export const REMOVED_FOLD = 'Removed from the inbox';

export const submitFold = (data: { action?: string }, event: string): string => (data.action === 'gone' ? 'Review no longer pending on GitHub' : `Review submitted: ${event}`);

export const discardFold = (data: { action?: string; invalid_target?: boolean }): string =>
  data.invalid_target ? 'Review removed from the inbox; nothing changed on GitHub' : data.action === 'gone' ? 'Review no longer pending on GitHub' : 'Review discarded';

/** Only a refusal that says the item was already answered ends the actions; a busy 409 can be tried again. */
export const lockedBy = (status: number | undefined, message: string): boolean => status === 409 && /already answered/i.test(message);
