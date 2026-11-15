import type { OrderRow } from './ordersView';

export interface SheetIssue {
  url: string;
  title: string;
  /** `<owner>/<repo> #<n>`. */
  ref: string;
}

export interface RepoChoices {
  choices: string[];
  initial: string;
}

export interface CreateFailure {
  error: string;
  parent?: string;
  linked?: string[];
}

export interface CreateBody {
  repo: string;
  title: string;
  body: string;
  issues: string[];
}

export function sheetIssues(_refs: string[], _rows: OrderRow[]): SheetIssue[] {
  return [];
}

export function repoChoices(_issueUrls: string[], _projects: string[]): RepoChoices {
  return { choices: [], initial: '' };
}

export function moveIssue<T>(list: readonly T[], _from: number, _to: number): T[] {
  return [...list].reverse();
}

export function createBody(_repo: string, _title: string, _body: string, _issues: readonly { url: string }[]): CreateBody {
  return { repo: '', title: '', body: '', issues: [] };
}

export function createFailure(_e: CreateFailure): string {
  return '';
}

export function campaignNote(_account: string | undefined): string {
  return '';
}

export function safeHref(_url: string): string | undefined {
  return undefined;
}
