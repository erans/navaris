# Standard Integration Workflow Timeout Adjustment

**Status:** Approved in discussion
**Date:** 2026-08-05

## Problem

PR #22's `integration (debian/12)` job was cancelled twice by GitHub Actions at exactly 15 minutes. Both logs show passing tests and continued forward progress up to cancellation; neither contains a test assertion or service failure. Historical runs completed in roughly 11–13 minutes, but current runner/image provisioning and integration execution exceed the workflow's fixed 15-minute budget.

The other integration workflows already allow 20–30 minutes:

- Firecracker: 30 minutes
- Firecracker CoW: 30 minutes
- Mixed provider: 30 minutes
- Incus CoW: 20 minutes

## Considered approaches

1. **Raise only `.github/workflows/integration.yml` to 30 minutes — selected.** This gives the standard Alpine/Debian matrix the same headroom as the Firecracker and mixed-provider suites without changing test behavior.
2. Raise it to 20 minutes. This is smaller but leaves little margin after two runs exceeded 15 minutes while still several tests from completion.
3. Use image-specific timeout logic or split jobs. This adds workflow complexity without evidence that separate policies are needed.

## Design

Change the standard integration job's `timeout-minutes` from `15` to `30`. Do not alter commands, matrix images, test selection, application code, or any other workflow.

Push the change to PR #22 and require every PR check to complete successfully. Squash-merge only after GitHub reports all checks green and the PR remains mergeable.

## Verification

- Inspect the workflow diff: exactly one functional line changes.
- Validate the YAML through GitHub Actions by pushing the branch.
- Watch all checks to completion.
- Do not merge on cancellation, failure, pending status, or merge conflict.

## Compatibility and rollback

This changes only the maximum CI runtime allowance; tests that finish sooner are unaffected. If future optimization makes the extra budget unnecessary, the timeout can be reduced independently.
