# SDD ledger — plan: docs/superpowers/plans/2026-07-26-milestone-5-kubernetes-edge.md

Workspace: `/Users/guilhermecastro/repos/araihu/xisnove`
Branch: `codex/milestone-4a-control-plane`
Plan freeze: `ac40419`
Execution baseline: `4432558`
Baseline: `make check`, operator verify/race tests, and Helm lint passed; Vacuum emitted 267 pre-existing warnings and zero errors.

Task 1: plan-path omissions authorized — obsolete M4 empty-batch/scope assertions and generated ServeMux `:revoke` compatibility tests/router.
Task 1: fix round 1/5 (1 addressed, 0 open — initial Agent generation frozen to exactly 1; commits 65ea694..062f2b5)
Task 1: complete (commits 4432558..062f2b5, review clean)

Task 6: complete (commits 062f2b5..0a3f41c, review clean)

Task 2: minor (deferred): discovery_batches index uses server completed_at instead of client observed_completed_at; final review must decide whether to change or remove it.

Task 5: fix round 1/5 (1 addressed, 0 open — operator module UUID dependency made direct and tidy; commits e829c5d..4e32727)
Task 5: complete (commits 0a3f41c..4e32727 excluding concurrent Task 2 commit e829c5d, review clean)

Task 2: fix round 1/5 (2 addressed, 0 open — stale complete fence and full managed Turso journey; commits 4e32727..6d3359c)
Task 2: complete (commits 35975ed..6d3359c excluding concurrent Task 5 fix 4e32727, review clean; 1 deferred minor)

Task 7: fix round 1/5 (0 addressed, 2 open — scoped relist completeness and bootstrap retry; commits 46cf018..7b693fa)
Task 7: fix round 2/5 (2 addressed, 0 open — aggregate relist coordinator and retrying bootstrap; commits 3852148..200161f)
Task 7: complete (commits 6d3359c..200161f excluding concurrent Task 3 commits, review clean)

Task 3: fix round 1/5 (2 addressed, 1 open — durable idempotency added; concurrent loser resolution remained; commits 7b693fa..3852148)
Task 3: fix round 2/5 (1 addressed, 0 open — mutation conflicts resolve through winning idempotency record; commits 200161f..79fab37)
Task 3: complete (commits fef89fd..79fab37 excluding concurrent Task 7 commits, review clean)

Task 4: minor (deferred): unexpected internal UUID parse errors flow to strict response error handler; final review should consider generic internal mapping.

Task 4: fix round 1/5 (1 addressed, 0 open — validator diagnostics sanitized and leak-tested; commits 918af39..b084dc1)
Task 4: complete (commits 630843e..b084dc1, review clean; 1 deferred minor)
Task 8: fix round 1/5 (6 addressed, 0 open — owner-proven observation, generation-aware and bounded reconciliation keys, presented-generation revoke fence, UTF-8 status bounds)
Task 8: fix round 2/5 (1 addressed, 0 open — credential-free bound Agent apply after bootstrap)
Task 8: fix round 3/5 (1 addressed, 0 open — failed post-bootstrap Apply retries before Observe)
Task 8: fix round 4/5 (acceptance guardrails strengthened)
Task 8: fix round 5/5 (post-bootstrap and generation-three convergence acceptance evidence)
Task 8: parent correction after round 5 (control-plane capability assertion, truthful Synced evidence, and mock credential-free creation parity)
