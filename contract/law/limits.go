package law

import "fmt"

// LoadLimits are inclusive, host-selected parser bounds. No zero field defaults.
// Every field must be positive and no larger than StandardV1's corresponding
// field. See the package documentation for counting rules.
type LoadLimits struct {
	Bytes       int
	Depth       int
	Members     int
	Elements    int
	StringBytes int
	Tokens      int
}

// CompactV1 returns a fresh compact-v1 profile value.
func CompactV1() LoadLimits { return LoadLimits{65536, 12, 64, 128, 128, 2048} }

// StandardV1 returns a fresh standard-v1 profile, the largest allowed bounds.
func StandardV1() LoadLimits { return LoadLimits{262144, 16, 128, 256, 256, 8192} }

func (l LoadLimits) validate() error {
	max := StandardV1()
	for _, b := range []struct {
		name       Bound
		value, max int
	}{
		{"bytes", l.Bytes, max.Bytes}, {"depth", l.Depth, max.Depth},
		{"members", l.Members, max.Members}, {"elements", l.Elements, max.Elements},
		{"string_bytes", l.StringBytes, max.StringBytes}, {"tokens", l.Tokens, max.Tokens},
	} {
		if b.value < 1 || b.value > b.max {
			return fmt.Errorf("%w: %s must be in 1..%d", ErrInvalidLimits, b.name, b.max)
		}
	}
	return nil
}
