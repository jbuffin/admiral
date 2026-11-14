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
}

export function reviewerNote(_r: ReviewerFields): ReviewerNote | null {
  return null;
}
