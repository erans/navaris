# Manual Terminal Smoke Test

Used during development when you need to sanity-check the end-to-end terminal
attach flow before committing UI changes. The unit test in
`src/routes/SandboxDetail.test.tsx` only covers the link gating — xterm.js
needs a real DOM with layout, so the interactive path can only be exercised
against a live backend.

## Prerequisites

- `navarisd` built with the `withui` and `incus` build tags — the first
  embeds the SPA assets into the binary, the second enables the Incus
  provider:
  ```
  make web-build   # bundles web/dist into internal/webui/dist (Task 37)
  go build -tags "withui incus" -o navarisd ./cmd/navarisd
  ```
- `--ui-password` set to a value you know
- At least one running Incus sandbox

## Procedure

1. Start navarisd:
   ```
   ./navarisd --ui-password "dev" --incus-socket /var/lib/incus/unix.socket
   ```
2. Open <http://localhost:8080/ui/> in a browser.
3. Log in with `dev`.
4. Navigate to Sandboxes → click a running sandbox → click **Terminal**.
5. Verify you see a shell prompt. Type `ls /` and confirm the output appears.
6. Resize the browser window — the terminal should reflow without garbling,
   and the PTY inside the sandbox should see the new dimensions (try `stty
   size` to confirm).
7. Type `exit`. The websocket should close cleanly and the header should
   show `ws · closed`.

## Expected failures (known)

- Firecracker sandboxes currently have no shell image wired up — the
  terminal will attach but there's nothing on the other end. Use Incus only
  for v1.
- Closing the browser tab mid-session leaves the PTY alive on the host until
  the next backend heartbeat detects the dead socket. This is a known gap
  tracked separately.
- The terminal theme is hardcoded dark. Toggling the UI theme to light does
  not recolour the xterm canvas — addressed in a future task.

## Reload & resilience smoke

1. **Reload preserves tabs + active tab**
   - Open 3 tabs. Click Session 2 so it's active.
   - Reload the page.
   - Verify: all 3 tabs present; Session 2 is the active tab.

2. **Auto-reconnect on server blip**
   - Attach to a session, run `while true; do date; sleep 1; done`.
   - Stop `navarisd` for 10 seconds, then restart it.
   - Verify: panel shows a small "Reconnecting…" pill during the outage; resumes streaming once `navarisd` is back; tab bar shows a yellow dot while reconnecting.

3. **Exited session UI**
   - Open 2 tabs. In Session 1, type `exit` to terminate the shell.
   - Verify: Session 1's tab becomes greyed with a line-through; the panel shows "Session ended"; Session 2 is unaffected.
   - Click Session 1's × to remove the tab.
   - Verify: only Session 2 remains.
