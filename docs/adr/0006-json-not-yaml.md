# ADR-0006: Session profiles are JSON

## Status

Accepted.

## Context

The natural alternative to JSON is YAML, which is more comfortable to
hand-edit. The question is whether the ergonomic win is worth adding a
YAML parser to the host-side tooling.

## Decision

Profiles are JSON. YAML is deferred.

## Consequences

Host-side validation and canonicalization use `jq`, which is already a
dependency. There is no second parser to keep in step with the schema,
no second set of edge cases (multi-document files, anchor references,
implicit type coercion), and no additional install-time surface.

Profiles are less pleasant to hand-edit as they grow. The example file
`config/session-profile.example.json` mitigates this by giving authors
something concrete to copy from. Reconsidering YAML would be reasonable
if profiles routinely grow past a screenful and hand-editing becomes a
real friction point.
