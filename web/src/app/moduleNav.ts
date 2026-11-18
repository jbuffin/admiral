import { go, loc, modulePath } from './router';

const FROM_LIST = 'moduleList';

/** Opens a row from the list, marking its history entry so closing can step back to the list. */
export function openRow(key: string, id: string) {
  go(modulePath(key, id), loc.value.search);
  history.replaceState({ [FROM_LIST]: key }, '');
}

/** Closes the open row: back to the list entry when the row was opened from it, else (a deep link) the list replaces the entry. */
export function closeRow(key: string) {
  if ((history.state as Record<string, unknown> | null)?.[FROM_LIST] === key) history.back();
  else go(modulePath(key), loc.value.search, true);
}
