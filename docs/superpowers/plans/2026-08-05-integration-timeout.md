# Standard Integration Timeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow the standard Alpine/Debian integration matrix enough time to complete, then squash-merge PR #22 only after all checks are green.

**Architecture:** Change only the job-level timeout in `.github/workflows/integration.yml` from 15 to 30 minutes. Preserve the workflow trigger, matrix, command, application code, and every other workflow unchanged.

**Tech Stack:** GitHub Actions YAML, GitHub CLI.

## Global Constraints

- Work only on `fix/fc-concurrency-batch1` in `/home/eran/work/navaris/.worktrees/fc-concurrency-batch1`.
- Modify exactly one functional workflow line: `.github/workflows/integration.yml` `timeout-minutes: 15` → `timeout-minutes: 30`.
- Do not change tests, matrix images, commands, application code, APIs, schema, or other workflows.
- Push normally; never force-push.
- Squash-merge PR #22 only when every check has completed successfully and GitHub reports the PR mergeable.

---

### Task 1: Raise the standard integration timeout

**Files:**
- Modify: `.github/workflows/integration.yml:11`

**Interfaces:**
- Consumes: existing `integration` matrix job and `make integration-test` command.
- Produces: the same job with a 30-minute maximum runtime.

- [ ] **Step 1: Record the failing configuration assertion**

Run:

```bash
grep -q '^    timeout-minutes: 30$' .github/workflows/integration.yml
```

Expected: exit 1 because the current value is 15.

- [ ] **Step 2: Make the one-line change**

Replace:

```yaml
    timeout-minutes: 15
```

with:

```yaml
    timeout-minutes: 30
```

- [ ] **Step 3: Verify exact scope**

Run:

```bash
grep -q '^    timeout-minutes: 30$' .github/workflows/integration.yml
git diff --check
git diff -- .github/workflows/integration.yml
git status --short
```

Expected: the grep and diff check exit 0; the workflow diff contains exactly the one timeout replacement; status contains only `.github/workflows/integration.yml`.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/integration.yml
git commit -m "ci: allow standard integration tests 30 minutes"
```

---

### Task 2: Push, require green CI, and squash-merge

**Files:**
- No repository file changes.

**Interfaces:**
- Consumes: Task 1 commit and open PR #22.
- Produces: a green, squash-merged PR and updated `main`.

- [ ] **Step 1: Push normally**

```bash
git push origin fix/fc-concurrency-batch1
```

Expected: push succeeds without force.

- [ ] **Step 2: Confirm PR head and mergeability**

```bash
gh pr view 22 --json headRefOid,mergeable,mergeStateStatus,state,url
```

Expected: PR is open, references the new branch HEAD, and has no merge conflict.

- [ ] **Step 3: Watch every check**

```bash
gh pr checks 22 --watch --interval 15
```

Expected: every check completes with `pass`. If any check fails, is cancelled, or remains pending, stop and investigate; do not merge.

- [ ] **Step 4: Squash-merge and delete the remote branch**

```bash
gh pr merge 22 --squash --delete-branch
```

Expected: GitHub reports PR #22 merged.

- [ ] **Step 5: Verify the merge**

```bash
gh pr view 22 --json state,mergedAt,mergeCommit,url
git fetch origin main
git log -1 --oneline origin/main
```

Expected: PR state is `MERGED`, `mergedAt` and `mergeCommit` are present, and `origin/main` points to the squash commit.
