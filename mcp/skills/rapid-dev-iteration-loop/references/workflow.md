# Rapid Dev Iteration Reference

This skill is for experimental engineering loops where the fastest path is:

1. isolate one blocker
2. patch the narrowest point
3. build the smallest viable artifact
4. prove the cluster or runtime actually used that artifact
5. capture the next exact blocker

## Typical Inputs

- unstable backend or runtime integration
- experimental image profile
- one or more debug probe manifests
- target node or hardware class
- active `.loom` research / implementation notes

## Typical Outputs

- one blocker retired or narrowed
- exact artifact identifiers recorded
- exact failure text and patch point captured
- next iteration obvious from evidence

## Good Evidence

- image digest and pod `imageID`
- exact job name and node
- exact exception text and function name
- upstream source file and symbol
- model config values that explain the behavior

## Anti-Patterns

- multiple theories in one probe
- editing production manifests during exploratory bring-up
- relying on mutable tags without confirming the pulled digest
- describing the result vaguely instead of recording the exact blocker
- bundling cleanup or refactors into a blocker-isolation loop
