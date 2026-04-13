import "@testing-library/jest-dom/vitest";

// jsdom's <dialog> support is incomplete: showModal()/close() may be
// missing entirely, OR present but ineffective (not toggling the `open`
// attribute). The NewSandboxDialog relies on attribute visibility (tests
// query by role/text and showModal is called imperatively on mount), so
// we probe the real behavior and replace the methods when they don't
// actually work. In a real browser the native implementation passes the
// probe and we leave it alone.
if (typeof document !== "undefined" && typeof HTMLDialogElement !== "undefined") {
  const probe = document.createElement("dialog");
  const showModalWorks = (() => {
    if (typeof probe.showModal !== "function") return false;
    try {
      document.body.appendChild(probe);
      probe.showModal();
      const ok = probe.hasAttribute("open");
      probe.remove();
      return ok;
    } catch {
      probe.remove();
      return false;
    }
  })();
  if (!showModalWorks) {
    HTMLDialogElement.prototype.showModal = function showModal() {
      this.setAttribute("open", "");
    };
  }

  const closeProbe = document.createElement("dialog");
  const closeWorks = (() => {
    if (typeof closeProbe.close !== "function") return false;
    try {
      document.body.appendChild(closeProbe);
      closeProbe.setAttribute("open", "");
      closeProbe.close();
      const ok = !closeProbe.hasAttribute("open");
      closeProbe.remove();
      return ok;
    } catch {
      closeProbe.remove();
      return false;
    }
  })();
  if (!closeWorks) {
    HTMLDialogElement.prototype.close = function close() {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    };
  }
}

// Node 25 ships a built-in empty stub for globalThis.localStorage that
// shadows jsdom's Storage implementation — jsdom sees the global already
// exists and leaves it alone, so setItem/getItem/clear are undefined or
// no-ops inside tests. Probe the native implementation; if the round-trip
// works we leave it alone (preserving jsdom fidelity in older Node
// versions), otherwise we install a minimal in-memory Storage so every
// test file can exercise the real API surface and Storage.prototype
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
    return this.store.get(key) ?? null;
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

const nativeStorageWorks = (() => {
  try {
    const ls = (globalThis as { localStorage?: Storage }).localStorage;
    if (!ls || typeof ls.setItem !== "function" || typeof ls.getItem !== "function" || typeof ls.clear !== "function") {
      return false;
    }
    const probeKey = "__navaris_test_probe__";
    ls.setItem(probeKey, "1");
    const ok = ls.getItem(probeKey) === "1";
    ls.removeItem(probeKey);
    return ok;
  } catch {
    return false;
  }
})();

if (!nativeStorageWorks) {
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
}
