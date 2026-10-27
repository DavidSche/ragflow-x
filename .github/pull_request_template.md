# PR Test Evidence

## Contract

- Contract source: 
- Contract change: None / Updated
- Architecture and permission impact: 

## Scenario

- Scenario IDs: 
- Scenario status before → after: 
- Risks covered: 

## Change Impact

- Mapped files / functions / routes:
- State, side effect, or provider behavior changed: 

## Test Strategy

- Levels used: Unit / Service / Contract / PG / E2E / Component / E2E
- Existing regression extended or added: 
- Why the level is sufficient: 

## TDD Evidence

### RED

- Command:
- Failing assertion / observed result:

### GREEN

- Test names:
- Passing command:

## Oracle

- Input and preconditions:
- Observable result:
- State invariant:
- Security invariant:
- Side effects asserted:

## Mutation / Negative Proof

- Applicable mutation targets:
- Negative test / mutation evidence:
- Not-applicable targets and reason:

## Coverage

- P0 scenario closure change:
- Changed-lines coverage attribution:
- Intentionally uncovered code and reason:

## Validation

- `make test-p0`: PASS / FAIL / NOT RUN
- `make test-contract`: PASS / FAIL / NOT RUN
- `make test-race`: PASS / FAIL / NOT RUN / NOT AVAILABLE
- `make test-coverage`: PASS / FAIL / NOT RUN
- `make test-integration`: PASS / FAIL / NOT RUN
- Frontend unit / coverage / E2E: PASS / FAIL / NOT RUN

## Documentation

- Updated docs and contracts: 
- Catalogue updated: Yes / No

## Risks

- Known remaining risks:
- Follow-up scenario / waiver:

## Final Status

- Test Strategy Gate: PASS / FAIL
- P0 Scenario Gate: PASS / FAIL / N/A
- Ready to merge: Yes / No

## Reviewer Checklist

- [ ] Contract was identified before implementation-focused tests.
- [ ] Scenario IDs, risks, and Oracle are explicit.
- [ ] New behavior has a meaningful RED where applicable.
- [ ] Assertions cover state, error semantics, tenant/project boundaries, side effects, and trace.
- [ ] Tests avoid weak-only assertions such as HTTP 200, non-empty object, or `error != nil`.
- [ ] Mocks and fixtures follow real HTTP/Provider contracts and use synthetic data.
- [ ] Provider retry distinguishes request attempts from business side effects.
- [ ] Tests avoid fixed sleeps, order coupling, shared mutable state, or unclassified `t.Skip`.
- [ ] SQLite/PostgreSQL differences are handled explicitly.
- [ ] Frontend tests assert user-visible behavior and accessibility semantics where relevant.
- [ ] Contract, permission, migration, and security docs are synchronized.
- [ ] Any waiver is approved, unexpired, and has alternative evidence.
- [ ] Removed tests/specs have replacement or archived evidence.
