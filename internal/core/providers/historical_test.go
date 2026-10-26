package providers

import (
	"errors"
	"reflect"
	"testing"
)

func TestHistoricalAccountingBoundary(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	units := []Units{{Class: CacheRead}, {Class: CacheWrite1h}, {Class: CacheWrite5m}, {Class: Uncached, Count: 10}, {Class: Generated, Count: 2}}
	price, err := PriceUsage(p, units)
	if err != nil {
		t.Fatal(err)
	}
	components, maximum, err := Bound(p, 20, inputClasses)
	if err != nil {
		t.Fatal(err)
	}
	// Historical money is not invalidated by installation-only evidence limits
	// or a verification lifetime. These checks still fence the live boundary.
	p.Sources = nil
	p.VerifiedUTCNS, p.ExpiresUTCNS = 0, 0
	if p.Validate() == nil {
		t.Fatal("historical interpretation weakened installation")
	}
	got, err := PriceHistoricalUsage(p, units)
	if err != nil || got != price {
		t.Fatalf("historical price changed with installation metadata: %d %v", got, err)
	}
	cs, n, err := BoundHistorical(p, 20, inputClasses)
	if err != nil || n != maximum || !reflect.DeepEqual(cs, components) {
		t.Fatalf("historical bound changed with installation metadata: %+v %d %v", cs, n, err)
	}
	p.Provider = "future_provider"
	if _, err := PriceHistoricalUsage(p, units); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("invented future provider money: %v", err)
	}
	p.Provider = "anthropic"
	p.Tariff.Rates[0].Denominator = "0"
	if _, _, err := BoundHistorical(p, 20, inputClasses); err == nil {
		t.Fatal("historical path accepted undefined tariff arithmetic")
	}
}
