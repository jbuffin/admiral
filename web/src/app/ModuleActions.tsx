import { createContext } from "preact";
import { useContext, useEffect, useId, useRef, useState } from "preact/hooks";
import {
  fetchLaunchTarget,
  fetchMessagePreview,
  fetchModuleForm,
  runModuleAction,
  sendMessage,
  type ActionTarget,
  type FormField,
  type ModuleForm,
  type PageAction,
} from "./api";
import { Actions, Button } from "./Button";
import { fleets, fleetSearch } from "./fleets";
import { LaunchForm } from "./LaunchDialog";
import { openableRow } from "./modules";
import { go, shipPath } from "./router";
import { Sheet, useCancelFocus } from "./Sheet";
import { showResult } from "./ToastRegion";

type Values = Record<string, string | string[]>;
type Run = {
  phase: "running" | "updating";
  action: string;
  version?: number;
  token: number;
};

/** How long a row or notice waits for the page to change after a run before its module actions come back. */
export const UPDATING_FALLBACK_MS = 30_000;

/** The action id a built-in action's first request is out for, per row. */
type Pending = Record<string, string>;

const isWeb = (u?: string) => !!u && /^https?:\/\//i.test(u);
/** What an action does: a bare url is Open link, and a module action has no builtin. */
export const builtinOf = (a: PageAction) => a.builtin ?? (a.url ? "open" : undefined);

interface Controller {
  runs: Record<string, Run>;
  pending: Pending;
  /** True while the page is in error: no module action runs. */
  blocked: boolean;
  press: (target: ActionTarget, action: PageAction) => void;
}

/** A target's key in the run table; the kind prefix keeps a row and a notice with one id apart. */
export const targetKey = (t: ActionTarget) => `${t.notice ? "notice" : "row"}:${t.id}`;

const Ctx = createContext<Controller | null>(null);
export const ModuleActionsProvider = Ctx.Provider;

export const useUpdating = (target: ActionTarget) =>
  useContext(Ctx)?.runs[targetKey(target)]?.phase === "updating";

