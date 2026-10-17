package billing

// CanonicalCategory shares the Billing classifier with deterministic planners.
func CanonicalCategory(value string) (string, bool) { return category(value) }
