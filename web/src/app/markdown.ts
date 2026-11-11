// A small Markdown renderer for transcript text, which is untrusted: every character is escaped before any tag is added.

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');

const safeUrl = (url: string) => /^(https?:|mailto:|\/|#)/i.test(url.trim());

function inline(src: string): string {
  const codes: string[] = [];
  let s = src.replace(/`([^`\n]+)`/g, (_, c: string) => {
    codes.push(`<code>${escape(c)}</code>`);
    return `\u0000${codes.length - 1}\u0000`;
  });
  s = escape(s);
  s = s.replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/g, (_, text: string, url: string) => {
    const raw = url.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
    return safeUrl(raw) ? `<a href="${escape(raw)}" target="_blank" rel="noopener noreferrer">${text}</a>` : text;
  });
  s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>').replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>');
  return s.replace(/\u0000(\d+)\u0000/g, (_, i: string) => codes[Number(i)]);
}

const BULLET = /^\s*[-*+]\s+(.*)$/;
const ORDERED = /^\s*\d+[.)]\s+(.*)$/;

/** Renders headings, lists, emphasis, inline code, code blocks and links; anything else is text. */
export function renderMarkdown(src: string): string {
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  const out: string[] = [];
  let para: string[] = [];
  const flush = () => {
    if (para.length > 0) out.push(`<p>${para.map(inline).join('<br>')}</p>`);
    para = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = /^\s*```/.exec(line);
    if (fence) {
      flush();
      const code: string[] = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) code.push(lines[i]);
      out.push(`<pre><code>${escape(code.join('\n'))}\n</code></pre>`);
      continue;
    }
    const heading = /^(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (heading) {
      flush();
      out.push(`<h${heading[1].length}>${inline(heading[2])}</h${heading[1].length}>`);
      continue;
    }
    const list = BULLET.test(line) ? BULLET : ORDERED.test(line) ? ORDERED : null;
    if (list) {
      flush();
      const tag = list === BULLET ? 'ul' : 'ol';
      const items: string[] = [];
      for (; i < lines.length && list.test(lines[i]); i++) items.push(`<li>${inline(list.exec(lines[i])![1])}</li>`);
      i--;
      out.push(`<${tag}>${items.join('')}</${tag}>`);
      continue;
    }
    if (line.trim() === '') flush();
    else para.push(line);
  }
  flush();
  return out.join('');
}
