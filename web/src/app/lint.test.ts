import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { ESLint } from 'eslint';
import { describe, expect, it } from 'vitest';

const web = resolve(fileURLToPath(import.meta.url), '../../..');
const eslint = new ESLint({ cwd: web, overrideConfigFile: resolve(web, 'eslint.config.js') });

const lint = async (code: string, filePath: string) => (await eslint.lintText(code, { filePath: resolve(web, filePath) }))[0].messages;
const withRule = (messages: Awaited<ReturnType<typeof lint>>, id: string) => messages.filter((m) => m.ruleId === id);
const rulesOf = (messages: Awaited<ReturnType<typeof lint>>) => messages.filter((m) => m.ruleId).map((m) => m.ruleId);

const effect = `import { useEffect, useLayoutEffect } from 'preact/hooks';\nexport function C() {\n  useEffect(() => {}, []);\n  return null;\n}\n`;
const layoutEffect = `import { useLayoutEffect } from 'preact/hooks';\nexport function C() {\n  useLayoutEffect(() => {}, []);\n  return null;\n}\n`;

const EFFECT_RULE = 'admiral/no-raw-effect';
const DEFERRAL_RULE = 'admiral/no-deferral';
const HOOKS_DIR = 'web/src/app/hooks/';

describe('effect budget', () => {
  for (const [label, path] of [
    ['the new shell', 'src/app/Example.tsx'],
    ['the old UI', 'src/Example.tsx'],
  ]) {
    it(`bans a raw useEffect in a component file of ${label}`, async () => {
      const found = withRule(await lint(effect, path), EFFECT_RULE);
      expect(found).toHaveLength(1);
      expect(found[0].severity).toBe(2);
      expect(found[0].message).toContain(HOOKS_DIR);
    });
    it(`bans a raw useLayoutEffect in a component file of ${label}`, async () => {
      const found = withRule(await lint(layoutEffect, path), EFFECT_RULE);
      expect(found).toHaveLength(1);
      expect(found[0].message).toContain(HOOKS_DIR);
    });
  }

  for (const path of ['src/app/hooks/useExample.ts', 'src/app/hooks/Example.tsx', 'src/app/Example.test.tsx', 'src/app/example.ts']) {
    it(`allows a raw effect in ${path}`, async () => {
      expect(withRule(await lint(effect, path), EFFECT_RULE)).toEqual([]);
      expect(withRule(await lint(layoutEffect, path), EFFECT_RULE)).toEqual([]);
    });
  }
});

describe('deferral ban', () => {
  const banned: [string, string][] = [
    ['setTimeout with no delay', `setTimeout(() => {});`],
    ['setTimeout with a zero delay', `setTimeout(() => {}, 0);`],
    ['window.setTimeout with a zero delay', `window.setTimeout(() => {}, 0);`],
    ['requestAnimationFrame', `requestAnimationFrame(() => {});`],
    ['a sleep promise', `export async function f() {\n  await new Promise((r) => setTimeout(r, 250));\n}`],
  ];
  for (const [label, path] of [
    ['new shell', 'src/app/example.ts'],
    ['old UI', 'src/example.ts'],
  ]) {
    for (const [what, code] of banned) {
      it(`bans ${what} in the ${label}`, async () => {
        const found = withRule(await lint(code, path), DEFERRAL_RULE);
        expect(found.length).toBeGreaterThan(0);
        expect(found[0].severity).toBe(2);
        expect(found[0].message).toContain(HOOKS_DIR);
        expect(found[0].message).toMatch(/useFocusOnOpen|usePollUntil/);
      });
    }
  }

  const allowed: [string, string][] = [
    ['setTimeout with a real delay', `setTimeout(() => {}, 1500);`],
    ['setInterval', `setInterval(() => {}, 1000);`],
    ['a computed delay', `const cond = Math.random() > 0.5;\nconst TYPING_MS = 30;\nsetTimeout(() => {}, cond ? TYPING_MS : 0);`],
    ['a timeout race', `const READ_MS = 2000;\nexport const p = new Promise((_, no) => setTimeout(() => no(new Error('timeout')), READ_MS));`],
  ];
  for (const [what, code] of allowed) {
    it(`allows ${what}`, async () => {
      expect(withRule(await lint(code, 'src/app/example.ts'), DEFERRAL_RULE)).toEqual([]);
    });
  }

  it('lets a test file use any of them', async () => {
    for (const [, code] of banned) expect(withRule(await lint(code, 'src/app/example.test.ts'), DEFERRAL_RULE)).toEqual([]);
  });

  it('reports the effect ban and the deferral ban under different rule ids', async () => {
    const both = effect + `setTimeout(() => {}, 0);\n`;
    const ids = rulesOf(await lint(both, 'src/app/Example.tsx'));
    expect(ids).toContain(EFFECT_RULE);
    expect(ids).toContain(DEFERRAL_RULE);
    expect(EFFECT_RULE).not.toBe(DEFERRAL_RULE);
  });
});

