import { signal } from '@preact/signals';

// The phone's tab, in a module of its own so chat.ts can read it without importing the shell.
export type Tab = 'inbox' | 'chat' | 'ships' | 'orders';
export const phoneTab = signal<Tab>('inbox');
