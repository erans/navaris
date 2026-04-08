import "@testing-library/jest-dom/vitest";

// jsdom's support for <dialog> is incomplete — showModal()/close() may be
// missing or may not toggle the `open` attribute. The NewSandboxDialog
// relies on the attribute being observable (tests assert visibility via
// DOM queries, and showModal is called imperatively on mount). Stub both
// methods so the element behaves close enough to the browser for tests.
// In a real browser these prototype properties already exist, so the
// `if (!...)` guards make the shim a no-op at runtime.
if (typeof HTMLDialogElement !== "undefined") {
  if (!HTMLDialogElement.prototype.showModal) {
    HTMLDialogElement.prototype.showModal = function showModal() {
      this.setAttribute("open", "");
    };
  }
  if (!HTMLDialogElement.prototype.close) {
    HTMLDialogElement.prototype.close = function close() {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    };
  }
}

// Node 25 ships a built-in (empty stub) globalThis.localStorage that
// shadows jsdom's Storage implementation — jsdom sees the global already
// exists and leaves it alone, so `localStorage.setItem` and friends are
// undefined inside tests. Install a minimal in-memory Storage so every
// test file can exercise the real API surface and `Storage.prototype`
// spies resolve against a genuine Storage class.
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

Object.defineProperty(globalThis, "localStorage", {
  value: new TestStorage(),
  writable: true,
  configurable: true,
});
Object.defineProperty(globalThis, "Storage", {
  value: TestStorage,
  writable: true,
  configurable: true,
});
