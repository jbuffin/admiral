import { describe, expect, it } from 'vitest';
import { renderMarkdown, renderRestrictedMarkdown } from './markdown';

describe('renderMarkdown', () => {
  it('renders headings, lists, emphasis, inline code, code blocks and links', () => {
    const html = renderMarkdown(
      ['# Plan', '', '- first', '- second', '', 'Some **bold**, *soft* and `code`.', '', '```go', 'x := 1 < 2', '```', '', '[docs](https://example.com/a)'].join('\n'),
    );
    expect(html).toMatch(/<h1[^>]*>Plan<\/h1>/);
    expect(html).toMatch(/<ul[^>]*>\s*<li[^>]*>first<\/li>\s*<li[^>]*>second<\/li>\s*<\/ul>/);
    expect(html).toMatch(/<strong>bold<\/strong>/);
    expect(html).toMatch(/<em>soft<\/em>/);
    expect(html).toMatch(/<code[^>]*>code<\/code>/);
    expect(html).toMatch(/<pre[^>]*>\s*<code[^>]*>x := 1 &lt; 2\s*<\/code>\s*<\/pre>/);
    expect(html).toMatch(/<a [^>]*href="https:\/\/example.com\/a"[^>]*>docs<\/a>/);
  });

  it('renders an ordered list', () => {
    expect(renderMarkdown('1. one\n2. two')).toMatch(/<ol[^>]*>\s*<li[^>]*>one<\/li>\s*<li[^>]*>two<\/li>\s*<\/ol>/);
  });

  it('escapes raw HTML as text', () => {
    const html = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror="window.pwned=1">');
    expect(html).not.toContain('<script');
    expect(html).not.toContain('<img');
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
    expect(html).toContain('&lt;img src=x onerror=&quot;window.pwned=1&quot;&gt;');
  });

  it('escapes HTML inside code and emphasis', () => {
    const html = renderMarkdown('`<b>` and **<i>x</i>**\n\n```\n<script>\n```');
    expect(html).not.toMatch(/<(b|i|script)>/);
  });

  it('gives a javascript: link no href', () => {
    const html = renderMarkdown('[click](javascript:alert(1))');
    expect(html).not.toMatch(/href="?javascript/i);
    expect(html).toContain('click');
  });

  it.each(['data:text/html,<b>x</b>', 'vbscript:msgbox(1)', 'JaVaScRiPt:alert(1)', '&#106;avascript:alert(1)', '&#x6A;avascript:alert(1)'])('gives a %s link no href', (url) => {
    expect(renderMarkdown(`[click](${url})`)).not.toContain('href');
  });

  it('keeps a * in a link target literal', () => {
    const html = renderMarkdown('[a](https://x.test/*one*/two) and *soft* and [b](https://x.test/**k**)');
    expect(html).toContain('href="https://x.test/*one*/two"');
    expect(html).toContain('href="https://x.test/**k**"');
    expect(html).toContain('<em>soft</em>');
  });

  it('keeps a quote in a link target inside its attribute', () => {
    const html = renderMarkdown('[x](https://a.test/"onmouseover="alert(1))');
    expect(html).not.toMatch(/<a [^>]*\sonmouseover=/);
  });
});

const plain = (html: string) => html.replace(/<[^>]*>/g, '').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&amp;/g, '&');

