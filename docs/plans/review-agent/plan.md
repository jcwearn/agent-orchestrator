# Plan: Add Review Agent

## Context

After the implementation agent creates a PR, the task immediately completes with no automated code review. This means every PR requires manual review before merging. Adding a review agent that automatically reviews PRs, leaves inline comments, and can trigger auto-fix loops will significantly reduce the manual review burden.

## State Machine Change

```
Current:  queued → planning → awaiting_approval → implementing → complete
New:      queued → planning → awaiting_approval → implementing → reviewing → complete
                                                                   ↕ (fix loop)
```

The review step runs within the same goroutine and workspace as `runImplement`, keeping the workspace alive for the review-fix loop.

## Phases

### Phase 1: Save Plan & Open PR

Create `docs/plans/review-agent/plan.md` (this plan) and `docs/plans/review-agent/progress.md` (progress tracker). Commit to a `docs/review-agent-plan` branch and open a PR. This is a standalone PR that gets merged before implementation begins.

### Phase 2: Database Migration & Model

**New file:** `internal/store/migrations/005_review_fields.sql`
```sql
ALTER TABLE tasks ADD COLUMN review_round INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN max_review_rounds INTEGER NOT NULL DEFAULT 3;
ALTER TABLE tasks ADD COLUMN last_review_verdict TEXT;
ALTER TABLE tasks ADD COLUMN last_review_summary TEXT;
```

**Modify:** `internal/store/models.go` — Add 4 fields to `Task` struct:
- `ReviewRound int`, `MaxReviewRounds int`, `ReviewVerdict *string`, `ReviewSummary *string`

**Modify:** `internal/store/tasks.go` — Append 4 new columns to all SQL queries:
- `scanTask` (line 273): add 4 `Scan` targets
- All SELECT column lists in `GetTask`, `GetTaskByPRNumber`, `GetTaskByGithubIssue`, `ListTasks` (both branches)
- `CreateTask` INSERT columns + placeholders + values
- `UpdateTask` SET clause + values

### Phase 3: Orchestrator Types & Status

**Modify:** `internal/orchestrator/queue.go`
- Add `StatusReviewing = "reviewing"`
- Add `reviewing` tasks to `activeTasks()` for crash recovery

**Modify:** `internal/orchestrator/orchestrator.go`
- Add types:
  ```go
  type ReviewComment struct {
      Path string `json:"path"`
      Line int    `json:"line"`
      Body string `json:"body"`
  }
  type ReviewResult struct {
      Verdict  string          `json:"verdict"`
      Summary  string          `json:"summary"`
      Comments []ReviewComment `json:"comments"`
  }
  ```
- Extend `Notifier` interface with 3 methods:
  - `NotifyReviewStarted(ctx, owner, repo, issue, round) error`
  - `SubmitPRReview(ctx, owner, repo, prNumber, event, summary, comments) error`
  - `NotifyMaxReviewRoundsReached(ctx, owner, repo, issue, rounds) error`
- Add `MaxReviewRounds int` to `Config` (default: 3)

### Phase 4: Review & Fix Steps

**Modify:** `internal/orchestrator/steps.go` — Add 4 new functions:

1. **`stepReview`** — Runs Claude on the workspace to review the diff, parses JSON output into `ReviewResult`. Uses `buildReviewPrompt` as the prompt. The workspace already has the code checked out from the implementation step.

2. **`stepFix`** — Runs Claude with `--allowedTools 'Bash,Edit,Write'` to address review comments. Uses `buildFixPrompt`. Agent commits and pushes to the existing branch.

3. **`buildReviewPrompt`** — Instructs Claude to:
   - Run `git diff <base_branch>...HEAD` to see all changes
   - Read full files for context (not just diff)
   - Evaluate: correctness, code quality, edge cases, security, tests
   - Output ONLY raw JSON: `{"verdict":"approve"|"request_changes","summary":"...","comments":[{"path":"...","line":N,"body":"..."}]}`
   - Only comment on lines that appear in the diff (GitHub API constraint)

4. **`buildFixPrompt`** — Provides the review summary and inline comments, instructs Claude to fix all issues, commit, and push to the same branch.

5. **`parseReviewResult`** — Extracts JSON from output (find first `{` to last `}`), unmarshals into `ReviewResult`, validates verdict is `"approve"` or `"request_changes"`.

### Phase 5: Review Loop in runImplement

