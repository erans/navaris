import { useTheme } from "next-themes";
import { useEffect, useState } from "react";

// ThemeToggle is a minimal monospace button that flips dark ⇄ light.
// The text content is rendered empty during SSR/hydration to avoid a mismatch.
export function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
  }, []);

  if (!mounted) {
    // Render a placeholder with the same dimensions so layout doesn't shift.
    return <span className="font-mono text-[10px] px-1.5 py-0.5 border opacity-0">◐ dark</span>;
  }

  const next = resolvedTheme === "dark" ? "light" : "dark";
  const label = resolvedTheme === "dark" ? "◐ dark" : "◑ light";

  return (
    <button
      type="button"
      onClick={() => setTheme(next)}
      className="font-mono text-[10px] px-1.5 py-0.5 border border-border-subtle text-fg-secondary hover:text-fg-primary transition-colors duration-150"
      aria-label={`Switch to ${next} theme`}
    >
      {label}
    </button>
  );
}
