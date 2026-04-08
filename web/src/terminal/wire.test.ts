import { describe, it, expect } from "vitest";
import { encodeInputBytes, encodeResizeMessage } from "./wire";

describe("terminal wire protocol", () => {
  it("encodeInputBytes returns the raw UTF-8 bytes of the input string", () => {
    const bytes = encodeInputBytes("hello");
    expect(bytes).toBeInstanceOf(Uint8Array);
    expect(new TextDecoder().decode(bytes)).toBe("hello");
  });

  it("encodeInputBytes handles multi-byte UTF-8 input", () => {
    const bytes = encodeInputBytes("héllo");
    // "héllo" is 6 bytes in UTF-8 (é = 2 bytes).
    expect(bytes.byteLength).toBe(6);
  });

  it("encodeResizeMessage emits the spec'd JSON shape", () => {
    const text = encodeResizeMessage(132, 40);
    const parsed = JSON.parse(text);
    expect(parsed).toEqual({ type: "resize", cols: 132, rows: 40 });
  });

  it("encodeResizeMessage rounds non-integer values defensively", () => {
    // xterm.js sometimes reports fractional cols/rows during animations;
    // the Go backend expects integers.
    const text = encodeResizeMessage(80.7, 24.2);
    const parsed = JSON.parse(text);
    expect(parsed.cols).toBe(81);
    expect(parsed.rows).toBe(24);
  });
});
