import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { StateBadge } from "./StateBadge";

describe("StateBadge", () => {
  it("renders the state label in lowercase", () => {
    render(<StateBadge state="running" />);
    expect(screen.getByText("running")).toBeInTheDocument();
  });

  it("uses distinct colour classes per state", () => {
    const { rerender } = render(<StateBadge state="running" />);
    expect(screen.getByTestId("state-dot")).toHaveClass("bg-[var(--status-running)]");
    rerender(<StateBadge state="stopped" />);
    expect(screen.getByTestId("state-dot")).toHaveClass("bg-[var(--status-stopped)]");
    rerender(<StateBadge state="failed" />);
    expect(screen.getByTestId("state-dot")).toHaveClass("bg-[var(--status-failed)]");
    rerender(<StateBadge state="pending" />);
    expect(screen.getByTestId("state-dot")).toHaveClass("bg-[var(--status-pending)]");
  });
});
