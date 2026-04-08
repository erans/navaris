import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// Regression guard for the broken status palette bug:
// every "Confirm delete", StateBadge dot, failed-row accent, error
// banner border, and StatusLine indicator reaches for a CSS variable
// via arbitrary utility classes like `bg-[var(--status-failed)]`.
// The @theme block only defines `--color-status-*` (Tailwind v4
// convention), so without the short-name aliases below, all of those
// usages silently resolve to the empty string and render as
// transparent-on-default. This test re-reads the source stylesheet
// and fails if any of the short aliases disappear.
const css = readFileSync(
  resolve(__dirname, "./index.css"),
  "utf8",
);

const SHORT_ALIASES = [
  "--status-running",
  "--status-pending",
  "--status-stopped",
  "--status-failed",
  "--status-destroyed",
] as const;

describe("index.css status palette", () => {
  it.each(SHORT_ALIASES)(
    "defines the %s short alias at :root",
    (name) => {
      // The alias should appear as a declaration at :root, not only
      // inside a var() reference. A regex on the name + ':' catches
      // that without depending on the exact right-hand side (which
      // may be a literal or a var() indirection).
      const declaration = new RegExp(`${name}\\s*:`);
      expect(css).toMatch(declaration);
    },
  );

  it("keeps the underlying --color-status-* tokens intact", () => {
    // The short aliases point at these via var(--color-status-*),
    // so removing the longer names without also removing the short
    // aliases would re-break the app in a subtler way. Assert both.
    for (const name of [
      "--color-status-running",
      "--color-status-pending",
      "--color-status-stopped",
      "--color-status-failed",
      "--color-status-destroyed",
    ]) {
      const declaration = new RegExp(`${name}\\s*:\\s*#`);
      expect(css).toMatch(declaration);
    }
  });
});
