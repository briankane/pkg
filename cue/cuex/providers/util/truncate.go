/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kubevela/pkg/cue/cuex/providers"
)

// maxHashLen is the number of hex chars a sha256 digest renders to, and thus
// the largest usable HashLength.
const maxHashLen = sha256.Size * 2

// TruncateInput are the input parameters for #Truncate.
type TruncateInput struct {
	// Value is the raw desired value. Always pass the raw value, never a value
	// this provider already returned: a previous result carries Prefix, and
	// re-supplying Prefix alongside it applies the prefix twice. See Truncate.
	Value string `json:"value"`
	// MaxLength is the hard length cap (counted in runes) applied to the whole result.
	MaxLength int `json:"maxLength"`
	// Delimiter joins the prefix, truncated base, and hash suffix. Defaults to "-".
	Delimiter string `json:"delimiter"`
	// HashLength is the number of hex chars in the uniqueness suffix. Defaults to 8.
	HashLength int `json:"hashLength"`
	// Prefix is always preserved verbatim and counts against MaxLength. Defaults to "".
	Prefix string `json:"prefix"`
	// PreserveSegments, when true, trims the base back to the last delimiter so a
	// truncated name never ends in a partial segment. Defaults to false.
	PreserveSegments bool `json:"preserveSegments"`
}

// TruncateOutput is the output of #Truncate.
type TruncateOutput struct {
	// Value is guaranteed to be <= MaxLength runes.
	Value string `json:"value"`
}

// TruncateParams is the provider input wrapper ($params).
type TruncateParams = providers.Params[TruncateInput]

// TruncateReturns is the provider output wrapper ($returns).
type TruncateReturns = providers.Returns[TruncateOutput]

// Truncate fits Value within MaxLength. If Value already fits it is returned
// unchanged. Otherwise the base is truncated (rune-safe) to leave room for a
// delimiter and a hash of the ORIGINAL value, guaranteeing that distinct inputs
// sharing a truncated prefix do not collide.
//
// An optional Prefix is always preserved and counts against MaxLength. When
// PreserveSegments is set, the base is trimmed back to the last delimiter so the
// result never ends in a partial segment; if the kept base contains no
// delimiter (a single long word), it falls back to the plain character cut
// rather than dropping all human-readable context.
//
// Truncate is deterministic: identical inputs always yield an identical result,
// which is what callers re-rendering on every reconcile rely on. It is NOT
// closed over its own output when Prefix is set -- feeding a returned value
// back in as Value while supplying the same Prefix applies the prefix again
// ("prod-web-<hash>" becomes "prod-prod-web-<hash>"), because a prefix this
// function added is indistinguishable from a value that genuinely starts with
// one. Always render from the raw value.
func Truncate(_ context.Context, params *TruncateParams) (*TruncateReturns, error) {
	p := params.Params

	sep := p.Delimiter
	if sep == "" {
		sep = "-"
	}
	hashLen := p.HashLength
	if hashLen <= 0 {
		hashLen = 8
	}
	// The suffix is sliced out of a hex-encoded sha256 digest, which is exactly
	// maxHashLen chars. Reject anything longer up-front -- as with the prefix
	// check below, an impossible configuration should fail the same way whether
	// or not the name happens to be long enough to trigger truncation.
	if hashLen > maxHashLen {
		return nil, fmt.Errorf("hashLength %d exceeds the maximum of %d hex chars in a sha256 digest", hashLen, maxHashLen)
	}
	sepLen := len([]rune(sep))

	// The prefix is always kept and consumes part of the budget. It is joined to
	// the fitted value with the delimiter only when present.
	valueBudget := p.MaxLength - len([]rune(p.Prefix))
	if p.Prefix != "" {
		valueBudget -= sepLen
	}

	// The prefix is emitted verbatim and is never truncated, so it must leave
	// room within MaxLength for its joining delimiter and at least one rune of
	// the fitted value. Otherwise honoring the prefix would blow the cap; error
	// out clearly up-front rather than only when the value happens to be long.
	if p.Prefix != "" && valueBudget < 1 {
		return nil, fmt.Errorf("prefix %q (%d runes) plus delimiter %q leaves no room within maxLength %d", p.Prefix, len([]rune(p.Prefix)), sep, p.MaxLength)
	}

	fittedValue := p.Value
	if len([]rune(p.Value)) > valueBudget {
		if valueBudget < hashLen+sepLen+1 {
			return nil, fmt.Errorf("maxLength %d too small to fit prefix %q, a %d-char hash suffix, and delimiter %q", p.MaxLength, p.Prefix, hashLen, sep)
		}

		sum := sha256.Sum256([]byte(p.Value))
		hash := hex.EncodeToString(sum[:])[:hashLen]

		keep := valueBudget - hashLen - sepLen
		base := string([]rune(p.Value)[:keep])
		if p.PreserveSegments {
			if idx := strings.LastIndex(base, sep); idx >= 0 {
				// Cutting at the last delimiter can still leave a trailing one
				// when the value contains a run of them ("foo--bar" -> "foo-"),
				// so trim as in the fallback path rather than emitting a
				// doubled delimiter once the hash is joined on.
				base = base[:idx]
			}
		}
		base = trimTrailingSep(base, sep)

		fittedValue = hash
		if base != "" {
			fittedValue = base + sep + hash
		}
	}

	result := fittedValue
	if p.Prefix != "" {
		result = p.Prefix + sep + fittedValue
	}
	return &TruncateReturns{Returns: TruncateOutput{Value: result}}, nil
}

// trimTrailingSep strips every trailing occurrence of the exact separator
// substring, so that joining base + sep + hash never doubles the delimiter.
//
// The delimiter is atomic everywhere else in this function (sepLen counts its
// runes, and it is joined as a unit), so a cutset trim such as
// strings.TrimRight would over-trim: with a "-_" delimiter it strips a base's
// trailing "_", a rune that is not a trailing delimiter at all.
func trimTrailingSep(s, sep string) string {
	if sep == "" {
		return s
	}
	for strings.HasSuffix(s, sep) {
		s = strings.TrimSuffix(s, sep)
	}
	return s
}