**Modify:** `internal/orchestrator/orchestrator.go` — Rewrite `runImplement`:

After `stepImplement` completes and PR URL is extracted, insert a review loop:

```
for round := 1 to maxRounds:
    1. Set status = "reviewing", current_step = "review"
    2. Notify GitHub issue: "Code Review (Round N)"
    3. Run stepReview → parse JSON → ReviewResult
    4. Store verdict/summary on task
    5. Submit PR review via SubmitPRReview (APPROVE or REQUEST_CHANGES with inline comments)
    6. If verdict == "approve" → break
    7. If last round → notify max rounds reached → break
    8. Set status = "implementing", current_step = "fix"
    9. Run stepFix → agent fixes and pushes
    10. Continue loop
```

Skip review entirely if `task.PRNumber` is nil (edge case where PR creation failed to parse).

Move `LinkPRToIssue` call before the review loop (it currently runs after implementation, keep that).

### Phase 6: GitHub Notifier Methods

**Modify:** `internal/github/notifier.go` — Add:

1. `ReviewComment` struct (local to github package, same fields as orchestrator's)

2. `NotifyReviewStarted` — Posts issue comment: "Code Review (Round N) — Reviewing..."

3. `SubmitPRReview` — Uses `client.PullRequests.CreateReview` with:
   - `Event`: "APPROVE" or "REQUEST_CHANGES"
   - `Body`: review summary
   - `Comments`: `[]*gogithub.DraftReviewComment` with `Path`, `Line`, `Side: "RIGHT"`, `Body`
   - Fallback: if `CreateReview` fails with inline comments (422 from GitHub when lines aren't in diff), retry with body-only review

4. `NotifyMaxReviewRoundsReached` — Posts issue comment about hitting the limit

### Phase 7: Adapter & Config Wiring

**Modify:** `cmd/main.go`
- Add 3 adapter methods on `notifierAdapter` for the new `Notifier` interface methods
- Convert `orchestrator.ReviewComment` → `github.ReviewComment` in the adapter
- Read optional `MAX_REVIEW_ROUNDS` env var into `orchConfig.MaxReviewRounds`

### Phase 8: Frontend

**Modify:** `web/src/components/StatusBadge.tsx` — Add `reviewing` status:
```typescript
reviewing: {
    label: "Reviewing",
    className: "bg-purple-500/20 text-purple-400 hover:bg-purple-500/20",
},
```

### Phase 9: Tests

**Modify:** `internal/orchestrator/orchestrator_test.go`
- Update `mockNotifier` with 3 new interface methods
- Add tests:
  - Review approves on first round → task completes
  - Review requests changes → fix → approve on round 2
  - Max review rounds exhausted → task still completes
  - No PR number → review skipped
  - Invalid JSON from review agent → task fails

**Add unit tests** for `parseReviewResult` in `internal/orchestrator/steps_test.go`:
- Valid approve/request_changes JSON
- JSON with surrounding text
- Invalid JSON, missing/invalid verdict

**Modify:** `internal/github/notifier_test.go`
- Test `SubmitPRReview` with approve and request_changes events
- Test inline comment fallback on 422 error

**Modify:** `internal/store/tasks_test.go`
- Test new fields round-trip through CreateTask/GetTask

## Files Summary

| File | Action |
|------|--------|
| `internal/store/migrations/005_review_fields.sql` | Create |
| `internal/store/models.go` | Modify |
| `internal/store/tasks.go` | Modify |
| `internal/orchestrator/queue.go` | Modify |
| `internal/orchestrator/orchestrator.go` | Modify |
| `internal/orchestrator/steps.go` | Modify |
| `internal/github/notifier.go` | Modify |
| `cmd/main.go` | Modify |
| `web/src/components/StatusBadge.tsx` | Modify |
| `internal/orchestrator/orchestrator_test.go` | Modify |
| `internal/github/notifier_test.go` | Modify |
| `internal/store/tasks_test.go` | Modify |

## Verification

1. **Unit tests**: `go test ./internal/...` — all existing + new tests pass
2. **Build**: `go build ./cmd/...` compiles cleanly
3. **Migration**: Start the service → verify `005_review_fields` migration applies
4. **Frontend**: Build the web app, verify "Reviewing" badge renders
5. **Integration (manual)**: Create a task via API → verify the flow reaches "reviewing" status after implementation completes, and that a PR review appears on GitHub with inline comments
