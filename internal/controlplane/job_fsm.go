package controlplane

// devicePhases are the only states that may hold a simulator/device lease.
var devicePhases = map[JobState]bool{
	JobWaitingForDevice: true,
	JobInstalling:       true,
	JobLaunching:        true,
	JobTesting:          true,
}

// HoldsDevice reports whether state requires a simulator/device lease.
func HoldsDevice(state JobState) bool {
	return devicePhases[state]
}

// progressOrder is the happy-path sequence (excluding terminal/side states).
var progressOrder = []JobState{
	JobQueued,
	JobSyncing,
	JobPreparing,
	JobBuilding,
	JobWaitingForDevice,
	JobInstalling,
	JobLaunching,
	JobTesting,
	JobCollecting,
	JobSucceeded,
}

var progressIndex = func() map[JobState]int {
	m := make(map[JobState]int, len(progressOrder))
	for i, s := range progressOrder {
		m[s] = i
	}
	return m
}()

var terminalStates = map[JobState]bool{
	JobSucceeded: true,
	JobCancelled: true,
	JobOrphaned:  true,
}

// CanTransition reports whether from→to is a legal job FSM edge.
func CanTransition(from, to JobState) bool {
	if from == to {
		return false
	}
	if terminalStates[from] {
		return false
	}
	// Side exits from any non-terminal (including failed → retrying).
	switch to {
	case JobFailed, JobCancelled, JobOrphaned:
		return from != JobFailed // failed may only go to retrying
	case JobRetrying:
		return from == JobFailed || (!terminalStates[from] && from != JobRetrying)
	case JobQueued:
		// Re-queue after retrying.
		return from == JobRetrying
	}

	if from == JobFailed || from == JobRetrying {
		return false
	}

	fromIdx, fromOK := progressIndex[from]
	toIdx, toOK := progressIndex[to]
	if !fromOK || !toOK {
		return false
	}
	// Strictly forward by one step on the happy path.
	return toIdx == fromIdx+1
}
