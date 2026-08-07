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

package util_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/kubevela/pkg/cue/cuex/providers/util"
)

func run(t *testing.T, in util.TruncateInput) (string, error) {
	t.Helper()
	ret, err := util.Truncate(context.Background(), &util.TruncateParams{Params: in})
	if err != nil {
		return "", err
	}
	return ret.Returns.Value, nil
}

func TestTruncate_FitsUnchanged(t *testing.T) {
	out, err := run(t, util.TruncateInput{Value: "short-name", MaxLength: 20, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	require.Equal(t, "short-name", out)
}

func TestTruncate_ExactLengthUnchanged(t *testing.T) {
	out, err := run(t, util.TruncateInput{Value: "exactly-eleven", MaxLength: 14, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	require.Equal(t, "exactly-eleven", out)
}

func TestTruncate_TooLongIsTruncatedAndHashed(t *testing.T) {
	name := "my-really-long-service-name-frontend"
	out, err := run(t, util.TruncateInput{Value: name, MaxLength: 20, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 20)
	// base(11) + "-" + hash(8) == 20
	require.Equal(t, 20, len([]rune(out)))
	require.True(t, strings.HasPrefix(out, "my-really-l"), "got %q", out)
	// suffix is "-" + 8 lowercase-hex chars
	require.Regexp(t, `-[0-9a-f]{8}$`, out)
}

func TestTruncate_Deterministic(t *testing.T) {
	in := util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 20, Delimiter: "-", HashLength: 8}
	a, err := run(t, in)
	require.NoError(t, err)
	b, err := run(t, in)
	require.NoError(t, err)
	require.Equal(t, a, b)
}

func TestTruncate_NoCollisionForSharedPrefix(t *testing.T) {
	max := 20
	a, err := run(t, util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: max, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	b, err := run(t, util.TruncateInput{Value: "my-really-long-service-name-backend", MaxLength: max, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	// same truncated base, different original -> different result
	require.NotEqual(t, a, b)
}

func TestTruncate_Idempotent(t *testing.T) {
	name := "my-really-long-service-name-frontend"
	once, err := run(t, util.TruncateInput{Value: name, MaxLength: 20, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	twice, err := run(t, util.TruncateInput{Value: once, MaxLength: 20, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	require.Equal(t, once, twice)
}

func TestTruncate_MultibyteCapIsCountedInRunes(t *testing.T) {
	// 10 emoji = 10 runes but 40 bytes. Against MaxLength 12 the name FITS in
	// runes, so it must come back untouched; a byte-counting implementation
	// would see 40 > 12 and truncate. This covers the cap comparison only --
	// the rune-splitting cut is covered by the test below.
	name := strings.Repeat("😀", 10)
	out, err := run(t, util.TruncateInput{Value: name, MaxLength: 12, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	require.Equal(t, name, out)
}

func TestTruncate_MultibyteTruncationDoesNotSplitRunes(t *testing.T) {
	// 20 emoji against MaxLength 14 actually enters the truncation branch.
	// keep = 14 - 4 - 1 = 9, so the base must be exactly 9 whole emoji and the
	// result 9 emoji + "-" + 4 hex chars.
	name := strings.Repeat("😀", 20)
	out, err := run(t, util.TruncateInput{Value: name, MaxLength: 14, Delimiter: "-", HashLength: 4})
	require.NoError(t, err)

	// Truncation really happened.
	require.NotEqual(t, name, out)
	require.Regexp(t, `-[0-9a-f]{4}$`, out)

	// No rune was cut in half: a byte-wise slice of 4-byte emoji would leave a
	// partial sequence, which decodes to U+FFFD and fails ValidString.
	require.True(t, utf8.ValidString(out), "output is not valid UTF-8: %q", out)
	require.NotContains(t, out, "�")

	// The kept base is a whole-rune count, and the budget is spent exactly.
	base := strings.TrimSuffix(out[:strings.LastIndex(out, "-")], "-")
	require.Equal(t, strings.Repeat("😀", 9), base)
	require.Equal(t, 9, utf8.RuneCountInString(base))
	require.Equal(t, 14, len([]rune(out)))
	require.Equal(t, 9*4+1+4, len(out)) // 41 bytes: 9 emoji + "-" + 4 hex
}

func TestTruncate_MaxLengthTooSmall(t *testing.T) {
	_, err := run(t, util.TruncateInput{Value: "way-too-long-name", MaxLength: 8, Delimiter: "-", HashLength: 8})
	require.Error(t, err)
	require.Contains(t, err.Error(), "too small")
}

func TestTruncate_DefaultsWhenZeroValued(t *testing.T) {
	// delimiter "" and hashLength 0 should fall back to "-" and 8
	out, err := run(t, util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 20})
	require.NoError(t, err)
	require.Regexp(t, `-[0-9a-f]{8}$`, out)
}

func TestTruncate_AllTrimmedBaseYieldsHashOnly(t *testing.T) {
	// base collapses to empty after trailing-delimiter trim -> result is just the hash
	out, err := run(t, util.TruncateInput{Value: "----------------------", MaxLength: 12, Delimiter: "-", HashLength: 8})
	require.NoError(t, err)
	require.Regexp(t, `^[0-9a-f]{8}$`, out)
}

func TestTruncate_PreserveSegmentsTrimsToDelimiter(t *testing.T) {
	// Default (preserveSegments=false) leaves the partial segment "l".
	hard, err := run(t, util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 20})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(hard, "my-really-l"), "got %q", hard)

	// preserveSegments=true trims back to the last delimiter -> no dangling "l".
	out, err := run(t, util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 20, PreserveSegments: true})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 20)
	require.True(t, strings.HasPrefix(out, "my-really-"), "got %q", out)
	require.False(t, strings.HasPrefix(out, "my-really-l"), "got %q", out)
	require.Regexp(t, `^my-really-[0-9a-f]{8}$`, out)
}

func TestTruncate_PreserveSegmentsFallsBackWhenNoDelimiter(t *testing.T) {
	// A single long word has no delimiter in the kept base -> fall back to the
	// plain character cut rather than dropping all human-readable context.
	name := "supercalifragilisticexpialidocious"
	out, err := run(t, util.TruncateInput{Value: name, MaxLength: 20, PreserveSegments: true})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 20)
	require.True(t, strings.HasPrefix(out, "super"), "got %q", out)
	require.Regexp(t, `-[0-9a-f]{8}$`, out)
}

func TestTruncate_PrefixPreservedAndCounted(t *testing.T) {
	out, err := run(t, util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 24, Prefix: "prod"})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 24)
	require.True(t, strings.HasPrefix(out, "prod-"), "got %q", out)
	require.Regexp(t, `^prod-.*-[0-9a-f]{8}$`, out)
}

func TestTruncate_PrefixWithShortNameUnchanged(t *testing.T) {
	// Value already fits within the prefix-adjusted budget -> kept verbatim.
	out, err := run(t, util.TruncateInput{Value: "web", MaxLength: 20, Prefix: "prod"})
	require.NoError(t, err)
	require.Equal(t, "prod-web", out)
}

func TestTruncate_PrefixTooLongErrors(t *testing.T) {
	// Prefix consumes so much of the cap that no hash suffix fits.
	_, err := run(t, util.TruncateInput{Value: "way-too-long-name", MaxLength: 14, Prefix: "prod"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "too small")
}

func TestTruncate_PrefixLongerThanMaxLengthErrors(t *testing.T) {
	// The prefix is emitted verbatim and can never be truncated, so a prefix
	// longer than maxLength must error rather than silently blow the cap --
	// regardless of the name's length.
	for _, name := range []string{"some-service-name", "x", ""} {
		_, err := run(t, util.TruncateInput{Value: name, MaxLength: 10, Prefix: "veryLongPrefixValue"})
		require.Error(t, err, "name=%q", name)
		require.Contains(t, err.Error(), "no room", "name=%q", name)
	}
}

func TestTruncate_PrefixLeavesNoRoomForNameErrors(t *testing.T) {
	// prefix + delimiter must leave room for at least one rune of the name.
	// "abcdefghi" (9) + "-" == maxLength 10 -> zero budget -> error (no
	// dangling-delimiter output).
	_, err := run(t, util.TruncateInput{Value: "", MaxLength: 10, Prefix: "abcdefghi"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no room")
}

func TestTruncate_PrefixNeverTruncated(t *testing.T) {
	// When the prefix does fit, it is preserved verbatim and the whole result
	// stays within the cap.
	out, err := run(t, util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 24, Prefix: "prod"})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 24)
	require.True(t, strings.HasPrefix(out, "prod-"), "prefix must be intact, got %q", out)
}

func TestTruncate_SameRawInputIsStable(t *testing.T) {
	// The property a reconcile loop depends on: rendering the same raw inputs
	// repeatedly yields the same name, prefix included. Renders must always
	// start from the raw name, not from a previously rendered result.
	in := util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 24, Prefix: "prod"}
	first, err := run(t, in)
	require.NoError(t, err)
	second, err := run(t, in)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestTruncate_PrefixedOutputIsNotValidInput(t *testing.T) {
	// Value is the RAW desired value; the output is a rendered result. Passing a
	// prefixed result back in while supplying Prefix again applies the prefix a
	// second time, by design: the function cannot tell a prefix it added from a
	// value that genuinely begins with one (Value "prod-api" + Prefix "prod" is
	// a real request for "prod-prod-api"). Pinned so the contract is explicit and
	// nobody "fixes" it with a prefix-stripping heuristic that would corrupt
	// that legitimate case.
	in := util.TruncateInput{Value: "my-really-long-service-name-frontend", MaxLength: 24, Prefix: "prod"}
	once, err := run(t, in)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(once, "prod-"), "got %q", once)
	require.False(t, strings.HasPrefix(once, "prod-prod-"), "got %q", once)

	in.Value = once
	twice, err := run(t, in)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(twice, "prod-prod-"), "re-entry doubles the prefix, got %q", twice)
	// The length cap is still honoured, so this degrades the name but never
	// produces an over-long one.
	require.LessOrEqual(t, len([]rune(twice)), 24)
}

func TestTruncate_PreserveSegmentsNoDoubledDelimiter(t *testing.T) {
	// A run of delimiters means the cut at the last one still leaves a trailing
	// delimiter ("foo--bar" -> "foo-"); joining the hash must not double it.
	out, err := run(t, util.TruncateInput{Value: "foo--barbazquxlongtail", MaxLength: 20, Delimiter: "-", HashLength: 8, PreserveSegments: true})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 20)
	require.Regexp(t, `^foo-[0-9a-f]{8}$`, out)
}

func TestTruncate_PreserveSegmentsDotDelimiterLeavesNoEmptyLabel(t *testing.T) {
	// The same defect with a "." delimiter yielded "foo..<hash>", an empty DNS
	// label that RFC 1123 rejects.
	out, err := run(t, util.TruncateInput{Value: "foo..barbazquxlongtail", MaxLength: 20, Delimiter: ".", HashLength: 8, PreserveSegments: true})
	require.NoError(t, err)
	require.NotContains(t, out, "..")
	require.Regexp(t, `^foo\.[0-9a-f]{8}$`, out)
}

func TestTruncate_PreserveSegmentsAllDelimitersYieldsHashOnly(t *testing.T) {
	// Base is nothing but delimiters -> trims to empty -> hash alone, matching
	// the non-preserveSegments path rather than emitting a leading delimiter.
	out, err := run(t, util.TruncateInput{Value: "----------------------", MaxLength: 12, Delimiter: "-", HashLength: 8, PreserveSegments: true})
	require.NoError(t, err)
	require.Regexp(t, `^[0-9a-f]{8}$`, out)
}

func TestTruncate_MultiRuneDelimiterTrimmedAtomically(t *testing.T) {
	// keep = 20 - 8 - 2 = 10 -> base "abcdefghi_". The trailing "_" is a member
	// of the "-_" delimiter's character set but is not a trailing delimiter, so
	// it must survive: a cutset trim would drop it.
	out, err := run(t, util.TruncateInput{Value: "abcdefghi_morestuffhere", MaxLength: 20, Delimiter: "-_", HashLength: 8})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 20)
	require.Regexp(t, `^abcdefghi_-_[0-9a-f]{8}$`, out)
}

func TestTruncate_MultiRuneDelimiterTrimmedAtomicallyPreserveSegments(t *testing.T) {
	// Same input on the preserveSegments fallback path (no "-_" occurs in the
	// kept base), which trims through the same helper.
	out, err := run(t, util.TruncateInput{Value: "abcdefghi_morestuffhere", MaxLength: 20, Delimiter: "-_", HashLength: 8, PreserveSegments: true})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 20)
	require.Regexp(t, `^abcdefghi_-_[0-9a-f]{8}$`, out)
}

func TestTruncate_MultiRuneDelimiterActuallyTrailingIsTrimmed(t *testing.T) {
	// keep = 22 - 8 - 2 = 12 -> base "abcdefgh-_-_", which really does end in
	// the delimiter (twice). Both occurrences go, leaving no doubled delimiter.
	out, err := run(t, util.TruncateInput{Value: "abcdefgh-_-_morestuffhere", MaxLength: 22, Delimiter: "-_", HashLength: 8})
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(out)), 22)
	require.Regexp(t, `^abcdefgh-_[0-9a-f]{8}$`, out)
}

