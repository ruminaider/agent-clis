# Agent Ledger Phase 1: Findings

## Iron Law clarification

- **Question:** Execute the Phase 1 kernel slice of the Agent Ledger spec (`SPEC.md` §32 Phase 1) in this repository.
- **Assumed answer:** Implement the 12-task, 3-wave decomposition already captured in `tasks/phase-1-task-packet.md`, layered with the tech-choice addendum (dp-002 through dp-008) in `task_plan.md`, gated per task by `gate-reviewer` and per wave by `wave-review-orchestrator`.
- **What "done" looks like:** The four SPEC §32 Phase 1 acceptance scenarios pass:
  1. Two local agents can claim and record disjoint files.
  2. Overlapping claims produce deterministic conflict output.
  3. `verify --json` reports unclaimed and forbidden changes.
  4. No full diff content is stored by default.

If any of those four scenarios cannot be demonstrated at the Wave 3 final review, Phase 1 is not done and remediation is required before Phase 2 starts.

## Approaches evaluated

### Approach A: Amend the existing packet incrementally (chosen)

Layer per-task tech-choice addenda over `tasks/phase-1-task-packet.md`. Workers read both the task and the addendum.

Risks:

- Two-artifact problem: a worker reads the packet and misses the addendum, then chooses a different SQLite driver or CLI framework. Mitigation: `task_plan.md`'s addendum is referenced from the plan header and called out in every wave-review checklist; `gate-reviewer` enforces it.
- Addendum drift: the addendum could contradict the packet text. Mitigation: addendum explicitly states "addendum wins where ambiguous" and the worker is required to surface contradictions in gate-review notes.
- Task 005 is heavy (identity + assign + claim + conflicts + status). Mitigation: split into 005a/005b only if Wave 1 review flags it. Default is single PR.

Open questions:

- Does the gate-reviewer have the addendum in its review checklist? It must. Confirm at first dispatch.
- Is the cross-clone case (separate clones, advisory locks not shared) genuinely punted by SPEC §2 #4? Yes, per the spec, but worth re-confirming with oracle if a real dogfood scenario surfaces.

### Approach B: Regenerate the packet from scratch via task-generator

Throw out the existing packet. Have `task-generator` produce a fresh decomposition that bakes dp-002 through dp-008 directly into each task's requirements section.

Risks:

- Burns a planning round before any code lands. The existing packet already passed an implementation-readiness review (per its own header) and aligns with SPEC §32 / §34 ordering.
- Regenerated packet may drift from SPEC §34's recommended ordering and re-introduce decisions already settled.
- No clear benefit over Approach A once the addendum exists, except cosmetic uniformity.

Open questions:

- Would a regenerated packet split Task 005 into smaller PRs by default? Probably yes, but Approach A can do that too on demand.
- Does the team genuinely benefit from a single artifact over packet+addendum? Empirically, the existing packet is high-quality; the marginal value of regeneration is low.

### Approach C: Execute the existing packet as-is, no addendum

Trust the packet. Let workers improvise SQLite driver, CLI framework, lock backend, packaging strategy.

Risks:

- High likelihood Wave 1 gate-review uncovers inconsistent infrastructure choices (e.g., one worker imports `mattn/go-sqlite3` for its CGO performance, another imports `modernc.org/sqlite` for static builds, and the third improvises with `crawshaw.io/sqlite`).
- Cross-cutting decisions (lock abstraction, payload privacy, ID shape) get re-litigated per task.
- Packaging Task 011 stalls choosing CGO vs pure Go after the choice has already been baked in.

Open questions: none worth pursuing. Approach C is dominated by Approach A.

### Approach D: Sequential vs parallel waves

Independent of A/B/C, the wave structure can be executed strictly sequentially or with limited intra-wave parallelism (Task 002 and 003 in parallel after 001 lands; Task 005, 006, 007 in parallel after Wave 1 review).

Chosen: limited intra-wave parallelism per the packet's "Recommended execution order". Workers must coordinate on shared interfaces (e.g., storage interfaces between Task 002 and Task 003) before parallel work begins.

Risks:

- Parallel workers race on shared interface design (e.g., `internal/storage` shape). Mitigation: worker on the earlier-touched task drafts the interface stubs first; the parallel worker rebases on top.
- Race for shared `internal/events`, `internal/storage`, `internal/paths`. Mitigation: explicit per-task "Allowed files/directories" lists in the packet already enforce ownership.

## Open questions / unknowns

1. **Cross-clone advisory locks**: SPEC §2 #4 punts cross-clone coordination to post-MVP. dp-006 honors that. If a real dogfood case (two clones of the same repo on the same machine) surfaces, route to `oracle` for re-evaluation. Not blocking for Phase 1.
2. **Pure-Go SQLite under heavy concurrent claim load**: dp-003 mitigates with subprocess integration tests in Task 010. If those tests trip the falsification signal, fall back to `mattn/go-sqlite3` behind a build tag. Not blocking for Phase 1 entry; possibly blocking for Phase 1 exit.
3. **Cobra binary size vs SPEC §27 cold-start latency**: Cobra-based binaries land in the 8–15 MB range with `-trimpath -ldflags="-s -w"`. SPEC §27 demands sub-20 ms `claim`. Worth a quick benchmark in Task 001 acceptance to confirm. If cold-start exceeds 50 ms, escalate to `oracle` for framework reconsideration.
4. **Subprocess test runtime in CI**: dp-007 requires SPEC §31.2 #1–3 and #6–7 to run by default. If CI duration becomes a problem (>5 min), parallelize at the Go test level rather than skipping. Do not gate behind `-short`.
5. **Goreleaser snapshot artifact storage**: dp-008 wires `goreleaser release --snapshot --clean` into CI on every PR. Do we keep snapshot artifacts as CI artifacts for download? Default: yes, 7-day retention. Confirm with user if storage cost matters.
6. **Validation argument format edge case**: SPEC §18.6 says `--validation` is `<command>:<status>` with status parsed after the last colon. What about an empty status (`some-cmd:`)? Treat as `unknown` and emit a warning? Confirm during Task 006 implementation.
7. **`AGENT_ID` forgery scope**: SPEC §4 says MVP targets honest-but-forgetful agents. dp-001 through dp-008 do not address signing. Confirm no Phase 1 worker tries to "harden" identity beyond the spec; that work is Phase 5.
8. **Project-fingerprint stability across symlink layouts**: SPEC §8.1 inputs include git common dir realpath. If a user moves their checkout (e.g., from `/Users/x/work/repo` to `/Users/x/dev/repo`), the realpath of the common dir changes and the fingerprint changes. Spec implies this is intentional (separate clones get separate ledgers). Worth a unit test in Task 002 to lock the behavior.
