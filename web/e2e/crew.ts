import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import type { Admiral } from './fixtures';
import { seedFleets } from './shell';

const member = (name: string, role: string, pick: string, extra = '') => `---\nname: ${name}\nrole: ${role}\n${extra}pick-me-when: ${pick}\n---\n\nWork as ${name}.\n`;

function write(path: string, text: string) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
}

/** Adds `count` more built-in members, mate01 and on, so the list outgrows the screen. Call after `seedCrew`, before `start`. */
export function seedManyBuiltIns(a: Admiral, count: number) {
  const crew = join(a.home, 'identity', 'zulu-claude', 'skills', 'commodore', 'crew');
  for (let i = 1; i <= count; i++) {
    const name = `mate${String(i).padStart(2, '0')}`;
    write(join(crew, `${name}.md`), member(name, 'implementer', `Pick for chore ${name}.`));
  }
}

/**
 * Westwind's crew: built-in scout and lookout under the claude dock's Commodore; fleet members ada (codex), bosun, cook and dory,
 * and a fleet lookout that customizes the built-in one. Call before `start`.
 */
export function seedCrew(a: Admiral) {
  seedFleets(a);
  const commodore = join(a.home, 'identity', 'zulu-claude', 'skills', 'commodore');
  write(join(commodore, 'SKILL.md'), '---\nname: commodore\ndescription: a fake skill\n---\n');
  write(join(commodore, 'crew', 'scout.md'), member('scout', 'scribe', 'Pick for the log.'));
  write(join(commodore, 'crew', 'lookout.md'), member('lookout', 'reviewer', 'Pick for a review.'));

  const crew = join(a.home, 'fleets', 'zulu', 'crew');
  write(join(crew, 'lookout.md'), member('lookout', 'reviewer', 'Pick for a sharper review.', 'harness: codex\n'));
  write(join(crew, 'ada.md'), member('ada', 'implementer', 'Pick for parser work.', 'harness: codex\n'));
  write(join(crew, 'bosun.md'), member('bosun', 'implementer', 'Pick for rigging.'));
  write(join(crew, 'cook.md'), member('cook', 'scribe', 'Pick for the galley.'));
  write(join(crew, 'dory.md'), member('dory', 'implementer', 'Pick for small jobs.'));

  const mem = join(crew, 'memory');
  write(join(mem, 'scout', 'fleet.md'), '- 2026-11-10 (run-a): scout keeps the log short\n');
  write(join(mem, 'ada', 'fleet.md'), '- 2026-11-10 (run-a): ada reads the grammar first\n');
  write(join(mem, 'ada', 'repos', 'acme', 'api.md'), '- 2026-11-10 (run-b): ada runs make check\n- 2026-11-10 (run-b): ada keeps handlers thin\n');
  write(join(mem, 'bosun', 'fleet.md'), '- 2026-11-10 (run-a): bosun ties a bowline\n');
  write(join(mem, 'cook', 'fleet.md'), '- 2026-11-10 (run-a): cook salts early\n');
}
