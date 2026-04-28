# Manual Docker Image Publish Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `workflow_dispatch`-triggered GitHub Actions workflow that builds the all-in-one navaris Docker image from an existing git tag and publishes it to Docker Hub as a multi-arch (amd64+arm64) manifest under `erans/navaris:<tag>` and `erans/navaris:latest`.

**Architecture:** Three jobs in one workflow file — `build-amd64` (native amd64 runner), `build-arm64` (native arm64 runner), and `manifest` (depends on both, fuses the per-arch images into multi-arch manifest lists). Native runners avoid QEMU emulation cost on the kernel build inside the Dockerfile.

**Tech Stack:** GitHub Actions, `docker/login-action@v3`, `docker/setup-buildx-action@v3`, `docker/build-push-action@v6`, `docker buildx imagetools` (manifest fusion).

**Spec:** [docs/superpowers/specs/2026-04-28-manual-docker-image-publish-design.md](../specs/2026-04-28-manual-docker-image-publish-design.md)

---

## File Plan

### Created
- `.github/workflows/docker-image.yml` — single workflow file with three jobs.

### Modified
None.

---

## Conventions

- All work on the existing worktree branch `worktree-docker-image-workflow` (already created).
- Final commit only: workflow YAML files don't have a build/test cycle to commit incrementally against. The plan groups related changes into one logical commit at the end.
- Match existing workflow style (see `.github/workflows/release.yml` for the closest analog: tag-driven, gh CLI usage, `concurrency:` block).

---

## Task 1: Author the workflow file

**Files:**
- Create: `.github/workflows/docker-image.yml`

This is the entire feature — one self-contained YAML file. We write it in one shot, then validate it via local linting + dry-run inspection (no actual run requires Docker Hub credentials in the repo).

- [ ] **Step 1: Create the workflow file**

Create `.github/workflows/docker-image.yml` with the following exact content:

```yaml
name: Publish Docker image

on:
  workflow_dispatch:
    inputs:
      tag:
        description: 'Existing git tag to build from (e.g. v0.1.0). Image will be pushed as erans/navaris:<tag> and erans/navaris:latest.'
        type: string
        required: true

permissions:
  contents: read

concurrency:
  group: docker-image-${{ inputs.tag }}
  cancel-in-progress: false

jobs:
  build-amd64:
    runs-on: ubuntu-24.04
    timeout-minutes: 60
    steps:
      - name: Checkout
        uses: actions/checkout@v6
        with:
          ref: ${{ inputs.tag }}
          fetch-depth: 0

      - name: Log in to Docker Hub
        uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Build and push amd64 image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile
          platforms: linux/amd64
          push: true
          provenance: false
          tags: erans/navaris:${{ inputs.tag }}-amd64

  build-arm64:
    runs-on: ubuntu-24.04-arm
    timeout-minutes: 60
    steps:
      - name: Checkout
        uses: actions/checkout@v6
        with:
          ref: ${{ inputs.tag }}
          fetch-depth: 0

      - name: Log in to Docker Hub
        uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Build and push arm64 image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile
          platforms: linux/arm64
          push: true
          provenance: false
          tags: erans/navaris:${{ inputs.tag }}-arm64

  manifest:
    needs: [build-amd64, build-arm64]
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    steps:
      - name: Log in to Docker Hub
        uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Create multi-arch manifest for ${{ inputs.tag }}
        run: |
          docker buildx imagetools create \
            -t erans/navaris:${{ inputs.tag }} \
            erans/navaris:${{ inputs.tag }}-amd64 \
            erans/navaris:${{ inputs.tag }}-arm64

      - name: Create multi-arch manifest for latest
        run: |
          docker buildx imagetools create \
            -t erans/navaris:latest \
            erans/navaris:${{ inputs.tag }}-amd64 \
            erans/navaris:${{ inputs.tag }}-arm64
```

- [ ] **Step 2: Validate YAML syntax**

```bash
python3 -c "import yaml, sys; yaml.safe_load(open('.github/workflows/docker-image.yml'))" && echo OK
```

Expected output: `OK`. A YAML syntax error fails this step with a parse exception.

- [ ] **Step 3: Validate against actionlint (if available; otherwise skip)**

```bash
if command -v actionlint >/dev/null 2>&1; then
  actionlint .github/workflows/docker-image.yml && echo "actionlint OK"
else
  echo "actionlint not installed — skipping (the workflow will be linted by GitHub on push)"
fi
```

