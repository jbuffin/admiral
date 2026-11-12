/** A tapped push resolves against the lists only when the link is open and both a ships and an inbox snapshot have arrived on this connection. */
export function canResolve({ link, shipsFresh, inboxFresh }: { link: string; shipsFresh: boolean; inboxFresh: boolean }): boolean {
  return link === 'open' && shipsFresh && inboxFresh;
}
