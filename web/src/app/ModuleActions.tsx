import { createContext } from "preact";
import { useContext, useEffect, useId, useRef, useState } from "preact/hooks";
import {
  fetchModuleForm,
  runModuleAction,
  type FormField,
  type ModuleForm,
  type PageAction,
} from "./api";
import { Actions, Button } from "./Button";
import { openableRow } from "./modules";
import { Sheet, useCancelFocus } from "./Sheet";
import { showResult } from "./ToastRegion";

type Values = Record<string, string | string[]>;
type Run = {
  phase: "running" | "updating";
  action: string;
  version?: number;
  token: number;
};

/** How long a row waits for the page to change after a run before its module actions come back. */
export const UPDATING_FALLBACK_MS = 30_000;

interface Controller {
  runs: Record<string, Run>;
  /** True while the page is in error: no module action runs. */
  blocked: boolean;
  press: (row: string, action: PageAction) => void;
}

const Ctx = createContext<Controller | null>(null);
export const ModuleActionsProvider = Ctx.Provider;

export const useUpdating = (row: string) =>
  useContext(Ctx)?.runs[row]?.phase === "updating";

/** A module action's sheet and run state for one page: one run per row, then "updating…" until the page's version moves. */
export function useModuleActions(
  sourceKey: string,
  version: number | undefined,
  blocked: boolean,
) {
  const [runs, setRuns] = useState<Record<string, Run>>({});
  const [sheet, setSheet] = useState<{
    row: string;
    action: PageAction;
  } | null>(null);
  const current = useRef({ version, runs });
  current.current = { version, runs };
  const tokens = useRef(0);
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);

  useEffect(() => () => timers.current.forEach(clearTimeout), []);

  useEffect(() => {
    setRuns((r) => {
      const keep = Object.entries(r).filter(
        ([, x]) => x.phase === "running" || x.version === version,
      );
      return keep.length === Object.keys(r).length
        ? r
        : Object.fromEntries(keep);
    });
  }, [version]);

  const drop = (row: string, token: number) =>
    setRuns((r) => {
      if (r[row]?.token !== token) return r;
      const { [row]: _gone, ...rest } = r;
      return rest;
    });

  const run = async (row: string, action: PageAction, values: Values) => {
    const token = ++tokens.current;
    const startVersion = current.current.version;
    setRuns((r) => ({
      ...r,
      [row]: { phase: "running", action: action.id, token },
    }));
    const res = await runModuleAction(sourceKey, row, action.id, values);
    const tag = `module-action:${sourceKey}:${row}`;
    if (!res.ok) {
      showResult({ tag, name: action.label, line: res.error, error: true });
      return drop(row, token);
    }
    showResult({
      tag,
      name: action.label,
      line: res.data.message,
      url: res.data.url,
    });
    if (current.current.version !== startVersion) return drop(row, token);
    setRuns((r) => ({
      ...r,
      [row]: {
        phase: "updating",
        action: action.id,
        version: startVersion,
        token,
      },
    }));
    timers.current.push(
      setTimeout(() => drop(row, token), UPDATING_FALLBACK_MS),
    );
  };

  const controller: Controller = {
    runs,
    blocked,
    press: (row, action) => {
      if (!openableRow(row) || !openableRow(action.id)) return;
      if (action.url || blocked || runs[row]) return;
      if (action.confirm === "yesno" || action.confirm === "form")
        setSheet({ row, action });
      else void run(row, action, {});
    },
  };

  const element = sheet ? (
    sheet.action.confirm === "form" ? (
      <FormSheet
        sourceKey={sourceKey}
        row={sheet.row}
        action={sheet.action}
        onClose={() => setSheet(null)}
        onSubmit={(v) => (setSheet(null), void run(sheet.row, sheet.action, v))}
      />
    ) : (
      <ConfirmSheet
        action={sheet.action}
        onClose={() => setSheet(null)}
        onConfirm={() => (
          setSheet(null),
          void run(sheet.row, sheet.action, {})
        )}
      />
    )
  ) : null;
  return { controller, sheet: element };
}

/** A row's or step's module actions; a built-in action with a url stays an inert button. */
export function ModuleActions({
  row,
  one,
  many,
}: {
  row: string;
  one?: PageAction;
  many?: PageAction[];
}) {
  const ctl = useContext(Ctx);
  const all = [...(one ? [one] : []), ...(many ?? [])];
  const run = ctl?.runs[row];
  return all.length === 0 ? null : (
    <span class="mactions">
      {all.map((a) => {
        const running = run?.phase === "running" && run.action === a.id;
        return (
          <button
            key={a.id}
            type="button"
            class="btn mact"
            disabled={
              !ctl ||
              !!a.url ||
              ctl.blocked ||
              !!run ||
              !openableRow(row) ||
              !openableRow(a.id)
            }
            aria-busy={running ? "true" : undefined}
            onClick={() => ctl?.press(row, a)}
          >
            {a.label}
            {running ? <span class="spinner" aria-hidden="true" /> : null}
          </button>
        );
      })}
    </span>
  );
}

