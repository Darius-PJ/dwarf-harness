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
	return nil
}
