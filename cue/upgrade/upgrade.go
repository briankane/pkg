/*
Copyright 2024 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package upgrade

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueast "cuelang.org/go/cue/ast"
	cueformat "cuelang.org/go/cue/format"
	cueparser "cuelang.org/go/cue/parser"
	"k8s.io/klog/v2"
)

// EnableCUEVersionCompatibility controls whether EnsureCueVersionCompatibility is applied at
// render time. Defaults to true. Can be disabled via --enable-cue-version-compatibility=false.
var EnableCUEVersionCompatibility = true

// GetCurrentVersion is a pluggable version provider. Consuming repos must set this at init
// time to return their own running version string (e.g. "v1.11.2"). If unset, all registered
// upgrades are applied (equivalent to "run everything").
//
// Example (in kubevela's main package):
//
//	func init() {
//	    upgrade.GetCurrentVersion = func() string { return version.VelaVersion }
//	}
var GetCurrentVersion func() string

// DefinitionKind identifies the type of definition for metrics, logs, and compatibility reports.
// It is an open string type — each consuming repo declares its own constants.
// Values should match the definition's Kind string for consistency, e.g. "Component", "Trait".
type DefinitionKind string

// TemplateArea identifies which part of a definition's CUE template a rewrite was applied to.
// It is an open string type — each consuming repo declares its own constants.
type TemplateArea string

// Version identifies a release version for upgrade ordering.
type Version struct {
	Major int
	Minor int
}

// String returns the canonical "Major.Minor" representation, e.g. "1.11".
func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Less reports whether v is strictly earlier than other.
func (v Version) Less(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	return v.Minor < other.Minor
}

// ParseVersion parses a "Major.Minor" string (with optional leading "v") into a Version.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimPrefix(s, "v")
	re := regexp.MustCompile(`^(\d+)\.(\d+)(?:\.\d+)?(?:[+-].*)?$`)
	m := re.FindStringSubmatch(s)
	if len(m) < 3 {
		return Version{}, fmt.Errorf("cannot parse version %q: expected Major.Minor format", s)
	}
	var v Version
	fmt.Sscanf(m[1], "%d", &v.Major)
	fmt.Sscanf(m[2], "%d", &v.Minor)
	return v, nil
}

// upgradeEntry is the internal interface satisfied by all registered upgrade types.
type upgradeEntry interface {
	id() string
	reason() string
	source() string
	versionLabel() string
	appliesToTarget(target Version) bool
	precheck() func(string) bool
	upgrade() func(string, *cueast.File) (string, error)
	velaVersion() Version
}

// KubeVelaUpgradeFunc is a CUE compatibility fix triggered by a KubeVela version.
// It applies when the running (or target) KubeVela version is >= VelaVersion.
// Register with RegisterUpgrade.
type KubeVelaUpgradeFunc struct {
	ID          string
	VelaVersion Version
	Reason      string
	Precheck    func(cueStr string) bool
	Upgrade     func(cueStr string, file *cueast.File) (string, error)
}

func (u KubeVelaUpgradeFunc) id() string           { return u.ID }
func (u KubeVelaUpgradeFunc) reason() string       { return u.Reason }
func (u KubeVelaUpgradeFunc) source() string       { return "kubevela" }
func (u KubeVelaUpgradeFunc) versionLabel() string { return u.VelaVersion.String() }
func (u KubeVelaUpgradeFunc) velaVersion() Version { return u.VelaVersion }
func (u KubeVelaUpgradeFunc) precheck() func(string) bool {
	return u.Precheck
}
func (u KubeVelaUpgradeFunc) upgrade() func(string, *cueast.File) (string, error) {
	return u.Upgrade
}
func (u KubeVelaUpgradeFunc) appliesToTarget(target Version) bool {
	v := u.VelaVersion
	return v.Less(target) || v == target
}

// CUEUpgradeFunc is a CUE compatibility fix triggered by the CUE language version.
// It applies when the running CUE language version (cue.LanguageVersion()) is >= CUEVersion,
// regardless of the KubeVela version. AssociatedVelaVersion is used only for registry ordering.
type CUEUpgradeFunc struct {
	ID                    string
	CUEVersion            Version
	AssociatedVelaVersion Version
	Reason                string
	Precheck              func(cueStr string) bool
	Upgrade               func(cueStr string, file *cueast.File) (string, error)
}

func (u CUEUpgradeFunc) id() string           { return u.ID }
func (u CUEUpgradeFunc) reason() string       { return u.Reason }
func (u CUEUpgradeFunc) source() string       { return "cue" }
func (u CUEUpgradeFunc) versionLabel() string { return u.CUEVersion.String() }
func (u CUEUpgradeFunc) velaVersion() Version { return u.AssociatedVelaVersion }
func (u CUEUpgradeFunc) precheck() func(string) bool {
	return u.Precheck
}
func (u CUEUpgradeFunc) upgrade() func(string, *cueast.File) (string, error) {
	return u.Upgrade
}
func (u CUEUpgradeFunc) appliesToTarget(_ Version) bool {
	current, err := ParseVersion(cue.LanguageVersion())
	if err != nil {
		return true // fail-open
	}
	return u.CUEVersion.Less(current) || u.CUEVersion == current
}

// UpgradeFunc is a type alias for KubeVelaUpgradeFunc, preserved for backward compatibility.
type UpgradeFunc = KubeVelaUpgradeFunc

// upgradeRegistry holds all registered upgrade entries, keyed by their associated KubeVela Version.
var upgradeRegistry = make(map[Version][]upgradeEntry)

// RegisterUpgrade registers an upgrade entry.
func RegisterUpgrade(u upgradeEntry) {
	if u.id() == "" {
		panic(fmt.Sprintf("upgrade.RegisterUpgrade: upgrade entry for version %s has empty ID", u.velaVersion()))
	}
	v := u.velaVersion()
	upgradeRegistry[v] = append(upgradeRegistry[v], u)
}

// sortedVersions returns all registered KubeVela versions in ascending order.
func sortedVersions() []Version {
	versions := make([]Version, 0, len(upgradeRegistry))
	for v := range upgradeRegistry {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Less(versions[j])
	})
	return versions
}

// latestSupportedVersion returns the highest registered KubeVela version.
func latestSupportedVersion() Version {
	vs := sortedVersions()
	return vs[len(vs)-1]
}

// getCurrentVersion resolves the running version via GetCurrentVersion, falling back to
// applying all registered upgrades (latest) if unset or unknown.
func getCurrentVersion() (Version, error) {
	var versionStr string
	if GetCurrentVersion != nil {
		versionStr = GetCurrentVersion()
	}
	if versionStr == "" || versionStr == "UNKNOWN" {
		latest := latestSupportedVersion()
		klog.InfoS("cue/upgrade: version is unknown or unset, applying all upgrades", "assumedVersion", latest)
		return latest, nil
	}
	v, err := ParseVersion(versionStr)
	if err != nil {
		return Version{}, fmt.Errorf("unable to parse version %q: %w", versionStr, err)
	}
	return v, nil
}

// appliedFix records a single upgrade fix that changed the template.
type appliedFix struct {
	id      string
	version string
}

// runUpgrades is the shared upgrade pipeline used by Upgrade and upgradeWithIDs.
// It normalizes cueStr, applies every registered fix that passes appliesToTarget
// and its precheck, and returns the final result together with metadata for each
// fix that produced a change. The caller is responsible for resolving target.
func runUpgrades(cueStr string, target Version) (result string, applied []appliedFix, err error) {
	if normalized, normErr := normalizeCUEWhitespace(cueStr); normErr == nil {
		cueStr = normalized
	}
	result = cueStr
	for _, v := range sortedVersions() {
		for _, u := range upgradeRegistry[v] {
			if !u.appliesToTarget(target) {
				continue
			}
			pc := u.precheck()
			if pc != nil && !pc(result) {
				continue
			}
			file, parseErr := cueparser.ParseFile("", result, cueparser.ParseComments)
			if parseErr != nil {
				return cueStr, nil, fmt.Errorf("failed to parse CUE for upgrade %s: %w", v, parseErr)
			}
			rewritten, upgradeErr := u.upgrade()(result, file)
			if upgradeErr != nil {
				return cueStr, nil, fmt.Errorf("failed to apply upgrade for version %s: %w", v, upgradeErr)
			}
			if rewritten != result {
				applied = append(applied, appliedFix{id: u.id(), version: v.String()})
			}
			result = rewritten
		}
	}
	if normalizedResult, normErr := normalizeCUEWhitespace(result); normErr == nil {
		result = normalizedResult
	}
	return result, applied, nil
}

// Upgrade applies all registered upgrades that apply to the given target version.
// If no targetVersion is provided, uses the version from GetCurrentVersion.
func Upgrade(cueStr string, targetVersion ...Version) (string, error) {
	var target Version
	var err error
	if len(targetVersion) > 0 {
		target = targetVersion[0]
	} else {
		target, err = getCurrentVersion()
		if err != nil {
			return "", err
		}
	}
	result, _, err := runUpgrades(cueStr, target)
	return result, err
}

// GetSupportedVersions returns all registered KubeVela versions in ascending order.
func GetSupportedVersions() []Version {
	return sortedVersions()
}

// normalizeCUEWhitespace parses and reformats a CUE string to canonical form.
func normalizeCUEWhitespace(cueStr string) (string, error) {
	f, err := cueparser.ParseFile("", cueStr, cueparser.ParseComments)
	if err != nil {
		return cueStr, err
	}
	b, err := cueformat.Node(f)
	if err != nil {
		return cueStr, err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

// upgradeWithIDs applies all registered upgrades and returns the rewritten string plus metadata
// for every fix that changed the template.
func upgradeWithIDs(cueStr string) (string, []appliedFix, error) {
	target, err := getCurrentVersion()
	if err != nil {
		return cueStr, nil, err
	}
	return runUpgrades(cueStr, target)
}

// OnRewrite is called by EnsureCueVersionCompatibility after a successful cache-miss upgrade.
// Consuming repos set this to record metrics (counter increments per applied fix).
// Signature: (fixID, fixVersion, defKind, area string)
var OnRewrite func(fixID, fixVersion string, defKind DefinitionKind, area TemplateArea)

// OnUpgradeDuration is called by EnsureCueVersionCompatibility after the upgrade path runs.
// Consuming repos set this to record latency metrics.
// Signature: (defKind string, elapsed time.Duration)
var OnUpgradeDuration func(defKind DefinitionKind, elapsed time.Duration)

// OnCacheEviction is called by the LRU cache when an entry is evicted.
// reason is "capacity" or "ttl".
var OnCacheEviction func(reason string)

// EnsureCueVersionCompatibility applies all upgrades for the current version to the provided
// CUE string, ensuring backward compatibility with legacy CUE syntax.
// Returns the upgraded template and whether any semantic upgrades were applied.
func EnsureCueVersionCompatibility(cueStr, defName string, defKind DefinitionKind, area TemplateArea) (string, bool) {
	if !EnableCUEVersionCompatibility {
		return cueStr, false
	}

	key := templateHash(cueStr)
	start := time.Now()

	cache := compatCache.Load()
	if entry, ok := cache.get(key); ok {
		if !entry.requiresUpgrade {
			klog.V(4).InfoS("cue/upgrade: skip (already compatible)", "definition", defName)
			return entry.upgraded, false
		}
		klog.V(4).InfoS("cue/upgrade: cache hit (upgraded template)", "definition", defName)
		return entry.upgraded, true
	}

	upgraded, applied, err := upgradeWithIDs(cueStr)
	elapsed := time.Since(start)
	if OnUpgradeDuration != nil {
		OnUpgradeDuration(defKind, elapsed)
	}

	if err != nil {
		klog.InfoS("cue/upgrade: skipping compatibility upgrade (fail-open)", "definition", defName, "err", err, "elapsed", elapsed)
		return cueStr, false
	}

	wasUpgraded := len(applied) > 0
	if wasUpgraded {
		cache.put(key, compatEntry{requiresUpgrade: true, upgraded: upgraded})
		if OnRewrite != nil {
			for _, fix := range applied {
				OnRewrite(fix.id, fix.version, defKind, area)
			}
		}
		klog.InfoS("cue/upgrade: applied CUE version compatibility fixes", "definition", defName, "elapsed", elapsed)
	} else {
		cache.put(key, compatEntry{requiresUpgrade: false, upgraded: upgraded})
		klog.V(4).InfoS("cue/upgrade: no compatibility fixes needed", "definition", defName, "elapsed", elapsed)
	}

	return upgraded, wasUpgraded
}

// RequiresUpgrade checks if the CUE string requires upgrading to the target version.
// If no targetVersion is provided, uses the version from GetCurrentVersion.
func RequiresUpgrade(cueStr string, targetVersion ...Version) (bool, []string, error) {
	var target Version
	var err error

	if len(targetVersion) > 0 {
		target = targetVersion[0]
	} else {
		target, err = getCurrentVersion()
		if err != nil {
			return false, nil, err
		}
	}

	normalized, err := normalizeCUEWhitespace(cueStr)
	if err == nil {
		cueStr = normalized
	}

	var allReasons []string

	for _, v := range sortedVersions() {
		for _, u := range upgradeRegistry[v] {
			if !u.appliesToTarget(target) {
				continue
			}
			pc := u.precheck()
			if pc != nil && !pc(cueStr) {
				continue
			}
			file, parseErr := cueparser.ParseFile("", cueStr, cueparser.ParseComments)
			if parseErr != nil {
				return false, nil, fmt.Errorf("failed to parse CUE for version %s: %w", v, parseErr)
			}
			result, err := u.upgrade()(cueStr, file)
			if err != nil {
				return false, nil, fmt.Errorf("failed to check upgrade for version %s: %w", v, err)
			}
			if result != cueStr {
				allReasons = append(allReasons, fmt.Sprintf("[%s@%s] [%s] %s", u.source(), u.versionLabel(), u.id(), u.reason()))
			}
		}
	}

	return len(allReasons) > 0, allReasons, nil
}
