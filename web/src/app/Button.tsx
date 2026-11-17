import type { ComponentChildren } from 'preact';
import { createContext } from 'preact';
import { useContext, useEffect, useId, useMemo } from 'preact/hooks';
import { createActionGroup, isPending, runPending, type PendingKey } from './busy';
import { buttonModel, type ButtonModelProps } from './buttonModel';

type Group = ReturnType<typeof createActionGroup>;
const GroupContext = createContext<Group | null>(null);

export function Actions({ children, class: cls = 'actions' }: { children: ComponentChildren; class?: string }) {
  const group = useMemo(createActionGroup, []);
  return (
    <div class={cls}>
      <GroupContext.Provider value={group}>{children}</GroupContext.Provider>
    </div>
  );
}

export interface ButtonProps extends Omit<ButtonModelProps, 'busy'> {
  busy?: boolean;
  /** What the request acts on and the action; with it, busy survives the button unmounting. */
  pending?: PendingKey;
  onClick?: (e: MouseEvent) => unknown;
  children?: ComponentChildren;
}

// useId repeats across render roots, and a pending key outlives its button by the spinner's minimum.
let mounted = 0;

export function Button({ pending, onClick, children, ...props }: ButtonProps) {
  const group = useContext(GroupContext);
  const id = useId();
  const localKey = useMemo<PendingKey>(() => ['local', id, String(++mounted)], [id]);
  const key = pending ?? localKey;
  const busy = !!props.busy || isPending(key);
  const m = buttonModel({ ...props, text: typeof children === 'string' ? children : undefined, busy, disabled: props.disabled || (group?.disabledFor(id) ?? false) });

  useEffect(() => {
    if (!group) return;
    group.setBusy(id, busy);
    return () => group.setBusy(id, false);
  }, [busy]);

  const tap = (e: MouseEvent) => {
    if (m.ignoreTap) {
      e.preventDefault();
      return;
    }
    void runPending(key, () => onClick?.(e));
  };

  const Tag = m.tag as 'button';
  return (
    <Tag
      id={m.id}
      class={m.class}
      type={m.type}
      {...(m.href !== undefined ? { href: m.href, target: m.target, rel: m.rel } : {})}
      aria-label={m.ariaLabel}
      aria-busy={m.ariaBusy}
      aria-pressed={m.ariaPressed}
      aria-expanded={m.ariaExpanded}
      disabled={m.disabled}
      onBlur={m.onBlur}
      onClick={m.href !== undefined ? onClick : tap}
    >
      <span class="label">{props.icon ? children : (children ?? props.label)}</span>
      <span class="spinner" aria-hidden="true" />
    </Tag>
  );
}
