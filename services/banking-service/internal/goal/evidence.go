package goal

// MatchesAmountEvidence exposes the existing integer-cent evidence validator
// to v2 compilers without changing the v1 parsing contract.
func MatchesAmountEvidence(evidence string, amountMinor int64) bool {
	return amountIn(evidence, amountMinor)
}
