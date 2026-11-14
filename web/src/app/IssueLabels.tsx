/** An issue's GitHub labels, as GitHub names them, so the person can see why it starts the way it does. */
export function IssueLabels({ labels }: { labels?: string[] }) {
  if (!labels?.length) return null;
  return (
    <ul class="issue-labels" aria-label="GitHub labels">
      {labels.map((l) => (
        <li key={l} class="issue-label mono">
          {l}
        </li>
      ))}
    </ul>
  );
}
