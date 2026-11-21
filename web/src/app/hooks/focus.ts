import type { RefObject } from 'preact';

export type FocusTarget = RefObject<HTMLElement | null> | (() => HTMLElement | null | undefined);

export function useFocusOnOpen(_open: boolean, _target: FocusTarget): void {}

export function useFocusOnClose(_open: boolean, _target: FocusTarget): void {}
