import type { Ship } from './api';
import type { InboxItem } from './inbox';
import { peekPath, shipPath } from './router';
import type { ToastAlert } from './toasts';

/** The alert message on /ws/v1: what the server decided is worth the engineer's eye, and where a tap lands. */
export interface Alert extends ToastAlert {
  type?: string;
  fleet?: string;
  fleet_name?: string;
  target?: string;
  session: string;
  item?: string;
  crew?: string;
  merge?: boolean;
}

const INBOX = '/app/inbox';

/** Where a tap on the alert's toast goes: its Peek while the entry is still there, otherwise the Inbox alone. */
export function alertDestination(alert: Pick<Alert, 'target' | 'session' | 'item' | 'crew' | 'merge'>, now: { ships: Ship[]; items: InboxItem[] }): string {
  const { session } = alert;
  const ship = now.ships.find((s) => s.session === session);
  if (alert.target === 'ship') return ship?.status === 'anchored' ? shipPath(session) : INBOX;
  if (alert.item !== undefined) return now.items.some((i) => i.session === session && i.id === alert.item) ? peekPath({ session, item: alert.item }) : INBOX;
  if (alert.crew !== undefined) return ship?.crew?.some((b) => b.name === alert.crew && b.status === 'needs_you') ? peekPath({ session, crew: alert.crew }) : INBOX;
  if (alert.merge) return ship?.status === 'moored' && ship.stack_ready ? peekPath({ session, merge: true }) : INBOX;
  return ship?.status === 'needs_you' ? peekPath({ session }) : INBOX;
}

/** Handles each message on the socket: an alert chimes (unless muted) and toasts, anything else is left alone. */
export function createAlertHandler({ toasts, chime, muted }: { toasts: { push: (a: Alert) => unknown }; chime: () => void; muted: () => boolean }) {
  return (msg: { type: string; [key: string]: unknown }) => {
    if (msg.type !== 'alert') return;
    toasts.push(msg as unknown as Alert);
    if (!muted()) chime();
  };
}