describe('renderMarkdown: tables', () => {
  const table = ['| name | qty | note |', '|:-----|:---:|-----:|', '| apple | 3 | red |', '| pear | 12 | green |'].join('\n');

  it('renders a GFM table with its header and rows', () => {
    const html = renderMarkdown(table);
    expect(html).toMatch(/<table[^>]*>[\s\S]*<thead[^>]*>[\s\S]*<th[^>]*>name<\/th>[\s\S]*<\/thead>[\s\S]*<tbody[^>]*>[\s\S]*<td[^>]*>apple<\/td>[\s\S]*<td[^>]*>12<\/td>/);
  });

  it('keeps the left, center and right alignment of each column', () => {
    const html = renderMarkdown(table);
    expect(html).toMatch(/<th[^>]*text-align:\s*left[^>]*>name<\/th>/);
    expect(html).toMatch(/<th[^>]*text-align:\s*center[^>]*>qty<\/th>/);
    expect(html).toMatch(/<th[^>]*text-align:\s*right[^>]*>note<\/th>/);
    expect(html).toMatch(/<td[^>]*text-align:\s*center[^>]*>12<\/td>/);
    expect(html).toMatch(/<td[^>]*text-align:\s*right[^>]*>green<\/td>/);
  });

  it('wraps each table in a box of its own so a wide one scrolls there', () => {
    const html = renderMarkdown(`${table}\n\ntext between\n\n${table}`);
    expect(html.match(/<div[^>]*>\s*<table/g)).toHaveLength(2);
    expect(html.match(/<\/table>\s*<\/div>/g)).toHaveLength(2);
  });

  it('escapes HTML in a cell', () => {
    const html = renderMarkdown('| a |\n|---|\n| <img src=x onerror=alert(1)> |');
    expect(html).not.toContain('<img');
    expect(html).toContain('&lt;img');
  });
});

describe('renderMarkdown: constructs', () => {
  it('renders a block quote and a horizontal rule', () => {
    const html = renderMarkdown('> quoted words\n\n---\n\nafter');
    expect(html).toMatch(/<blockquote[^>]*>[\s\S]*quoted words[\s\S]*<\/blockquote>/);
    expect(html).toMatch(/<hr\s*\/?>/);
  });

  it('renders strikethrough, _em_ and __strong__', () => {
    const html = renderMarkdown('~~gone~~ and _soft_ and __loud__');
    expect(html).toMatch(/<(s|del)>gone<\/(s|del)>/);
    expect(html).toContain('<em>soft</em>');
    expect(html).toContain('<strong>loud</strong>');
  });

  it('nests a list inside a list item', () => {
    const html = renderMarkdown('- top\n  - inner one\n  - inner two\n- next');
    expect(html).toMatch(/<ul[^>]*>\s*<li[^>]*>top\s*<ul[^>]*>\s*<li[^>]*>inner one<\/li>\s*<li[^>]*>inner two<\/li>\s*<\/ul>\s*<\/li>\s*<li[^>]*>next<\/li>/);
  });

  it('starts a numbered list at its first number', () => {
    expect(renderMarkdown('3. three\n4. four')).toMatch(/<ol[^>]*\sstart="3"[^>]*>/);
  });

  it('shows a task list as read-only checkboxes, checked where marked', () => {
    const html = renderMarkdown('- [ ] open\n- [x] done');
    const boxes = html.match(/<input[^>]*>/g) ?? [];
    expect(boxes).toHaveLength(2);
    for (const b of boxes) {
      expect(b).toMatch(/type="checkbox"/);
      expect(b).toMatch(/disabled/);
    }
    expect(boxes[0]).not.toMatch(/checked/);
    expect(boxes[1]).toMatch(/checked/);
    expect(html).toContain('open');
    expect(html).toContain('done');
  });

  it('leaves a [ ] inside code alone', () => {
    const html = renderMarkdown('- `[ ] not a box`\n\n```\n- [x] nor this\n```');
    expect(html).not.toContain('<input');
  });

  it('turns a bare https URL into a link', () => {
    expect(renderMarkdown('see https://example.com/a/b for more')).toMatch(/<a [^>]*href="https:\/\/example\.com\/a\/b"[^>]*>https:\/\/example\.com\/a\/b<\/a>/);
  });
});

