import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useLastActiveSession } from "./useLastActiveSession";

describe("useLastActiveSession", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("returns null when no id has been written", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    expect(result.current.read()).toBeNull();
  });

  it("round-trips a session id through localStorage", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    act(() => result.current.write("sess_42"));
    expect(result.current.read()).toBe("sess_42");
  });

  it("uses a per-sandbox storage key", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    act(() => result.current.write("sess_99"));
    expect(localStorage.getItem("navaris.terminal.sbx_1.activeSession")).toBe("sess_99");
  });

  it("isolates sandboxes from each other", () => {
    const { result: a } = renderHook(() => useLastActiveSession("sbx_a"));
    const { result: b } = renderHook(() => useLastActiveSession("sbx_b"));
    act(() => a.current.write("sess_a"));
    act(() => b.current.write("sess_b"));
    expect(a.current.read()).toBe("sess_a");
    expect(b.current.read()).toBe("sess_b");
  });

  it("clear() removes the entry", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    act(() => result.current.write("sess_1"));
    act(() => result.current.clear());
    expect(result.current.read()).toBeNull();
  });

  it("swallows getItem errors (private mode)", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    expect(result.current.read()).toBeNull();
  });

  it("swallows setItem errors (quota/private mode)", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    expect(() => act(() => result.current.write("sess_1"))).not.toThrow();
  });
});
