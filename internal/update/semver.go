package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed release tag such as v0.1.0 or v0.1.0-beta.2, or the
// version of a build made from source some commits after one.
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
	// Ahead is how many commits after the tag a source build was made. Zero
	// for a released binary.
	Ahead int
}

var versionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

// sourceBuildRe matches what "git describe" adds to a tag for a build made
// after it: the number of commits since, the commit, and whether the tree had
// uncommitted changes. v0.1.0-3-g181b7e9 is three commits after v0.1.0.
var sourceBuildRe = regexp.MustCompile(`-(\d+)-g[0-9a-f]{7,40}(?:-dirty)?$`)

// ParseVersion parses a release tag, or the version of a build made from
// source after one. The leading "v" is optional.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	var v Version
	if loc := sourceBuildRe.FindStringSubmatchIndex(s); loc != nil {
		v.Ahead, _ = strconv.Atoi(s[loc[2]:loc[3]])
		s = s[:loc[0]]
	} else {
		s = strings.TrimSuffix(s, "-dirty")
	}
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("not a release version: %q", s)
	}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
	}
	return v, nil
}

// IsPrerelease reports whether the version has a pre-release part.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Compare returns -1, 0 or 1 by semantic-version precedence: a pre-release
// sorts before the release it leads up to. A build made from source after a
// tag is newer than that tag, so it is never offered the tag as an update.
func (v Version) Compare(o Version) int {
	if c := v.compareTag(o); c != 0 {
		return c
	}
	return sign(v.Ahead - o.Ahead)
}

// compareTag compares the tags two versions were built from.
func (v Version) compareTag(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			return sign(d[0] - d[1])
		}
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		a, b := v.Pre[i], o.Pre[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if a != b {
				return sign(strings.Compare(a, b))
			}
		}
	}
	return sign(len(v.Pre) - len(o.Pre))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// IsPrereleaseTag reports whether a tag names a pre-release. Any tag with a
// hyphen counts, matching how releases are published.
func IsPrereleaseTag(tag string) bool {
	return strings.Contains(tag, "-")
}
