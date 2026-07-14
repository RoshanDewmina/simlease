package controlplane

// ClassifyRetry maps a failure class to whether a retry is safe.
//
// Infrastructure failures (capacity, pressure, transient provisioner issues)
// are safe to retry. Source failures (compile/test) and explicit cancellation
// are not. Unknown stays unknown so callers do not silently retry.
func ClassifyRetry(class FailureClass) RetryClass {
	switch class {
	case FailureInfrastructure:
		return RetrySafe
	case FailureSource, FailureCancelled:
		return RetryUnsafe
	default:
		return RetryUnknown
	}
}
