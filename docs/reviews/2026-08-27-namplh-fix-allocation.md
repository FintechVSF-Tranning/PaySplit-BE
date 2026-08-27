# Review, namplh/fix-allocation, 2026-08-27

**Reviewed by**: Gemini 3.5 Flash (author on Gemini 3.5 Flash)
**Scope**: 3 files, namplh/fix-allocation vs main
**Verdict**: Approve

## Summary
This change implements the exact bill allocation rounding revision using precise rational arithmetic via `math/big.Rat` to compute provisional members' breakdown and allocate residual VND. The implementation matches all spec requirements, including largest remainder distribution and stable tie breaking using UUID lexicographical order. The test suite is extensive and verifies invariants, extreme inputs, and performance targets.

## Strengths
- **Use of Exact Rational Arithmetic**: Intermediate division and proportions are computed using `math/big.Rat` instead of floating point, preventing any cumulative loss of accuracy before the final rounding pass.
- **Robust Boundary Testing**: The brute force invariant test sweeps through thousands of combinations of subtotals, weight distributions, service charges, VAT, and discounts, ensuring the allocation is always exact and non negative.
- **Deterministic and Fair Tie Breaking**: Tie breaking based on canonical UUID byte sorting correctly guarantees stability regardless of the order of participants in requests.

## Test coverage
Unit tests in `allocation_test.go` cover exact item shares aggregation, largest remainder distribution, mixed participation, input permutation stability, and zero subtotal defaults. The benchmark verifies that a large allocation of 100 items and 50 members runs in ~3.0 ms, well below the 50 ms budget requirement.
