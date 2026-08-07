// Copyright 2026 The KubeVela Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package util

#Truncate: {
	#do:       "truncate"
	#provider: "util"

	$params: {
		// +usage=The raw desired value
		value: string
		// +usage=The hard length cap (counted in runes), applied to the whole result
		maxLength: int
		// +usage=Delimiter joining the prefix, truncated base, and hash suffix
		delimiter: *"-" | string
		// +usage=Number of hex chars in the uniqueness suffix, at most 64 (the width of a sha256 digest)
		hashLength: *8 | int & <=64
		// +usage=Prefix always preserved verbatim and counted against maxLength
		prefix: *"" | string
		// +usage=Trim the base back to the last delimiter so it never ends in a partial segment
		preserveSegments: *false | bool
	}

	// +usage=The result of this action, filled in after the action is executed
	$returns: {
		// +usage=The value, guaranteed <= maxLength runes
		value?: string
		...
	}
}
