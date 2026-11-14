/** The step reviewer's answer as an event card or an event proposal's detail carries it. */
export interface ReviewerFields {
  verdict?: string;
  check_id?: string;
  check?: string;
  reviewer_reason?: string;
}

export interface ReviewerNote {
  heading: string;
  check: string;
  checkId: string;
  reason: string;
  /** The label before the reason: the reviewer only speaks when it judged a Check. */
  reasonLabel: 'Reviewer' | 'Why';
}

/** A block or an ask has a note; an allow, or an event the reviewer was not asked about, has none. The text is untrusted: render it as text. */
export function reviewerNote(r: ReviewerFields): ReviewerNote | null {
  if (r.verdict !== 'block' && r.verdict !== 'ask') return null;
  const id = r.check_id ?? '';
  const verb = r.verdict === 'block' ? 'Blocked by' : 'Asked about';
  const unjudged = r.verdict === 'ask' && !id; // spend reached, unreadable Checks and failed calls never reached the judge
  return {
    heading: id ? `${verb} Check ${id}` : unjudged ? 'Held for your approval' : 'Blocked by the reviewer',
    check: r.check ?? '',
    checkId: id,
    reason: r.reviewer_reason ?? '',
    reasonLabel: unjudged ? 'Why' : 'Reviewer',
  };
}