describe('react-hooks', () => {
  it('rules-of-hooks is an error on a hook inside an if', async () => {
    const code = `import { useState } from 'preact/hooks';\nexport function C({ a }: { a: boolean }) {\n  if (a) {\n    useState(0);\n  }\n  return null;\n}\n`;
    const found = withRule(await lint(code, 'src/app/Example.tsx'), 'react-hooks/rules-of-hooks');
    expect(found).toHaveLength(1);
    expect(found[0].severity).toBe(2);
  });

  it('exhaustive-deps is an error on a missing dependency', async () => {
    const code = `import { useMemo } from 'preact/hooks';\nexport function C({ n }: { n: number }) {\n  return useMemo(() => n * 2, []);\n}\n`;
    const found = withRule(await lint(code, 'src/app/hooks/useExample.ts'), 'react-hooks/exhaustive-deps');
    expect(found).toHaveLength(1);
    expect(found[0].severity).toBe(2);
  });
});

describe('inline comments', () => {
  it('an eslint-disable-next-line does not silence a banned call', async () => {
    const code = `// eslint-disable-next-line admiral/no-deferral\nsetTimeout(() => {}, 0);\n`;
    const messages = await lint(code, 'src/app/example.ts');
    expect(withRule(messages, DEFERRAL_RULE)).toHaveLength(1);
    expect(messages.some((m) => m.ruleId === null && m.severity === 1)).toBe(true);
  });
});

describe('TypeScript syntax', () => {
  const ts = [
    `export const id = <T,>(x: T) => x;`,
    `type Foo = { a: number };\nexport const f = (x: unknown) => x as Foo;`,
    `export const o = { a: 1 } satisfies Record<string, number>;`,
    `export interface Thing {\n  name: string;\n}`,
    `export type Union = 'a' | 'b' | 1;`,
  ];
  it('parses TypeScript in a .ts file', async () => {
    for (const code of ts) expect((await lint(code, 'src/app/example.ts')).filter((m) => m.fatal)).toEqual([]);
  });
  it('parses TypeScript in a .tsx file, with JSX and a typed prop', async () => {
    const jsx = `function Box({ n }: { n: number }) {\n  return <p>{n}</p>;\n}\nexport const x = <Box n={1} />;`;
    for (const code of [...ts.filter((c) => !c.includes('<T,>')), `export const id = <T,>(x: T) => x;`, jsx]) {
      expect((await lint(code, 'src/app/Example.tsx')).filter((m) => m.fatal)).toEqual([]);
    }
  });
});

describe('exceptions list', () => {
  it('holds no entry under src/app/hooks/', () => {
    const list = JSON.parse(readFileSync(resolve(web, 'eslint-suppressions.json'), 'utf8')) as Record<string, unknown>;
    expect(Object.keys(list).filter((p) => p.includes('src/app/hooks/'))).toEqual([]);
  });
});
