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

for (const [name, lang] of Object.entries({ bash, css, diff, go, javascript, json, markdown, python, sql, swift, typescript, xml, yaml })) hljs.registerLanguage(name, lang);
hljs.registerAliases(['sh', 'shell'], { languageName: 'bash' });

// Only these schemes become links; a target with no scheme is relative.
const safeUrl = (url: string) => !/^[a-z][a-z0-9+.-]*:/i.test(url.trim()) || /^(https?|mailto):/i.test(url.trim());

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

function newRenderer() {
  const md = new MarkdownIt({ html: false, linkify: true, typographer: false, breaks: true, highlight });
  // A bare file name like main.go is not a link.
  md.linkify.set({ fuzzyLink: false, fuzzyEmail: false });
  md.validateLink = safeUrl;
  md.core.ruler.after('inline', 'task_lists', taskLists);
  const open = md.renderer.rules.link_open ?? ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));
  md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
    tokens[idx].attrSet('target', '_blank');
    tokens[idx].attrSet('rel', 'noopener noreferrer');
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
export function renderMarkdown(src: string): string {
  return full.render(src);
}

/** The same without headings and images, for the short text of a question and its options. */
export function renderRestrictedMarkdown(src: string): string {
  return restricted.render(src);
}
