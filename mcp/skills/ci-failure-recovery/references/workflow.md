# CI Failure Recovery Reference

This reference keeps the skill body short while preserving the patterns behind the loop.

## GitLab Merge Safety

- Auto-merge is the default safer handoff after a repo-local CI fix, because GitLab can merge only after all merge checks pass:
  - https://docs.gitlab.com/user/project/merge_requests/auto_merge/
- The Merge Requests API supports `auto_merge=true` and an explicit `sha` on the merge endpoint:
  - https://docs.gitlab.com/api/merge_requests/
- Merge trains validate combined queued changes and drop the failing MR from the train if its train pipeline fails:
  - https://docs.gitlab.com/ci/pipelines/merge_trains/
- When both branch and merge-request pipelines exist, GitLab's mergeability checks follow the MR pipeline, not the duplicate branch pipeline:
  - https://docs.gitlab.com/ci/pipelines/mr_pipeline_troubleshooting/

## Flaky-Test Handling Ideas

These were reviewed with Tavily and are useful heuristics for the skill:

- Quarantine confirmed flaky tests instead of masking them with blanket retries in the main gate:
  - https://trunk.io/blog/what-we-learned-from-analyzing-20-2-million-ci-jobs-in-trunk-flaky-tests-part-1
  - https://www.minware.com/guide/best-practices/flaky-test-quarantine
- Keep quarantined tests running in a non-blocking lane so fixes can be validated and the suite does not silently rot:
  - https://mill-build.org/blog/4-flaky-tests.html
- Attach ownership and a follow-up plan; quarantine is temporary debt, not a permanent exception:
  - https://thoughtbot.com/blog/dealing-with-flaky-tests

## Practical Translation For Loom

1. Triage first: identify the earliest causal failed job and collect logs.
2. Decide repo-local vs flaky vs infra before editing.
3. Land repo-local fixes with auto-merge / merge trains instead of manual merge races.
4. When a failure is truly flaky, reduce blast radius with quarantine and create a follow-up fix path.
