package sdk

// Backward-compatible aliases retained for callers that used the original
// unqualified probe outcome constants before code generation gained another
// outcome enum.
const (
	Failed ProbeResultInputOutcome = ProbeResultInputOutcomeFailed
	Passed ProbeResultInputOutcome = ProbeResultInputOutcomePassed
)
