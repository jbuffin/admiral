/** An ISO time as the engineer's local "Nov 11, 09:30", or '' when it is missing or unparseable. */
export const when = (iso: string | undefined) => {
  const t = iso ? Date.parse(iso) : NaN;
  return Number.isNaN(t) ? '' : new Date(t).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
};
