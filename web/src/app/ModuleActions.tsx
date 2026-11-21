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
import { durationLabel, MAX_DURATION, parseDuration, toHHMM } from "./moduleView";
import { go, shipPath } from "./router";
import { Sheet, useCancelFocus, useListKeys } from "./Sheet";
import { isWebUrl, showResult } from "./ToastRegion";
import { useOutsidePress } from "./useOutsidePress";

type Values = Record<string, string | string[]>;
type Run = {
  phase: "running" | "updating";
  action: string;
  version?: number;
  token: number;
};

/** How long a row or notice waits for the page to change after a run before its module actions come back. */
export const UPDATING_FALLBACK_MS = 30_000;

/** The action id a built-in action's first request is out for, keyed by `targetKey` like `runs`. */
type Pending = Record<string, string>;

/** What an action does: a bare url is Open link, and a module action has no builtin. */
export const builtinOf = (a: PageAction) => a.builtin ?? (a.url ? "open" : undefined);
/** Open link and Launch work while the page is in error; the message and module actions don't. */
const enabledFor = (kind: string | undefined, blocked: boolean) =>
  kind === "open" || kind === "launch" || !blocked;

interface Controller {
  runs: Record<string, Run>;
  pending: Pending;
  /** True while the page is in error: no module action runs. */
  blocked: boolean;
  press: (target: ActionTarget, action: PageAction) => void;
}

/** Whether a group's actions of one kind are off for a target, before any one action's own id is checked. */
const groupOff = (ctl: Controller | null, target: ActionTarget, kind?: string) =>
  !ctl ||
  !enabledFor(kind, ctl.blocked) ||
  !!ctl.runs[targetKey(target)] ||
  !!ctl.pending[targetKey(target)] ||
  !openableRow(target.id);

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
    /** Names this sheet: a changed text opens a new one, so a tap meant for the old one can't land on it. */
    ask: number;
    changed: boolean;
  } | null>(null);
  const asks = useRef(0);
  const open = useRef(0);
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
      const { [targetKey({ id: row })]: _gone, ...rest } = p;
      return rest;
    });

  // Launch asks the server which fleet the issue routes to; a refusal shows its reason and opens nothing.
  const startLaunch = async (row: string, action: PageAction) => {
    setPending((p) => ({ ...p, [targetKey({ id: row })]: action.id }));
    const r = await fetchLaunchTarget(sourceKey, row, action.id);
    settle(row);
    if (!r.ok) return failed(action, row, r.error);
    if (!fleets.value.some((f) => f.key === r.data.fleet))
      return failed(action, row, `Fleet ${r.data.fleet} is not available.`);
    setLaunch(r.data);
  };

  const startMessage = async (row: string, action: PageAction) => {
    setPending((p) => ({ ...p, [targetKey({ id: row })]: action.id }));
    const r = await fetchMessagePreview(sourceKey, row, action.id);
    settle(row);
    if (!r.ok) return failed(action, row, r.error);
    const ask = ++asks.current;
    open.current = ask;
    setMessage({ row, action, text: r.data.text, chat: r.data.chat, busy: false, ask, changed: false });
  };
  const closeMessage = () => {
    open.current = 0;
    setMessage(null);
  };

  // The server posts only the text it composes now: when it refuses the text, ask again with the new one.
  const sendConfirmed = async () => {
    const m = message;
    if (!m || m.busy) return;
    setMessage({ ...m, busy: true });
    const r = await sendMessage(sourceKey, m.row, m.action.id, m.text);
    if (r.ok) {
      if (open.current === m.ask) closeMessage();
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
      // The engineer closed the sheet while the send was out: it stays closed.
      if (open.current !== m.ask) return;
      if (again.ok && again.data.text !== m.text) {
        const ask = ++asks.current;
        open.current = ask;
        return setMessage({ ...m, text: again.data.text, chat: again.data.chat, busy: false, ask, changed: true });
      }
    }
    if (open.current === m.ask) closeMessage();
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
      if (kind === "open" || pending[key] || runs[key]) return;
      if (!enabledFor(kind, blocked)) return;
      if (kind) {
        // Built-in actions belong to rows; a notice never draws them.
        if (target.notice) return;
        return void (kind === "launch" ? startLaunch : startMessage)(target.id, action);
      }
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
      key={message.ask}
      action={message.action}
      text={message.text}
      busy={message.busy}
      changed={message.changed}
      onClose={closeMessage}
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
  lead,
  small,
}: {
  target: ActionTarget;
  one?: PageAction;
  many?: PageAction[];
  /** The action drawn as the one to press: filled on a row, outlined in the accent when small. */
  lead?: string;
  small?: boolean;
}) {
  const ctl = useContext(Ctx);
  const all = [...(one ? [one] : []), ...(many ?? [])];
  const cls = (a: PageAction) => `btn mact${small ? " mact-small" : ""}${a.id === lead ? (small ? " lead" : " primary") : ""}`;
  const run = ctl?.runs[targetKey(target)];
  const inMenu = (a: PageAction) => !!a.menu && !builtinOf(a);
  const menu = all.filter(inMenu);
  return all.length === 0 ? null : (
    <span class="mactions">
      {all.filter((a) => !inMenu(a)).map((a) => {
        const kind = builtinOf(a);
        if (kind && target.notice) return null;
        if (kind === "open")
          return isWebUrl(a.url) ? (
            <a
              key={a.id}
              class={cls(a)}
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
            class={cls(a)}
            disabled={groupOff(ctl, target, kind) || !openableRow(a.id)}
            aria-busy={running || ctl?.pending[targetKey(target)] === a.id ? "true" : undefined}
            onClick={() => ctl?.press(target, a)}
          >
            {a.label}
            {running ? <span class="spinner" aria-hidden="true" /> : null}
          </button>
        );
      })}
      {menu.length > 0 ? <ActionMenu target={target} items={menu} small={small} /> : null}
    </span>
  );
}

