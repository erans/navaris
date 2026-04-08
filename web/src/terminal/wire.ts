// wire.ts is the single source of truth for the terminal WebSocket's frame
// format on the client side. It mirrors the Go side in internal/api/attach.go
// (see the bridgeAttach function). There is no framing header — the WS
// protocol itself distinguishes binary frames (raw data) from text frames
// (JSON control messages).

// encodeInputBytes converts a keystroke string into the raw byte sequence
// that should be written verbatim to the sandbox's stdin. xterm's onData
// gives us the string form of the keystroke (already with escape sequences
// for function/arrow keys), so we just UTF-8 encode it and ship it.
export function encodeInputBytes(data: string): Uint8Array {
  return new TextEncoder().encode(data);
}

// ResizeMessage is the single text-frame message the client sends.
export interface ResizeMessage {
  type: "resize";
  cols: number;
  rows: number;
}

// encodeResizeMessage produces the JSON text payload for a resize event.
// Values are rounded to the nearest integer — xterm.js can report
// fractional sizes mid-animation, but the Go side expects int cols/rows.
export function encodeResizeMessage(cols: number, rows: number): string {
  const msg: ResizeMessage = {
    type: "resize",
    cols: Math.round(cols),
    rows: Math.round(rows),
  };
  return JSON.stringify(msg);
}
