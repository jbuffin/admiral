package ships

import (
	"net/url"
	"strconv"
	"strings"
)

// prKey is a lower-cased https://host/owner/repo/pull/N, or "" for a URL that is not a pull request.
func prKey(ref string) string {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(p) < 4 || p[2] != "pull" {
		return ""
	}
	if _, err := strconv.Atoi(p[3]); err != nil {
		return ""
	}
	return strings.ToLower("https://" + u.Hostname() + "/" + p[0] + "/" + p[1] + "/pull/" + p[3])
}

// RefHolder is the listed ship that holds ref, spelled however GitHub spells it: an issue it claims, or a PR in its PRs or
// its order URL. Ships push under the engineer's own login, so only the ref can tell their work apart; a PR an underway
// ship just opened is not in PRs until it moors.
func RefHolder(list []Ship, ref string) (Ship, bool) {
	if k, ok := ClaimKey(ref); ok {
		sh, held := claimingShips(list)[k]
		return sh, held
	}
	k := prKey(ref)
	if k == "" {
		return Ship{}, false
	}
	for _, sh := range list {
		if prKey(sh.URL) == k {
			return sh, true
		}
		for _, pr := range sh.PRs {
			if prKey(pr.URL) == k {
				return sh, true
			}
		}
	}
	return Ship{}, false
}