function ConfirmSheet({
  action,
  onClose,
  onConfirm,
}: {
  action: PageAction;
  onClose: () => void;
  onConfirm: () => void;
}) {
  useCancelFocus(true);
  return (
    <Sheet
      label={action.label}
      kind="launch-backdrop stop-backdrop"
      onClose={onClose}
    >
      {action.summary ? <p class="line">{action.summary}</p> : null}
      <Actions class="launch-foot">
        <Button label="Cancel" onClick={onClose} />
        <Button label={action.label} variant="primary" onClick={onConfirm} />
      </Actions>
    </Sheet>
  );
}

const minutes = (hhmm: string) => {
  const m = /^(\d{1,2}):([0-5]\d)$/.exec(hhmm.trim());
  return m ? Number(m[1]) * 60 + Number(m[2]) : null;
};

function startValues(form: ModuleForm): Values {
  const v: Values = {};
  for (const f of form.fields) {
    if (f.type === "checklist")
      v[f.id] = Array.isArray(f.default)
        ? f.default
        : (f.options ?? []).map((o) => o.id);
    else
      v[f.id] =
        typeof f.default === "string"
          ? f.default
          : f.type === "choice"
            ? (f.options?.[0]?.id ?? "")
            : "";
  }
  return v;
}

/** The note under a duration that can't be submitted, or '' when it can. */
function durationNote(f: FormField, value: string) {
  const m = minutes(value);
  if (m === null) return "Use hh:mm.";
  const min = f.min ? minutes(f.min) : null;
  return min !== null && m < min ? `At least ${f.min}.` : "";
}

function FormSheet({
  sourceKey,
  row,
  action,
  onClose,
  onSubmit,
}: {
  sourceKey: string;
  row: string;
  action: PageAction;
  onClose: () => void;
  onSubmit: (v: Values) => void;
}) {
  const [form, setForm] = useState<ModuleForm | null>(null);
  const [error, setError] = useState("");
  const [values, setValues] = useState<Values>({});
  useEffect(() => {
    let stale = false;
    void fetchModuleForm(sourceKey, row, action.id).then((r) => {
      if (stale) return;
      if (r.ok) {
        const f = { ...r.data, fields: r.data.fields ?? [] };
        setForm(f);
        setValues(startValues(f));
      } else setError(r.error);
    });
    return () => {
      stale = true;
    };
  }, [sourceKey, row, action.id]);

  const notes = form
    ? form.fields.map((f) =>
        f.type === "duration"
          ? durationNote(f, String(values[f.id] ?? ""))
          : "",
      )
    : [];
  const set = (id: string, v: string | string[]) =>
    setValues((cur) => ({ ...cur, [id]: v }));
  return (
    <Sheet
      label={form?.title || action.label}
      kind="launch-backdrop"
      onClose={onClose}
    >
      {error ? (
        <p class="launch-error" role="alert">
          {error}
        </p>
      ) : null}
      {!form && !error ? (
        <div class="skeleton" role="status" aria-label="Loading" />
      ) : null}
      {form ? (
        <form
          class="mform"
          onSubmit={(e) => {
            e.preventDefault();
            if (notes.every((n) => !n)) onSubmit(values);
          }}
        >
          {form.fields.map((f, i) => (
            <Field
              key={f.id}
              field={f}
              value={values[f.id]}
              note={notes[i]}
              onChange={(v) => set(f.id, v)}
            />
          ))}
          <Actions class="launch-foot">
            <Button label="Cancel" onClick={onClose} />
            <Button
              label="Submit"
              variant="primary"
              disabled={notes.some((n) => n)}
              onClick={() => notes.every((n) => !n) && onSubmit(values)}
            />
          </Actions>
        </form>
      ) : (
        <Actions class="launch-foot">
          <Button label="Close" onClick={onClose} />
        </Actions>
      )}
    </Sheet>
  );
}

function Field({
  field,
  value,
  note,
  onChange,
}: {
  field: FormField;
  value: string | string[] | undefined;
  note: string;
  onChange: (v: string | string[]) => void;
}) {
  const id = useId();
  if (field.type === "duration") {
    return (
      <div class="mform-field">
        <label class="mform-label" for={id}>
          {field.label}
        </label>
        <input
          id={id}
          type="text"
          autocomplete="off"
          value={String(value ?? "")}
          onInput={(e) => onChange(e.currentTarget.value)}
        />
        {note ? <p class="mform-note">{note}</p> : null}
      </div>
    );
  }
  const options = field.options ?? [];
  const picked = Array.isArray(value) ? value : [];
  return (
    <fieldset>
      <legend>{field.label}</legend>
      {options.map((o) => (
        <label key={o.id} class="mform-option">
          {field.type === "choice" ? (
            <input
              type="radio"
              name={id}
              checked={value === o.id}
              onChange={() => onChange(o.id)}
            />
          ) : (
            <input
              type="checkbox"
              checked={picked.includes(o.id)}
              onChange={(e) =>
                onChange(
                  options
                    .map((x) => x.id)
                    .filter((x) =>
                      x === o.id ? e.currentTarget.checked : picked.includes(x),
                    ),
                )
              }
            />
          )}
          {o.label}
        </label>
      ))}
    </fieldset>
  );
}
