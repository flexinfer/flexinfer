# CI Green Merge Loop Reference

Use this reference when you need the rationale behind the landing workflow or need to adjust it for repo policy.

## Why Auto-Merge Beats Manual Waiting

- GitLab auto-merge waits for the required merge checks, not just the absence of a failed branch pipeline:
  - https://docs.gitlab.com/user/project/merge_requests/auto_merge/
- The API exposes `auto_merge=true`, `sha`, `squash`, and `should_remove_source_branch` on the merge endpoint:
  - https://docs.gitlab.com/api/merge_requests/
- `glab mr merge` documents `--auto-merge` for the same workflow if a CLI-only path is preferred:
  - https://docs.gitlab.com/cli/mr/merge/

## Why The Loop Polls For Merge, Not Only Pipeline Success

- A green source-branch pipeline is necessary but not always sufficient. Merge-request checks, approvals, merge trains, and status checks can still keep the MR open:
  - https://docs.gitlab.com/user/project/merge_requests/auto_merge/
  - https://docs.gitlab.com/ci/pipelines/merge_trains/
- If duplicate pipeline types exist, the merge-request pipeline is the decisive one:
  - https://docs.gitlab.com/ci/pipelines/mr_pipeline_troubleshooting/

## Why Flakes Are A Separate Branch In The Workflow

- Quarantine keeps the main merge gate trustworthy while preserving visibility into flaky tests:
  - https://trunk.io/blog/what-we-learned-from-analyzing-20-2-million-ci-jobs-in-trunk-flaky-tests-part-1
  - https://www.minware.com/guide/best-practices/flaky-test-quarantine
- Blanket retries in the main lane tend to hide real regressions and burn team trust:
  - https://mill-build.org/blog/4-flaky-tests.html
  - https://thoughtbot.com/blog/dealing-with-flaky-tests

## Practical Defaults

1. Verify locally before the first push.
2. Reuse an open MR for the same source branch instead of opening duplicates.
3. Request auto-merge with the current source `sha`.
4. Poll until either the MR merges or a concrete blocker is visible.
