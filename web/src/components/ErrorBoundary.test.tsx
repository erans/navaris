import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ErrorBoundary } from "./ErrorBoundary";

// Helper component that throws when `shouldThrow` is true.
function Boom({ shouldThrow, message = "kaboom" }: { shouldThrow: boolean; message?: string }) {
  if (shouldThrow) {
    throw new Error(message);
  }
  return <div>safe content</div>;
}

describe("ErrorBoundary", () => {
  // React logs caught errors to console.error; silence those logs for the
  // duration of these tests so the vitest output stays clean.
  const originalConsoleError = console.error;
  afterEach(() => {
    console.error = originalConsoleError;
  });

  it("renders children when no error is thrown", () => {
    render(
      <ErrorBoundary>
        <Boom shouldThrow={false} />
      </ErrorBoundary>,
    );
    expect(screen.getByText("safe content")).toBeInTheDocument();
  });

  it("renders the default fallback when a child throws", () => {
    console.error = vi.fn();
    render(
      <ErrorBoundary>
        <Boom shouldThrow message="synthetic failure" />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText("Something went wrong")).toBeInTheDocument();
    expect(screen.getByText("synthetic failure")).toBeInTheDocument();
  });

  it("logs caught errors to console.error", () => {
    const spy = vi.fn();
    console.error = spy;
    render(
      <ErrorBoundary>
        <Boom shouldThrow message="logged failure" />
      </ErrorBoundary>,
    );
    // componentDidCatch fires at least one console.error with our label.
    const labelled = spy.mock.calls.find(
      (args) => typeof args[0] === "string" && args[0].includes("ErrorBoundary caught"),
    );
    expect(labelled).toBeDefined();
  });

  it("clears the error state and renders new children when reset is pressed", async () => {
    console.error = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <ErrorBoundary>
        <Boom shouldThrow />
      </ErrorBoundary>,
    );
    // Initial throw should land on the fallback.
    expect(screen.getByText("Something went wrong")).toBeInTheDocument();

    // Swap children so the next render will not throw, then click reset.
    // The boundary holds onto its error state until reset is explicitly
    // called, so we stay on the fallback through the rerender.
    rerender(
      <ErrorBoundary>
        <Boom shouldThrow={false} />
      </ErrorBoundary>,
    );
    expect(screen.getByText("Something went wrong")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /try again/i }));
    expect(screen.getByText("safe content")).toBeInTheDocument();
  });

  it("uses a custom fallback when provided", () => {
    console.error = vi.fn();
    render(
      <ErrorBoundary fallback={(err) => <div>custom: {err.message}</div>}>
        <Boom shouldThrow message="custom-path" />
      </ErrorBoundary>,
    );
    expect(screen.getByText("custom: custom-path")).toBeInTheDocument();
  });
});
