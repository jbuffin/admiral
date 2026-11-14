export type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'quiet' | 'link';

export interface ButtonModelProps {
  label: string;
  variant?: ButtonVariant;
  icon?: boolean;
  round?: boolean;
  class?: string;
  busy?: boolean;
  busyLabel?: string;
  disabled?: boolean;
  pressed?: boolean;
  href?: string;
  external?: boolean;
  type?: 'button' | 'submit';
}

export function buttonModel(p: ButtonModelProps) {
  const link = p.href !== undefined;
  const busy = !!p.busy && !link;
  const classes = ['btn'];
  if (p.variant && p.variant !== 'secondary') classes.push(p.variant);
  if (p.icon) classes.push('icon');
  if (p.round) classes.push('round');
  if (p.class) classes.push(p.class);
  return {
    tag: (link ? 'a' : 'button') as 'a' | 'button',
    class: classes.join(' '),
    type: link ? undefined : (p.type ?? 'button'),
    href: p.href,
    target: link && p.external ? '_blank' : undefined,
    rel: link && p.external ? 'noopener noreferrer' : undefined,
    name: busy && p.busyLabel ? p.busyLabel : p.label,
    ariaBusy: busy ? ('true' as const) : undefined,
    ariaPressed: p.pressed === undefined ? undefined : p.pressed ? ('true' as const) : ('false' as const),
    disabled: !link && !busy && !!p.disabled,
    ignoreTap: busy,
  };
}
