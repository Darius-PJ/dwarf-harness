package workspace

import "fmt"

// CaptureProfile is the capture subset of CORE:862's CapacityProfile. Ticket
// 06 and C4 extend that same profile; these values are not measured capacity.
type CaptureProfile struct {
	MaxFiles      int
	MaxBytes      int64
	BytesInFlight int64
}

// ProvisionalCaptureProfile returns calibration inputs, not a service level.
func ProvisionalCaptureProfile() CaptureProfile {
	return CaptureProfile{MaxFiles: 100_000, MaxBytes: 1 << 30, BytesInFlight: 20 << 30}
}

// Config configures workspace capacity. A nil Capture makes capture unavailable.
type Config struct{ Capture *CaptureProfile }

func validateProfile(p *CaptureProfile) error {
	if p != nil && (p.MaxFiles < 1 || p.MaxBytes < 1 || p.BytesInFlight < p.MaxBytes) {
		return fmt.Errorf("%w: invalid capture capacity profile", ErrInvalid)
	}
	return nil
}
