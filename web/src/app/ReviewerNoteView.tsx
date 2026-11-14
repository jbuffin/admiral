import { reviewerNote, type ReviewerFields } from './reviewerNote';

/** The reviewer's verdict on an event, as plain text: its Check and reason are model text over outside text. */
export function ReviewerNoteView({ r }: { r: ReviewerFields }) {
  const n = reviewerNote(r);
  if (!n) return null;
  return (
    <div class="line">
      <p class="line">
        <strong>{n.heading}</strong>
      </p>
      {n.check ? <p class="line">Check: {n.check}</p> : null}
      {n.reason ? <p class="line">{n.reasonLabel}: {n.reason}</p> : null}
    </div>
  );
}
