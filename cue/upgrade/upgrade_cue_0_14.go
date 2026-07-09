/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package upgrade

import (
	"regexp"
	"strings"
)

// All fixes in this file address breaking changes introduced in CUE v0.14.
// They shipped with KubeVela v1.11, which was the first release to require CUE >= v0.14.
var v1_11 = Version{Major: 1, Minor: 11}

// errorFieldLabelRe matches an unquoted `error` used as a field label.
var errorFieldLabelRe = regexp.MustCompile(`\berror\s*[?!]?\s*:`)

func init() {
	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "list-arithmetic",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                "contains deprecated list operators (+ or *) that need upgrading to list.Concat() or list.Repeat()",
		Precheck:              func(s string) bool { return strings.Contains(s, "+") || strings.Contains(s, "*") },
		Upgrade:               upgradeListConcatenation,
	})

	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "error-field-label",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                `contains field named 'error' which conflicts with the CUE 0.14 built-in; must be quoted as "error"`,
		Precheck:              func(s string) bool { return errorFieldLabelRe.MatchString(s) },
		Upgrade:               upgradeErrorFieldLabel,
	})

	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "bool-default-guard-hazard",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                "contains a `bool | *false` or `bool | *true` field used in an if-guard; CUE v0.14+ reads the default before unification, causing incorrect evaluation",
		Precheck: func(s string) bool {
			return (strings.Contains(s, "bool | *false") || strings.Contains(s, "bool | *true")) &&
				strings.Contains(s, "if ")
		},
		Upgrade: upgradeBoolDefaultNegation,
	})

	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "generic-default-guard-hazard",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                "contains non-bool default-read guard hazards that require sentinel rewrite for CUE v0.14+ evaluation compatibility",
		Precheck:              func(s string) bool { return strings.Contains(s, "*") && strings.Contains(s, "if ") },
		Upgrade:               upgradeGenericDefaultGuardCompat,
	})

	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "keepvalidators-singleton",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                "contains singleton keepvalidators intersections (e.g. >=x & <=x) that must be concretized for CUE v0.14+ compatibility",
		Precheck: func(s string) bool {
			return strings.Contains(s, ">=") && strings.Contains(s, "<=") && strings.Contains(s, "&")
		},
		Upgrade: upgradeKeepValidatorsSingletonCompat,
	})

	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "evalv3-selfref-default-guard",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                "contains self-referential guards inside defaulted disjunctions that require rewrite for evalv3 validation compatibility",
		Precheck:              func(s string) bool { return strings.Contains(s, "*") && strings.Contains(s, "if ") },
		Upgrade:               upgradeEvalv3SelfRefGuardCompat,
	})
}