func TestTruncate_HashLengthAboveDigestWidthErrors(t *testing.T) {
	// A sha256 digest is 64 hex chars; a larger hashLength used to slice past
	// the end of that string and panic. maxLength here is deliberately wide
	// enough to clear the "too small" guard and reach the slice.
	_, err := run(t, util.TruncateInput{Value: strings.Repeat("a", 300), MaxLength: 200, HashLength: 100})
	require.Error(t, err)
	require.Contains(t, err.Error(), "hashLength 100")
}

func TestTruncate_HashLengthAboveDigestWidthErrorsEvenWhenNameFits(t *testing.T) {
	// The configuration is impossible regardless of the name, so it fails
	// up-front rather than only when the name is long enough to be truncated.
	_, err := run(t, util.TruncateInput{Value: "short", MaxLength: 200, HashLength: 100})
	require.Error(t, err)
	require.Contains(t, err.Error(), "hashLength 100")
}

func TestTruncate_HashLengthAtDigestWidthIsAllowed(t *testing.T) {
	// 64 is the boundary and must still work: base(15) + "-" + hash(64) == 80.
	out, err := run(t, util.TruncateInput{Value: strings.Repeat("a", 300), MaxLength: 80, HashLength: 64})
	require.NoError(t, err)
	require.Equal(t, 80, len([]rune(out)))
	require.Regexp(t, `-[0-9a-f]{64}$`, out)
}

func TestPackage_RegistersTruncate(t *testing.T) {
	// util.Package must be constructed (NewInternalPackage did not error) and
	// expose the "truncate" provider function under the "util" name.
	require.NotNil(t, util.Package)
	require.Equal(t, "util", util.Package.GetName())
	require.NotNil(t, util.Package.GetProviderFn("truncate"))
}