/** A module action's sheet and run state for one page: one run per row or notice, then "updating…" until the page's version moves. */
export function useModuleActions(
  sourceKey: string,
  version: number | undefined,
  blocked: boolean,
) {
  const [runs, setRuns] = useState<Record<string, Run>>({});
  const [sheet, setSheet] = useState<{
    target: ActionTarget;
    action: PageAction;
  } | null>(null);
  const [pending, setPending] = useState<Pending>({});
  const [launch, setLaunch] = useState<{ url: string; fleet: string } | null>(
    null,
  );
  const [message, setMessage] = useState<{
    row: string;
    action: PageAction;
    text: string;
    chat: string;
    busy: boolean;
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

  const drop = (key: string, token: number) =>
    setRuns((r) => {
      if (r[key]?.token !== token) return r;
      const { [key]: _gone, ...rest } = r;
      return rest;
    });

  const run = async (target: ActionTarget, action: PageAction, values: Values) => {
    const key = targetKey(target);
    const token = ++tokens.current;
    const startVersion = current.current.version;
    setRuns((r) => ({
      ...r,
      [key]: { phase: "running", action: action.id, token },
    }));
    const res = await runModuleAction(sourceKey, target, action.id, values);
    const tag = `module-action:${sourceKey}:${target.notice ? `notice:${target.id}` : target.id}`;
    if (!res.ok) {
      showResult({ tag, name: action.label, line: res.error, error: true });
      return drop(key, token);
    }
    showResult({
      tag,
      name: action.label,
      line: res.data.message,
      url: res.data.url,
    });
    if (current.current.version !== startVersion) return drop(key, token);
    setRuns((r) => ({
      ...r,
      [key]: {
        phase: "updating",
        action: action.id,
        version: startVersion,
        token,
      },
    }));
    timers.current.push(
      setTimeout(() => drop(key, token), UPDATING_FALLBACK_MS),
    );
  };

  const failed = (action: PageAction, row: string, error: string) =>
    showResult({
      tag: `module-action:${sourceKey}:${row}`,
      name: action.label,
      line: error,
      error: true,
    });
  const settle = (row: string) =>
    setPending((p) => {
      const { [row]: _gone, ...rest } = p;
      return rest;
    });

  // Launch asks the server which fleet the issue routes to; a refusal shows its reason and opens nothing.
  const startLaunch = async (row: string, action: PageAction) => {
    setPending((p) => ({ ...p, [row]: action.id }));
    const r = await fetchLaunchTarget(sourceKey, row, action.id);
    settle(row);
    if (!r.ok) return failed(action, row, r.error);
    if (!fleets.value.some((f) => f.key === r.data.fleet))
      return failed(action, row, `Fleet ${r.data.fleet} is not available.`);
    setLaunch(r.data);
  };

  const startMessage = async (row: string, action: PageAction) => {
    setPending((p) => ({ ...p, [row]: action.id }));
    const r = await fetchMessagePreview(sourceKey, row, action.id);
    settle(row);
    if (!r.ok) return failed(action, row, r.error);
    setMessage({ row, action, text: r.data.text, chat: r.data.chat, busy: false });
  };

  // The server posts only the text it composes now: when it refuses the text, ask again with the new one.
  const sendConfirmed = async () => {
    const m = message;
    if (!m || m.busy) return;
    setMessage({ ...m, busy: true });
    const r = await sendMessage(sourceKey, m.row, m.action.id, m.text);
    if (r.ok) {
      setMessage(null);
      return showResult({
        tag: `module-action:${sourceKey}:${m.row}`,
        name: m.action.label,
        line: "Sent to AdmiralBot",
        url: r.data.chat,
        linkLabel: "Open the chat",
      });
    }
    if (r.status === 409) {
      const again = await fetchMessagePreview(sourceKey, m.row, m.action.id);
      if (again.ok && again.data.text !== m.text)
        return setMessage({ ...m, text: again.data.text, chat: again.data.chat, busy: false });
    }
    setMessage(null);
    failed(m.action, m.row, r.error);
  };

  const controller: Controller = {
    runs,
    pending,
    blocked,
    press: (target, action) => {
      if (!openableRow(target.id) || !openableRow(action.id)) return;
      const kind = builtinOf(action);
      const key = targetKey(target);
      if (kind === "open" || runs[key]) return;
      if (kind) {
        // Built-in actions belong to rows; a notice never draws them.
        if (target.notice || pending[target.id]) return;
        if (kind === "launch") return void startLaunch(target.id, action);
        if (!blocked) void startMessage(target.id, action);
        return;
      }
      if (blocked) return;
      if (action.confirm === "yesno" || action.confirm === "form")
        setSheet({ target, action });
      else void run(target, action, {});
    },
  };

  const fleet = launch && fleets.value.find((f) => f.key === launch.fleet);
  const element = launch && fleet ? (
    <LaunchForm
      fleet={fleet}
      issues={[launch.url]}
      onClose={() => setLaunch(null)}
      onLaunched={(session) => go(shipPath(session), fleetSearch.value)}
    />
  ) : message ? (
    <MessageSheet
      action={message.action}
      text={message.text}
      busy={message.busy}
      onClose={() => setMessage(null)}
      onConfirm={() => void sendConfirmed()}
    />
  ) : sheet ? (
    sheet.action.confirm === "form" ? (
      <FormSheet
        sourceKey={sourceKey}
        target={sheet.target}
        action={sheet.action}
        onClose={() => setSheet(null)}
        onSubmit={(v) => (setSheet(null), void run(sheet.target, sheet.action, v))}
      />
    ) : (
      <ConfirmSheet
        action={sheet.action}
        onClose={() => setSheet(null)}
        onConfirm={() => (
          setSheet(null),
          void run(sheet.target, sheet.action, {})
        )}
      />
    )
  ) : null;
  return { controller, sheet: element };
}

/** A row's, step's or notice's actions: on a row Open link is a link, Launch and the AdmiralBot message are buttons; a module action runs in the module. A notice draws module actions only. */
export function ModuleActions({
  target,
  one,
  many,
}: {
  target: ActionTarget;
  one?: PageAction;
  many?: PageAction[];
}) {
  const ctl = useContext(Ctx);
  const all = [...(one ? [one] : []), ...(many ?? [])];
  const run = ctl?.runs[targetKey(target)];
  return all.length === 0 ? null : (
    <span class="mactions">
      {all.map((a) => {
        const kind = builtinOf(a);
        if (kind && target.notice) return null;
        if (kind === "open")
          return isWeb(a.url) ? (
            <a
              key={a.id}
              class="btn mact"
              href={a.url}
              target="_blank"
              rel="noopener noreferrer"
            >
              {a.label}
            </a>
          ) : null;
        const running = run?.phase === "running" && run.action === a.id;
        return (
          <button
            key={a.id}
            type="button"
            class="btn mact"
            disabled={
              !ctl ||
              (kind !== "launch" && ctl.blocked) ||
              !!run ||
              !!ctl.pending[target.id] ||
              !openableRow(target.id) ||
              !openableRow(a.id)
            }
            aria-busy={running || ctl?.pending[target.id] === a.id ? "true" : undefined}
            onClick={() => ctl?.press(target, a)}
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

/** The whole composed message as text, asked yes or no; the text shown is the text sent. */
function MessageSheet({
  action,
  text,
  busy,
  onClose,
  onConfirm,
}: {
  action: PageAction;
  text: string;
  busy: boolean;
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
      <p class="line mmessage">{text}</p>
      <Actions class="launch-foot">
        <Button label="Cancel" onClick={onClose} />
        <Button
          label={action.label}
          variant="primary"
          busy={busy}
          onClick={onConfirm}
        />
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
  target,
  action,
  onClose,
  onSubmit,
}: {
  sourceKey: string;
  target: ActionTarget;
  action: PageAction;
  onClose: () => void;
  onSubmit: (v: Values) => void;
}) {
  const [form, setForm] = useState<ModuleForm | null>(null);
  const [error, setError] = useState("");
  const [values, setValues] = useState<Values>({});
  useEffect(() => {
    let stale = false;
    void fetchModuleForm(sourceKey, target, action.id).then((r) => {
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
  }, [sourceKey, target.notice, target.id, action.id]);

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
