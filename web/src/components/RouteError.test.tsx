import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RouteError } from "./RouteError";

// Mock react-router-dom's useRouteError and useNavigate so we can drive
// RouteError deterministically without spinning up a full data router.
// jsdom's undici polyfill chokes on React Router v7's AbortSignal inside
// createMemoryRouter, so a hook-level mock is both simpler and more robust.
const navigateMock = vi.fn();
let mockedRouteError: unknown = null;

vi.mock("react-router-dom", () => ({
  useRouteError: () => mockedRouteError,
  useNavigate: () => navigateMock,
}));

function renderWith(error: unknown) {
  mockedRouteError = error;
  return render(<RouteError />);
}

describe("RouteError", () => {
  const originalConsoleError = console.error;
  afterEach(() => {
    console.error = originalConsoleError;
    navigateMock.mockReset();
    mockedRouteError = null;
  });

  it("renders the error message from a thrown Error", () => {
    renderWith(new Error("page explosion"));
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText("This screen failed to load")).toBeInTheDocument();
    expect(screen.getByText("page explosion")).toBeInTheDocument();
  });

  it("handles a string thrown from a route", () => {
    renderWith("raw string failure");
    expect(screen.getByText("raw string failure")).toBeInTheDocument();
  });

  it("handles an unknown thrown value with a fallback message", () => {
    renderWith(42);
    expect(screen.getByText("unknown error")).toBeInTheDocument();
  });

  it("reads statusText when the thrown value is a Response-like object", () => {
    renderWith({ status: 404, statusText: "not found" });
    expect(screen.getByText("not found")).toBeInTheDocument();
  });

  it("reads .message from a plain object without losing type safety", () => {
    renderWith({ message: "custom object failure" });
    expect(screen.getByText("custom object failure")).toBeInTheDocument();
  });

  it("navigates to /sandboxes when the back button is clicked", async () => {
    const user = userEvent.setup();
    renderWith(new Error("nav test"));
    await user.click(screen.getByRole("button", { name: /back to sandboxes/i }));
    expect(navigateMock).toHaveBeenCalledWith("/sandboxes");
  });
});
