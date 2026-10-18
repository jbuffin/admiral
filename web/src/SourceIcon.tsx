import { type Source, sourceColor, sources } from './sources';

// The plugin's icon when it has one, else the source name's initial on its colour.
export function SourceIcon({ source, size }: { source: Source; size: number }) {
  if (source.icon_url) return <img class="source-icon" src={source.icon_url} alt="" width={size} height={size} />;
  const { bg, ink } = sourceColor(sources.value ?? [], source);
  return (
    <span class="source-dot" aria-hidden="true" style={{ width: size, height: size, backgroundColor: bg, color: ink, fontSize: size * 0.55 }}>
      {source.name.charAt(0).toUpperCase()}
    </span>
  );
}
