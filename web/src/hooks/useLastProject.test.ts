import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useLastProject } from "./useLastProject";

describe("useLastProject", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("returns null when no id has been written", () => {
    const { result } = renderHook(() => useLastProject());
    expect(result.current.readLastProject()).toBeNull();
  });

  it("round-trips a project id through localStorage", () => {
    const { result } = renderHook(() => useLastProject());
    act(() => result.current.writeLastProject("prj_42"));
    expect(result.current.readLastProject()).toBe("prj_42");
  });

  it("uses the storage key 'navaris.lastProjectId'", () => {
    const { result } = renderHook(() => useLastProject());
    act(() => result.current.writeLastProject("prj_99"));
    expect(localStorage.getItem("navaris.lastProjectId")).toBe("prj_99");
  });

  it("swallows thrown errors from localStorage.getItem (private mode)", () => {
    const spy = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("SecurityError: private mode");
      });
    const { result } = renderHook(() => useLastProject());
    expect(result.current.readLastProject()).toBeNull();
    expect(spy).toHaveBeenCalled();
  });

  it("swallows thrown errors from localStorage.setItem (private mode)", () => {
    const spy = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });
    const { result } = renderHook(() => useLastProject());
    expect(() => {
      act(() => result.current.writeLastProject("prj_1"));
    }).not.toThrow();
    expect(spy).toHaveBeenCalled();
  });
});
