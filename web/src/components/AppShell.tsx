import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { ThemeToggle } from "./ThemeToggle";
import { StatusLine } from "./StatusLine";

// AppShell provides the persistent sidebar + status-line chrome around all
// authenticated routes. The grid keeps the 180px sidebar, flexible main
// column, and 26px status strip in stable positions as content changes.
export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div
      className="min-h-screen grid bg-[var(--bg-base)] text-[var(--fg-primary)]"
      style={{
        gridTemplateColumns: "180px 1fr",
        gridTemplateRows: "1fr 26px",
      }}
    >
      <aside className="row-span-1 col-start-1 border-r border-[var(--border-subtle)] pt-5 pb-3 flex flex-col">
        <div className="px-5 pb-5 border-b border-[var(--border-subtle)] mb-3">
          <div className="text-[15px] font-semibold tracking-[0.16em]">NAVARIS</div>
          <div className="mt-0.5 font-mono text-[9px] tracking-[0.08em] text-[var(--fg-muted)]">
            control plane
          </div>
        </div>

        <NavSection label="Workloads">
          <NavRow to="/projects">Projects</NavRow>
          <NavRow to="/sandboxes">Sandboxes</NavRow>
        </NavSection>

        <NavSection label="Observability">
          <NavRow to="/events">Events</NavRow>
        </NavSection>

        <div className="mt-auto px-5 pt-3 border-t border-[var(--border-subtle)] flex items-center gap-2 text-[11px] text-[var(--fg-muted)]">
          <span>navaris</span>
          <div className="ml-auto">
            <ThemeToggle />
          </div>
        </div>
      </aside>

      <main className="col-start-2 row-start-1 overflow-auto px-7 py-6">{children}</main>

      <div className="col-span-2 row-start-2">
        <StatusLine />
      </div>
    </div>
  );
}

function NavSection({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="py-2">
      <div className="px-5 pb-1.5 font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
        {label}
      </div>
      {children}
    </div>
  );
}

function NavRow({ to, children }: { to: string; children: ReactNode }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        [
          "px-5 py-1.5 text-[13px] flex items-center justify-between transition-colors",
          isActive
            ? "text-[var(--fg-primary)] bg-[var(--bg-overlay)] shadow-[inset_2px_0_0_var(--fg-primary)]"
            : "text-[var(--fg-secondary)] hover:bg-[var(--bg-overlay)] hover:text-[var(--fg-primary)]",
        ].join(" ")
      }
    >
      {children}
    </NavLink>
  );
}