describe('renderMarkdown: links and images', () => {
  it('opens links in a new tab without opener or referrer', () => {
    const html = renderMarkdown('[a](https://example.com) https://example.org');
    const anchors = html.match(/<a [^>]*>/g) ?? [];
    expect(anchors).toHaveLength(2);
    for (const a of anchors) {
      expect(a).toContain('target="_blank"');
      expect(a).toMatch(/rel="[^"]*noopener[^"]*"/);
      expect(a).toMatch(/rel="[^"]*noreferrer[^"]*"/);
    }
  });

  it('shows an image as a link labelled with its alt text and never as an img', () => {
    const html = renderMarkdown('![the diagram](https://x.test/y.png)');
    expect(html).not.toContain('<img');
    expect(html).toMatch(/<a [^>]*href="https:\/\/x\.test\/y\.png"[^>]*>the diagram<\/a>/);
  });

  it('does not make a link of an image with an unsafe address', () => {
    const html = renderMarkdown('![x](javascript:alert(1)) ![y](data:image/png;base64,AAAA)');
    expect(html).not.toContain('<img');
    expect(html).not.toMatch(/href="?(javascript|data):/i);
  });

  it('keeps mailto, relative and fragment links', () => {
    const html = renderMarkdown('[m](mailto:a@b.test) [r](/docs/x) [f](#top)');
    expect(html).toContain('href="mailto:a@b.test"');
    expect(html).toContain('href="/docs/x"');
    expect(html).toContain('href="#top"');
  });

  it('does not link an autolinked javascript: address', () => {
    expect(renderMarkdown('<javascript:alert(1)> and javascript:alert(1)')).not.toMatch(/href="?javascript/i);
  });
});

describe('renderMarkdown: code highlighting', () => {
  it('colours the tokens of a fence in a known language and keeps its text', () => {
    const src = 'func main() {\n\tx := "a"\n}';
    const html = renderMarkdown('```go\n' + src + '\n```');
    expect(html).toMatch(/<span class="hljs-keyword">func<\/span>/);
    expect(html).toMatch(/hljs-string/);
    expect(plain(html).trim()).toBe(src);
  });

  it.each(['typescript', 'javascript', 'json', 'bash', 'sh', 'shell', 'python', 'yaml', 'diff', 'sql', 'css', 'xml', 'html', 'markdown', 'swift'])('knows the language %s', (lang) => {
    const samples: Record<string, string> = {
      typescript: 'const x: number = 1;', javascript: 'const x = 1;', json: '{"a": 1}', bash: 'echo "hi"', sh: 'echo "hi"', shell: 'echo "hi"',
      python: 'def f():\n    return 1', yaml: 'a: 1\nb: [x]', diff: '+added\n-removed', sql: 'SELECT a FROM t;', css: 'a { color: red; }',
      xml: '<a href="x">y</a>', html: '<a href="x">y</a>', markdown: '# head\n**b**', swift: 'let x = 1',
    };
    expect(renderMarkdown('```' + lang + '\n' + samples[lang] + '\n```')).toContain('hljs-');
  });

  it('leaves an unknown or missing language escaped and unhighlighted', () => {
    for (const fence of ['```nosuchlang\n<b>x</b> func\n```', '```\n<b>x</b> func\n```']) {
      const html = renderMarkdown(fence);
      expect(html).not.toContain('hljs-');
      expect(html).not.toContain('<b>');
      expect(html).toContain('&lt;b&gt;x&lt;/b&gt; func');
    }
  });

  it('escapes HTML in a highlighted fence', () => {
    const html = renderMarkdown('```html\n<script>alert(1)</script>\n```');
    expect(html).not.toContain('<script>');
    expect(plain(html)).toContain('<script>alert(1)</script>');
  });
});

describe('renderRestrictedMarkdown', () => {
  it('keeps emphasis, code, lists and links', () => {
    const html = renderRestrictedMarkdown('**bold** and `code`\n\n- one\n- two\n\n[docs](https://example.com)');
    expect(html).toContain('<strong>bold</strong>');
    expect(html).toMatch(/<code[^>]*>code<\/code>/);
    expect(html).toMatch(/<ul[^>]*>\s*<li[^>]*>one<\/li>/);
    expect(html).toMatch(/<a [^>]*href="https:\/\/example\.com"[^>]*target="_blank"|<a [^>]*target="_blank"[^>]*href="https:\/\/example\.com"/);
  });

  it('has no headings but keeps their words', () => {
    const html = renderRestrictedMarkdown('# Big title\n\n## Smaller\n\nbody');
    expect(html).not.toMatch(/<h[1-6]/);
    expect(html).toContain('Big title');
    expect(html).toContain('Smaller');
  });

  it('has no images', () => {
    const html = renderRestrictedMarkdown('![alt text](https://x.test/y.png)');
    expect(html).not.toContain('<img');
  });

  it('still escapes raw HTML', () => {
    const html = renderRestrictedMarkdown('<script>alert(1)</script>');
    expect(html).not.toContain('<script');
  });
});