Expected output: either `actionlint OK` or the "not installed" message. If actionlint reports issues, fix them before proceeding.

- [ ] **Step 4: Sanity-check the workflow shape with grep**

```bash
grep -E "^name:|^on:|workflow_dispatch:|build-amd64:|build-arm64:|manifest:|needs:|ubuntu-24.04-arm|inputs.tag|DOCKERHUB_TOKEN|imagetools create" .github/workflows/docker-image.yml | head -30
```

Expected: shows the key structural lines (workflow name, trigger, three job names, the arm64 runner, the inputs reference, the secret reference, both manifest creates). All present means the structural intent is in place.

- [ ] **Step 5: Confirm the file is self-contained (no missing references)**

```bash
# The workflow references: inputs.tag, secrets.DOCKERHUB_USERNAME, secrets.DOCKERHUB_TOKEN, Dockerfile.
# Dockerfile must exist in the repo root.
ls Dockerfile
```

Expected: `Dockerfile` listed. (If absent, the build steps would fail at runtime; we verify upfront.)

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/docker-image.yml
git commit -m "ci: manual workflow_dispatch to publish Docker image to Docker Hub (amd64+arm64)"
```

---

## Task 2: Push the branch and open a PR

**Files:** none.

The workflow can't be tested without first being on the default branch (workflow_dispatch is only triggerable on workflows that exist on a branch the repo recognises). We open a PR so a maintainer can review the YAML, then merge to main, then trigger the workflow manually.

- [ ] **Step 1: Push the branch**

```bash
git push -u origin worktree-docker-image-workflow
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --title "ci: manual Docker image publish workflow" --body "$(cat <<'EOF'
## Summary

- Adds `.github/workflows/docker-image.yml`, a `workflow_dispatch`-triggered workflow that builds the all-in-one `Dockerfile` for `linux/amd64` and `linux/arm64` on native GitHub-hosted runners, then fuses them into a multi-arch manifest list pushed to `erans/navaris:<tag>` and `erans/navaris:latest`.
- One required input: `tag` (existing git tag, e.g. `v0.1.0`). The workflow checks out that ref and uses it as both the source and the published Docker tag.

## Spec / Plan

- Spec: [docs/superpowers/specs/2026-04-28-manual-docker-image-publish-design.md](docs/superpowers/specs/2026-04-28-manual-docker-image-publish-design.md)
- Plan: [docs/superpowers/plans/2026-04-28-manual-docker-image-publish.md](docs/superpowers/plans/2026-04-28-manual-docker-image-publish.md)

## Operator setup before first run

Two repo secrets must be added (Settings → Secrets and variables → Actions):

- `DOCKERHUB_USERNAME` — Docker Hub username with push rights to `erans/navaris`.
- `DOCKERHUB_TOKEN` — Docker Hub access token (not the account password) with read+write scope on `erans/navaris`.

## Test plan

- [x] YAML syntax validates (`python3 -c "import yaml; yaml.safe_load(...)"`).
- [x] No missing repo references — `Dockerfile` exists at the repo root, the workflow's `inputs.tag` and `secrets.*` are well-formed.
- [ ] After merge: trigger the workflow manually with `tag=v0.1.0` and verify the multi-arch manifest is published. The arm64 kernel build is a known risk (spec §10) — first run will reveal whether the existing kernel config needs arm64 tweaks.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL printed.

- [ ] **Step 3: Report back**

The PR is open. The next manual step (after merge) is for the operator to:
1. Add `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` secrets to the repo.
2. Run the workflow from the GitHub Actions UI with `tag=v0.1.0` (or any existing git tag).
3. If the arm64 build fails (kernel config issue), follow up per spec §10 mitigations.

---

## Notes for the executing agent

- The workflow can't be smoke-tested locally without Docker Hub creds and an arm64 builder. YAML syntax + actionlint + grep checks are the local verification. The actual end-to-end validation happens when the operator triggers the workflow on `main` after merging.
- Do NOT attempt to build the Dockerfile locally during plan execution — the kernel build alone takes 10+ minutes and isn't required for the plan to be considered done.
- Do NOT add Docker Hub credentials anywhere in the YAML, plan, or commits — they live only in GitHub repo secrets.
- The `provenance: false` flag on `docker/build-push-action` avoids attestation manifests that confuse `imagetools create` when fusing per-arch images. It's intentional, not a YAGNI omission.
