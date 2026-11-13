import { useEffect, useId, useState } from 'preact/hooks';
import { call } from './api';
import { crewApi, type CrewMember } from './crew';
import { filterSkills, isChosen, pickSkill, rankSkills, skillWarning, typeSkill, type SkillEntry } from './crewSkills';
import { Flag } from './Flag';

type Listing = { state: 'loading' } | { state: 'ready'; skills: SkillEntry[]; noDock: boolean } | { state: 'failed'; message: string };

/** The installed skills for the form's harness, or every dock's when none is chosen; a change of harness fetches again. */
function useInstalled(fleetKey: string, harness: string): Listing {
  const [listing, setListing] = useState<Listing>({ state: 'loading' });
  useEffect(() => {
    let live = true;
    setListing({ state: 'loading' });
    void call<{ skills?: SkillEntry[] }>('GET', crewApi(fleetKey, harness, '/skills')).then((r) => {
      if (!live) return;
      if (r.ok) setListing({ state: 'ready', skills: r.data?.skills ?? [], noDock: false });
      else if (r.status === 400 && harness) setListing({ state: 'ready', skills: [], noDock: true });
      else setListing({ state: 'failed', message: r.error });
    });
    return () => {
      live = false;
    };
  }, [fleetKey, harness]);
  return listing;
}

interface Props {
  fleetKey: string;
  harness: string;
  skills: string[];
  onChange: (skills: string[]) => void;
  role: string;
  pickMeWhen: string;
  members: CrewMember[];
  self?: string;
  error?: string;
}

export function SkillPicker({ fleetKey, harness, skills, onChange, role, pickMeWhen, members, self, error }: Props) {
  const id = useId();
  const listing = useInstalled(fleetKey, harness);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [refusal, setRefusal] = useState('');
  const installed = listing.state === 'ready' ? listing.skills : [];
  const ranked = rankSkills(installed, { role, pickMeWhen, chosen: skills, members, self });
  const shown = filterSkills(ranked, query);
  const typed = typeSkill(skills, query, installed);
  const add = (next: string[]) => {
    onChange(next);
    setQuery('');
    setRefusal('');
  };
  // Only an add attempt can be refused: a person filtering by words is not entering a name.
  const attempt = () => {
    const name = query.trim();
    const row = installed.find((s) => s.name === name) ?? shown[0];
    if (row && name) {
      if (isChosen(skills, row)) setRefusal(`${row.name} is already added.`);
      else add(pickSkill(skills, row.name));
    } else if (typed.ok) add(typed.skills);
    else setRefusal(typed.reason);
  };
  const errId = `${id}-err`;
  const listId = `${id}-list`;
  const hintId = `${id}-hint`;
  return (
    <div class="field crew-field skill-picker">
      <span class="skill-label">Skills</span>
      <ul class="skill-pills" aria-label="Skills" aria-describedby={error ? errId : undefined}>
        {skills.map((name) => {
          const warning = listing.state === 'loading' || listing.state === 'failed' ? null : skillWarning(name, installed, harness);
          return (
            <li key={name} class="skill-pill">
              <span class="skill-pill-name">{name}</span>
              {warning ? <span class="skill-pill-warn">{warning}</span> : null}
              <button type="button" class="skill-pill-remove" aria-label={`Remove ${name}`} onClick={() => onChange(skills.filter((s) => s !== name))}>
                ×
              </button>
            </li>
          );
        })}
      </ul>
      {error ? (
        <p id={errId} class="note error">
          {error}
        </p>
      ) : null}
      <div>
        <button
          type="button"
          class="btn"
          aria-expanded={open}
          onClick={() => {
            setOpen(!open);
            setQuery('');
            setRefusal('');
          }}
        >
          Add skill
        </button>
      </div>
      {open ? (
        <div class="skill-add">
          <input
            type="text"
            role="combobox"
            aria-label="Search skills"
            aria-expanded="true"
            aria-controls={shown.length > 0 ? listId : undefined}
            aria-autocomplete="list"
            aria-describedby={refusal ? hintId : undefined}
            aria-invalid={refusal ? true : undefined}
            value={query}
            autocapitalize="off"
            autocomplete="off"
            spellcheck={false}
            placeholder="Search or type a skill name"
            onInput={(e) => {
              setQuery((e.currentTarget as HTMLInputElement).value);
              setRefusal('');
            }}
            onKeyDown={(e) => {
              if (e.key !== 'Enter') return;
              e.preventDefault();
              attempt();
            }}
          />
          {refusal ? (
            <p id={hintId} class="note error">
              {refusal}
            </p>
          ) : null}
          {query.trim() ? (
            <button type="button" class="btn" onClick={() => (typed.ok ? add(typed.skills) : setRefusal(typed.reason))}>
              {`Add "${query.trim()}"`}
            </button>
          ) : null}
          {listing.state === 'loading' ? <p class="note">Loading skills…</p> : null}
          {listing.state === 'failed' ? <p class="note error">{listing.message}</p> : null}
          {listing.state === 'ready' && listing.noDock ? <p class="note">This fleet has no {harness} dock, so no skills are listed.</p> : null}
          {shown.length > 0 ? (
            <ul class="skill-options" role="listbox" id={listId} aria-label="Installed skills">
              {shown.map((s) => (
                <li key={s.name} class="skill-choice" role="option" aria-selected={isChosen(skills, s)} onClick={() => (isChosen(skills, s) ? undefined : add(pickSkill(skills, s.name)))}>
                  <span class="skill-name">{s.name}</span>
                  {s.description ? <span class="skill-desc">{s.description}</span> : null}
                  <span class="skill-flags">
                    {s.harnesses.map((h) => (
                      <Flag key={h} harness={h} size="row" />
                    ))}
                  </span>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
