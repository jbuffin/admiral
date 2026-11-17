package update

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// Nine digits keep a part inside int on any platform.
	versionRe  = regexp.MustCompile(`^(\d{1,9})\.(\d{1,9})\.(\d{1,9})$`)
	describeRe = regexp.MustCompile(`-\d+-g[0-9a-f]+(-dirty)?$`)
)

func validVersion(v string) bool { return versionRe.MatchString(v) }

func parts(v string) ([3]int, bool) {
	var out [3]int
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return out, false
	}
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// isDescribe is whether v is a git describe version from a build in a checkout.
func isDescribe(v string) bool { return describeRe.MatchString(v) }

// Available is whether latest (a bare x.y.z) is newer than the running version's base; unknown running versions give false.
func Available(running, latest string) bool {
	run, ok := parts(describeRe.ReplaceAllString(strings.TrimSpace(running), ""))
	if !ok {
		return false
	}
	lat, ok := parts(latest)
	if !ok {
		return false
	}
	for i := range run {
		if lat[i] != run[i] {
			return lat[i] > run[i]
		}
	}
	return false
}
