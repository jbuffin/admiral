import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './markdown';

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
