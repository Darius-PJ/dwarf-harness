package law

import "crypto/sha256"

// ValidationPolicy is D47's local policy artifact. SHA256 is the manifest's
// validation_policy_hash; these values never enter the shared field law.
type ValidationPolicy struct {
	SHA256                  Digest
	SchemaVersion           uint32
	FlakeRatePrior          Rational
	FailureRepairConfidence Rational
	QuarantineThreshold     Rational
}

// Rational is an exact fraction with 0 < Numerator < Denominator. Equivalent
// fractions are not reduced: 1/20 and 2/40 remain distinct artifact spellings.
type Rational struct{ Numerator, Denominator uint64 }

// LoadValidationPolicy reads the exact bytes Core retains and binds by
// validation_policy_hash, not a working-tree settings file. The caller chooses
// the limits and verifies the manifest binding. Loading activates nothing.
// On any error it returns the zero ValidationPolicy.
func LoadValidationPolicy(data []byte, limits LoadLimits) (ValidationPolicy, error) {
	root, err := readArtifact(data, limits)
	if err != nil {
		return ValidationPolicy{}, err
	}
	if err := checkHead(root, true); err != nil {
		return ValidationPolicy{}, err
	}
	d := decoder{}
	m := d.object(root, "", "format", "schema_version", "flake_rate_prior", "failure_repair_confidence", "quarantine_threshold")
	result := ValidationPolicy{SchemaVersion: 1}
	result.FlakeRatePrior = d.rational(m["flake_rate_prior"], "/flake_rate_prior")
	result.FailureRepairConfidence = d.rational(m["failure_repair_confidence"], "/failure_repair_confidence")
	result.QuarantineThreshold = d.rational(m["quarantine_threshold"], "/quarantine_threshold")
	if d.err != nil {
		return ValidationPolicy{}, d.err
	}
	result.SHA256 = sha256.Sum256(data)
	return result, nil
}

func (d *decoder) rational(n node, path string) Rational {
	m := d.object(n, path, "numerator", "denominator")
	result := Rational{
		Numerator:   d.natural(m["numerator"], path+"/numerator", 1, ^uint64(0)),
		Denominator: d.natural(m["denominator"], path+"/denominator", 1, ^uint64(0)),
	}
	if result.Numerator >= result.Denominator {
		d.fail(path+"/numerator", "out_of_range")
	}
	return result
}
