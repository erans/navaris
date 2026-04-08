interface Props {
  label: string;
}

export function Placeholder({ label }: Props) {
  return (
    <div className="p-6 font-mono text-xs uppercase tracking-widest text-fg-muted">
      {label}
    </div>
  );
}
