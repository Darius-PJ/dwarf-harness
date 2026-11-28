package workspace

import "fmt"

// CapacityProfile is Core's capture and checkpoint capacity. C4 extends this
// same profile; these values are not measured capacity.
type CapacityProfile struct {
	MaxFiles           int
	MaxBytes           int64
	BytesInFlight      int64
	CheckpointsPerPair int
	GenerationWindow   int
	Validation         *ValidationCapacity
}

// ValidationCapacity is owner-supplied admission capacity, not hard memory or
// CPU containment. Nil disables validation; no production defaults exist.
type ValidationCapacity struct {
	Lanes              int64               `json:"lanes"`
	QueuedJobs         int64               `json:"queued_jobs"`
	JobsPerPair        int64               `json:"jobs_per_pair"`
	MaterializedBytes  int64               `json:"materialized_bytes"`
	OutputBytes        int64               `json:"output_bytes"`
	MemoryBytes        int64               `json:"memory_bytes"`
	Processes          int64               `json:"processes"`
	CPUUnits           int64               `json:"cpu_units"`
	PerJob             ValidationResources `json:"per_job"`
	IncidentExecutions *int64              `json:"incident_executions,omitempty"`
}

// ValidationResources bounds a single attempt's reservations.
type ValidationResources struct {
	MaterializedBytes int64 `json:"materialized_bytes"`
	OutputBytes       int64 `json:"output_bytes"`
	MemoryBytes       int64 `json:"memory_bytes"`
	Processes         int64 `json:"processes"`
	CPUUnits          int64 `json:"cpu_units"`
}

// Validate refuses zero, unbounded and internally inconsistent capacities.
// Admission uses subtraction from the remaining total, never unchecked sums.
func (p ValidationCapacity) Validate() error {
	if p.Lanes < 1 || p.Lanes > 4 || p.QueuedJobs < 1 || p.JobsPerPair < 1 ||
		p.JobsPerPair > p.QueuedJobs || p.IncidentExecutions != nil && *p.IncidentExecutions < 1 {
		return fmt.Errorf("%w: invalid validation capacity", ErrInvalid)
	}
	for _, pair := range [][2]int64{
		{p.MaterializedBytes, p.PerJob.MaterializedBytes},
		{p.OutputBytes, p.PerJob.OutputBytes}, {p.MemoryBytes, p.PerJob.MemoryBytes},
		{p.Processes, p.PerJob.Processes}, {p.CPUUnits, p.PerJob.CPUUnits},
	} {
		if pair[1] < 1 || pair[0] < pair[1] {
			return fmt.Errorf("%w: invalid validation resource capacity", ErrInvalid)
		}
	}
	return nil
}

// ProvisionalCapacityProfile returns calibration inputs, not a service level.
func ProvisionalCapacityProfile() CapacityProfile {
	return CapacityProfile{MaxFiles: 100_000, MaxBytes: 1 << 30, BytesInFlight: 20 << 30, CheckpointsPerPair: 4, GenerationWindow: 4}
}

// Config configures workspace capacity. A nil Capacity makes capture unavailable.
type Config struct{ Capacity *CapacityProfile }

func validateProfile(p *CapacityProfile) error {
	if p != nil && (p.MaxFiles < 1 || p.MaxBytes < 1 || p.BytesInFlight < p.MaxBytes || p.CheckpointsPerPair < 1 || p.GenerationWindow < 1) {
		return fmt.Errorf("%w: invalid capacity profile", ErrInvalid)
	}
	if p != nil && p.Validation != nil {
		return p.Validation.Validate()
	}
	return nil
}
