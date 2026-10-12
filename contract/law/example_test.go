package law_test

import (
	"fmt"
	"os"

	"github.com/Darius-PJ/dwarf-harness/contract/law"
)

func ExampleLoadValidationPolicy() {
	// This fixture stands for the exact bytes Core retained under the active
	// manifest's validation_policy_hash, not a mutable working-tree setting.
	data, err := os.ReadFile("testdata/validation-policy/v1/policy.json")
	if err != nil {
		panic(err)
	}
	policy, err := law.LoadValidationPolicy(data, law.StandardV1())
	if err != nil {
		panic(err)
	}
	// A consumer checks policy.SHA256 == the manifest's validation_policy_hash.
	// field_law_sha256 and every other manifest member come from Core's
	// activation record. Reading this policy never loads the field engine.
	fmt.Printf("flake_rate_prior: %d/%d\n", policy.FlakeRatePrior.Numerator, policy.FlakeRatePrior.Denominator)
	fmt.Printf("failure_repair_confidence: %d/%d\n", policy.FailureRepairConfidence.Numerator, policy.FailureRepairConfidence.Denominator)
	fmt.Printf("quarantine_threshold: %d/%d\n", policy.QuarantineThreshold.Numerator, policy.QuarantineThreshold.Denominator)
	fmt.Printf("validation_policy_hash: %s\n", policy.SHA256)
	// Output:
	// flake_rate_prior: 1/20
	// failure_repair_confidence: 999/1000
	// quarantine_threshold: 1/5
	// validation_policy_hash: 1f3c66a9eca49f82e0e224c8f7a5edaee890757b6281a035f2e9a1f543f4f447
}

func ExampleLoad() {
	data, err := os.ReadFile("testdata/law/v1/full.json")
	if err != nil {
		panic(err)
	}
	field, err := law.Load(data, law.StandardV1())
	if err != nil {
		panic(err)
	}
	fmt.Printf("field_law_sha256: %s\n", field.SHA256)
	for _, kind := range field.Kinds {
		fmt.Printf("%s: %d sources\n", kind.ID, len(kind.Sources))
	}
	// Output:
	// field_law_sha256: 3b148a28c5cd88445c3cd43b53e25cb99b8e9009e57ab7416e7293469134895c
	// affordance: 3 sources
	// dependency_strain: 3 sources
	// epistemic_uncertainty: 6 sources
	// perturbation: 3 sources
	// resource_pressure: 4 sources
	// surprise: 3 sources
}
