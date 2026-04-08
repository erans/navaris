import {
  describe,
  it,
  expect,
  beforeAll,
  beforeEach,
  afterEach,
  vi,
} from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useLastProject } from "./useLastProject";

// Node 25 ships a built-in (empty stub) `globalThis.localStorage` that
// shadows jsdom's Storage implementation — jsdom sees the global already
// exists and leaves it alone, so `localStorage.setItem` and friends are
// undefined inside tests. Install a minimal in-memory Storage before any
// tests run so the hook can exercise the real API surface and
// `Storage.prototype` spies resolve against a genuine Storage class.
class TestStorage {
  private store = new Map<string, string>();
  get length(): number {
    return this.store.size;
  }
  key(index: number): string | null {
    return Array.from(this.store.keys())[index] ?? null;
  }
  getItem(key: string): string | null {
    return this.store.has(key) ? (this.store.get(key) as string) : null;
  }
  setItem(key: string, value: string): void {
    this.store.set(key, String(value));
  }
  removeItem(key: string): void {
    this.store.delete(key);
  }
  clear(): void {
    this.store.clear();
  }
}

beforeAll(() => {
  const storage = new TestStorage();
  Object.defineProperty(globalThis, "localStorage", {
    value: storage,
    writable: true,
    configurable: true,
  });
  // The error-swallowing tests spy on `Storage.prototype`, so `Storage`
  // must resolve to the same class our installed instance inherits from.
  Object.defineProperty(globalThis, "Storage", {
    value: TestStorage,
    writable: true,
    configurable: true,
  });
});

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
