// Markdown for transcript text, which is untrusted: raw HTML is never parsed, and only web, mail, relative and fragment links are kept.

import hljs from 'highlight.js/lib/core';
import bash from 'highlight.js/lib/languages/bash';
import css from 'highlight.js/lib/languages/css';
import diff from 'highlight.js/lib/languages/diff';
import go from 'highlight.js/lib/languages/go';
import javascript from 'highlight.js/lib/languages/javascript';
import json from 'highlight.js/lib/languages/json';
import markdown from 'highlight.js/lib/languages/markdown';
import python from 'highlight.js/lib/languages/python';
import sql from 'highlight.js/lib/languages/sql';
import swift from 'highlight.js/lib/languages/swift';
import typescript from 'highlight.js/lib/languages/typescript';
import xml from 'highlight.js/lib/languages/xml';
import yaml from 'highlight.js/lib/languages/yaml';
import MarkdownIt from 'markdown-it';
import type { Token } from 'markdown-it';
import type { Ship } from './api';

for (const [name, lang] of Object.entries({ bash, css, diff, go, javascript, json, markdown, python, sql, swift, typescript, xml, yaml })) hljs.registerLanguage(name, lang);
hljs.registerAliases(['sh', 'shell'], { languageName: 'bash' });

// Only these schemes become links; a target with no scheme is relative.
// A protocol-relative //host target is another site, not a relative link.
const safeUrl = (url: string) => !/^\/\//.test(url.trim()) && (!/^[a-z][a-z0-9+.-]*:/i.test(url.trim()) || /^(https?|mailto):/i.test(url.trim()));

function highlight(code: string, lang: string): string {
  if (!hljs.getLanguage(lang)) return '';
  return `<pre><code class="hljs">${hljs.highlight(code, { language: lang, ignoreIllegals: true }).value}</code></pre>`;
}

// A list item whose text starts with [ ] or [x] gets a read-only checkbox; code never reaches it, being a different token.
function taskLists(state: { tokens: Token[]; Token: new (type: string, tag: string, nesting: 0 | 1 | -1) => Token }) {
  const { tokens } = state;
  for (let i = 2; i < tokens.length; i++) {
    const inline = tokens[i];
    if (inline.type !== 'inline' || tokens[i - 2].type !== 'list_item_open' || tokens[i - 1].type !== 'paragraph_open') continue;
    const first = inline.children?.[0];
    const m = first?.type === 'text' ? /^\[([ xX])\]\s+/.exec(first.content) : null;
    if (!first || !m) continue;
    first.content = first.content.slice(m[0].length);
    const box = new state.Token('html_inline', '', 0);
    box.content = `<input type="checkbox" disabled${m[1] === ' ' ? '' : ' checked'}> `;
    inline.children!.unshift(box);
  }
}

/** The ships whose names an AdmiralBot reply links: the chat's fleet, and the message's time for what had been launched by then. */
export interface ShipLinks {
  ships: Ship[];
  fleet: string;
  at: string;
  search?: string;
}

interface Linker {
  re: RegExp;
  hrefs: Map<string, string>;
}

// router.ts reads `location` on import, which the node tests lack, so the ship path is spelled here too.
const shipPath = (session: string) => `/app/ships/${encodeURIComponent(session)}`;

// A name matches as a whole word sequence, the longest first, so "Saltpaw" never links inside "Saltpaw II".
function linkerOf(links: ShipLinks): Linker | null {
  const at = Date.parse(links.at);
  const hrefs = new Map<string, string>();
  for (const s of links.ships) {
    const first = Date.parse(s.first_launched ?? s.launched);
    if (s.via !== 'admiralbot' || s.fleet !== links.fleet || !s.name || !(first <= at) || hrefs.has(s.name)) continue;
    hrefs.set(s.name, shipPath(s.session) + (links.search ?? ''));
  }
  if (hrefs.size === 0) return null;
  const names = [...hrefs.keys()].sort((a, b) => b.length - a.length).map((n) => n.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
  return { re: new RegExp(`(?<![\\p{L}\\p{N}_])(?:${names.join('|')})(?![\\p{L}\\p{N}_])`, 'gu'), hrefs };
}

function shipLinks(state: { tokens: Token[]; env: unknown; Token: new (type: string, tag: string, nesting: 0 | 1 | -1) => Token }) {
  const linker = (state.env as { linker?: Linker | null }).linker;
  if (!linker) return;
  for (const block of state.tokens) {
    if (block.type !== 'inline' || !block.children) continue;
    const out: Token[] = [];
    let inLink = 0;
    for (const t of block.children) {
      if (t.type === 'link_open') inLink++;
      else if (t.type === 'link_close') inLink--;
      if (t.type !== 'text' || inLink > 0) {
        out.push(t);
        continue;
      }
      let last = 0;
      for (const m of t.content.matchAll(linker.re)) {
        const text = (s: string) => Object.assign(new state.Token('text', '', 0), { content: s, level: t.level });
        if (m.index > last) out.push(text(t.content.slice(last, m.index)));
        const open = new state.Token('link_open', 'a', 1);
        open.attrSet('href', linker.hrefs.get(m[0])!);
        open.attrSet('data-ship', '');
        open.meta = { ship: true };
        out.push(open, text(m[0]), new state.Token('link_close', 'a', -1));
        last = m.index + m[0].length;
      }
      if (last === 0) out.push(t);
      else if (last < t.content.length) out.push(Object.assign(new state.Token('text', '', 0), { content: t.content.slice(last), level: t.level }));
    }
    block.children = out;
  }
}

function newRenderer() {
  const md = new MarkdownIt({ html: false, linkify: true, typographer: false, breaks: true, highlight });
  // A bare file name like main.go is not a link.
  md.linkify.set({ fuzzyLink: false, fuzzyEmail: false });
  md.linkify.add('//', null);
  md.validateLink = safeUrl;
  md.core.ruler.after('inline', 'task_lists', taskLists);
  md.core.ruler.after('task_lists', 'ship_links', shipLinks);
  const open = md.renderer.rules.link_open ?? ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));
  md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
    // A ship link stays in the app, so an installed app keeps its window.
    if (!tokens[idx].meta?.ship) {
      tokens[idx].attrSet('target', '_blank');
      tokens[idx].attrSet('rel', 'noopener noreferrer');
    }
    return open(tokens, idx, options, env, self);
  };
  // An image is never fetched: it shows as a link labelled with its alt text.
  md.renderer.rules.image = (tokens, idx) => {
    const t = tokens[idx];
    const label = md.utils.escapeHtml(t.content || String(t.attrGet('src') ?? ''));
    return `<a href="${md.utils.escapeHtml(String(t.attrGet('src') ?? ''))}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  };
  md.renderer.rules.table_open = () => '<div class="md-table"><table>\n';
  md.renderer.rules.table_close = () => '</table></div>\n';
  return md;
}

const full = newRenderer();
const restricted = newRenderer().disable(['heading', 'lheading', 'image']);

/** Renders transcript Markdown: headings, emphasis, lists, task lists, tables, quotes, links and highlighted code. */
export function renderMarkdown(src: string, links?: ShipLinks): string {
  return full.render(src, { linker: links ? linkerOf(links) : null });
}

/** The same without headings and images, for the short text of a question and its options. */
export function renderRestrictedMarkdown(src: string): string {
  return restricted.render(src);
}

/** The restricted renderer without the wrapping paragraph, for a description inside a button. */
export function renderRestrictedInline(src: string): string {
  return restricted.renderInline(src);
}
