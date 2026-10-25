package providers

import (
	"math/big"
	"slices"
	"strconv"
	"strings"
)

const (
	Uncached     = "input.uncached"
	CacheRead    = "input.cache_read"
	CacheWrite5m = "input.cache_write_5m"
	CacheWrite1h = "input.cache_write_1h"
	Generated    = "output.generated"
)

var inputClasses = []string{CacheRead, CacheWrite1h, CacheWrite5m, Uncached}
var allClasses = []string{CacheRead, CacheWrite1h, CacheWrite5m, Uncached, Generated}

// Rate is an exact USD-per-million-token rational, not a floating-point price.
type Rate struct {
	Class       string `json:"class"`
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}

type Tariff struct {
	Currency string `json:"currency"`
	Rates    []Rate `json:"rates"`
}

// Component groups disjoint billing classes under one inclusive maximum.
// Its amount is not rounded independently.
type Component struct {
	Classes     []string
	MaxUnits    int64
	Numerator   string
	Denominator string
}

func decimal(s string) (int64, error) {
	if len(s) == 0 || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, ErrInvalid
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrInvalid
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrOverflow
	}
	return n, nil
}

// ParseMoney converts a strict unsigned decimal dollar amount to microdollars.
func ParseMoney(s string) (int64, error) {
	if len(s) > 26 {
		return 0, ErrOverflow
	}
	whole, frac, dotted := strings.Cut(s, ".")
	n, err := decimal(whole)
	if err != nil {
		return 0, err
	}
	if dotted && (len(frac) == 0 || len(frac) > 6) {
		return 0, ErrInvalid
	}
	var f int64
	for i := range len(frac) {
		if frac[i] < '0' || frac[i] > '9' {
			return 0, ErrInvalid
		}
		f = f*10 + int64(frac[i]-'0')
	}
	for i := len(frac); i < 6; i++ {
		f *= 10
	}
	const max = int64(1<<63 - 1)
	if n > (max-f)/1_000_000 {
		return 0, ErrOverflow
	}
	return n*1_000_000 + f, nil
}

func rational(n, d string) (*big.Rat, error) {
	num, err := decimal(n)
	if err != nil {
		return nil, err
	}
	den, err := decimal(d)
	if err != nil || den == 0 {
		return nil, ErrInvalid
	}
	return new(big.Rat).SetFrac64(num, den), nil
}

// Maximum rounds the sum once, rejecting overlap and overflow rather than
// saturating a financial bound.
func Maximum(components []Component) (int64, error) {
	if len(components) == 0 || len(components) > len(allClasses) {
		return 0, ErrInvalid
	}
	seen := make(map[string]bool)
	sum := new(big.Rat)
	for _, c := range components {
		if c.MaxUnits < 0 || len(c.Classes) == 0 || len(c.Classes) > len(allClasses) {
			return 0, ErrInvalid
		}
		for _, class := range c.Classes {
			if !slices.Contains(allClasses, class) || seen[class] {
				return 0, ErrInvalid
			}
			seen[class] = true
		}
		rate, err := rational(c.Numerator, c.Denominator)
		if err != nil {
			return 0, err
		}
		sum.Add(sum, rate.Mul(rate, new(big.Rat).SetInt64(c.MaxUnits)))
	}
	return ceilMicros(sum)
}

func ceilMicros(r *big.Rat) (int64, error) {
	n, rem := new(big.Int), new(big.Int)
	n.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Sign() > 0 {
		n.Add(n, big.NewInt(1))
	}
	if !n.IsInt64() || n.Sign() < 0 {
		return 0, ErrOverflow
	}
	return n.Int64(), nil
}

// Bound uses the full rectangular input/output maxima. Eligible input classes
// must come from the compiled request; caching never reduces inclusive input.
func Bound(p Profile, requestedOutput int64, eligible []string) ([]Component, int64, error) {
	if err := p.Validate(); err != nil {
		return nil, 0, err
	}
	if requestedOutput <= 0 || requestedOutput > p.OutputMax || len(eligible) == 0 {
		return nil, 0, ErrInvalid
	}
	classes := slices.Clone(eligible)
	slices.Sort(classes)
	var best Rate
	bestRat := new(big.Rat).SetInt64(-1)
	for i, class := range classes {
		if !slices.Contains(inputClasses, class) || i > 0 && class == classes[i-1] {
			return nil, 0, ErrInvalid
		}
		for _, r := range p.Tariff.Rates {
			if r.Class == class {
				rr, _ := rational(r.Numerator, r.Denominator)
				if rr.Cmp(bestRat) > 0 {
					best, bestRat = r, rr
				}
			}
		}
	}
	var output Rate
	for _, r := range p.Tariff.Rates {
		if r.Class == Generated {
			output = r
		}
	}
	cs := []Component{{classes, min(p.InputMax, p.ContextMax), best.Numerator, best.Denominator}, {[]string{Generated}, requestedOutput, output.Numerator, output.Denominator}}
	n, err := Maximum(cs)
	return cs, n, err
}