/** The ⋯ menu of a group's menu module actions; choosing one acts like pressing its button. */
function ActionMenu({ target, items, small }: { target: ActionTarget; items: PageAction[]; small?: boolean }) {
  const ctl = useContext(Ctx);
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLSpanElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const run = ctl?.runs[targetKey(target)];
  const pending = ctl?.pending[targetKey(target)];
  const busy = (run?.phase === "running" && items.some((a) => a.id === run.action)) || items.some((a) => a.id === pending);
  const disabled = groupOff(ctl, target);
  const close = (refocus = true) => {
    setOpen(false);
    if (refocus) trigger.current?.focus();
  };
  useListKeys(panel, open, () => close());
  useOutsidePress(open, [wrap], () => close(false));
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  return (
    <span ref={wrap} class="mmenu">
      <button
        ref={trigger}
        type="button"
        class={`btn mact${small ? " mact-small" : ""}`}
        aria-label="More actions"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-busy={busy ? "true" : undefined}
        disabled={disabled}
        onClick={() => setOpen(!open)}
      >
        ⋯{busy ? <span class="spinner" aria-hidden="true" /> : null}
      </button>
      {open ? (
        <div ref={panel} class="menu mmenu-panel" role="menu" aria-label="More actions">
          {items.map((a) => (
            <button
              key={a.id}
              type="button"
              role="menuitem"
              class="menu-item"
              disabled={!openableRow(a.id)}
              onClick={() => (close(false), ctl?.press(target, a))}
            >
              <span class="menu-name">{a.label}</span>
            </button>
          ))}
        </div>
      ) : null}
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
  changed,
  onClose,
  onConfirm,
}: {
  action: PageAction;
  text: string;
  busy: boolean;
  changed: boolean;
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
      {changed ? (
        <p class="launch-note" role="status">
          The row changed; this is the new message.
        </p>
      ) : null}
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

interface Check {
  ok: boolean;
  /** Why a typed value can't be sent; none while the field is still empty. */
  note: string;
  minutes?: number;
}

function durationCheck(f: FormField, value: string): Check {
  if (!value.trim()) return { ok: false, note: "" };
  const n = parseDuration(value);
  if (n === null) return { ok: false, note: "Couldn't read that. Try 1:30, 90, 1h30 or 1.5." };
  const min = f.min ? parseDuration(f.min) : null;
  if (min !== null && n < min) return { ok: false, note: `At least ${durationLabel(min)}.` };
  if (n > MAX_DURATION) return { ok: false, note: `At most ${durationLabel(MAX_DURATION)}.` };
  return { ok: true, note: "", minutes: n };
}

const QUICK_PICKS: [string, number][] = [["15m", 15], ["30m", 30], ["1h", 60], ["1h 30m", 90], ["2h", 120]];

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

  const checks: Check[] = form
    ? form.fields.map((f) =>
        f.type === "duration"
          ? durationCheck(f, String(values[f.id] ?? ""))
          : { ok: true, note: "" },
      )
    : [];
  const ready = checks.every((c) => c.ok);
  // The plugin takes hh:mm whatever the engineer typed.
  const submit = () =>
    ready &&
    onSubmit(
      Object.fromEntries(
        Object.entries(values).map(([k, v]) => {
          const i = form?.fields.findIndex((f) => f.id === k) ?? -1;
          const n = i >= 0 ? checks[i].minutes : undefined;
          return [k, n === undefined ? v : toHHMM(n)];
        }),
      ),
    );
  const set = (id: string, v: string | string[]) =>
    setValues((cur) => ({ ...cur, [id]: v }));
  return (
    <Sheet
      label={form?.title || action.label}
      kind="launch-backdrop stop-backdrop"
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
            submit();
          }}
        >
          {form.fields.map((f, i) => (
            <Field
              key={f.id}
              field={f}
              value={values[f.id]}
              check={checks[i]}
              onChange={(v) => set(f.id, v)}
            />
          ))}
          <Actions class="launch-foot">
            <Button label="Cancel" onClick={onClose} />
            <Button
              label="Submit"
              variant="primary"
              disabled={!ready}
              onClick={submit}
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
  check,
  onChange,
}: {
  field: FormField;
  value: string | string[] | undefined;
  check: Check;
  onChange: (v: string | string[]) => void;
}) {
  const id = useId();
  if (field.type === "duration") {
    const min = field.min ? parseDuration(field.min) : null;
    return (
      <div class="mform-field">
        <label class="mform-label" for={id}>
          {field.label}
        </label>
        <div class="mform-duration">
          {/* Plain text, not numeric: an iPhone's number pad has no ":". */}
          <input
            id={id}
            type="text"
            autocomplete="off"
            spellcheck={false}
            placeholder="1:30, 90, 1h30, 1.5"
            aria-describedby={`${id}-note`}
            value={String(value ?? "")}
            onInput={(e) => onChange(e.currentTarget.value)}
          />
          <span class="mform-read" aria-live="polite">
            {check.minutes !== undefined ? `= ${durationLabel(check.minutes)}` : ""}
          </span>
        </div>
        <div class="mform-picks">
          {QUICK_PICKS.filter(([, n]) => min === null || n >= min).map(([label, n]) => (
            <button
              key={label}
              type="button"
              class="btn mact-small"
              aria-pressed={check.minutes === n}
              onClick={() => onChange(label)}
            >
              {label}
            </button>
          ))}
        </div>
        {check.note ? (
          <p id={`${id}-note`} class="mform-note">
            {check.note}
          </p>
        ) : (
          <p id={`${id}-note`} class="mform-hint">
            A plain number is minutes; a decimal is hours.
            {check.minutes !== undefined ? ` Saved as ${toHHMM(check.minutes)}.` : ""}
          </p>
        )}
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
