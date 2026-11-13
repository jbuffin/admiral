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

// OwnsRef is whether ref is a PR a listed ship holds or an issue a listed ship claims, spelled however GitHub spells it.
// Ships push under the engineer's own login, so only the ref can tell their work apart; a PR an underway ship just
// opened is not in PRs until it moors.
func OwnsRef(list []Ship, ref string) bool {
	if k, ok := ClaimKey(ref); ok {
		if _, held := ClaimsOf(list)[k]; held {
			return true
		}
	}
	k := prKey(ref)
	if k == "" {
		return false
	}
	for _, sh := range list {
		for _, pr := range sh.PRs {
			if prKey(pr.URL) == k {
				return true
			}
		}
	}
	return false
}
