package webtier

import (
	"fmt"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Role slugs and labels. The slug map mirrors aggmodel.roleSlugs, which is what
// the route table and the artifact file names both use, so /tier-list/mid and
// matchups/mid.json cannot drift apart.
//
// It is restated rather than derived because the generated artifact types carry
// the enum values but not their URL spelling, and because a missing slug should
// be an error rather than a silent empty segment in a link.

// Roles is the canonical role order, best-known-first as the aggregator emits
// it.
var Roles = []aggmodel.Role{
	aggmodel.RoleTop,
	aggmodel.RoleJungle,
	aggmodel.RoleMid,
	aggmodel.RoleBottom,
	aggmodel.RoleSupport,
}

var roleLabels = map[aggmodel.Role]string{
	aggmodel.RoleTop:     "Top",
	aggmodel.RoleJungle:  "Jungle",
	aggmodel.RoleMid:     "Mid",
	aggmodel.RoleBottom:  "Bottom",
	aggmodel.RoleSupport: "Support",
}

// RoleSlug returns the URL segment for a role. The spelling lives in
// aggmodel, which is also what names the artifact files, so a route segment and
// a file name cannot drift apart.
func RoleSlug(role aggmodel.Role) (string, error) {
	if slug := role.Slug(); slug != "" {
		return slug, nil
	}
	return "", fmt.Errorf("no URL slug for role %q", string(role))
}

// RoleLabel returns the display label for a role.
func RoleLabel(role aggmodel.Role) string {
	if label, ok := roleLabels[role]; ok {
		return label
	}
	return string(role)
}

// RoleFromSlug resolves a URL segment back to a role.
func RoleFromSlug(slug string) (aggmodel.Role, bool) {
	for _, role := range Roles {
		if role.Slug() == slug {
			return role, true
		}
	}
	return "", false
}

// RoleCrumb is the label the artifact-free pages use for a role segment, e.g.
// "mid" becomes "Mid". An unknown segment is passed through.
func RoleCrumb(slug string) string {
	if role, ok := RoleFromSlug(slug); ok {
		return RoleLabel(role)
	}
	return slug
}

// tierRanks orders the tier letters best to worst for the interactive sorter.
// A tier the sorter does not know ranks below every known tier.
var tierRanks = map[aggmodel.Tier]int{
	aggmodel.TierSPlus: 6,
	aggmodel.TierS:     5,
	aggmodel.TierA:     4,
	aggmodel.TierB:     3,
	aggmodel.TierC:     2,
	aggmodel.TierD:     1,
}

// TierRank returns the sort weight of a tier letter.
func TierRank(tier aggmodel.Tier) int {
	return tierRanks[tier]
}

// RoleSlugString is RoleSlug for templates, which cannot carry an error return.
func RoleSlugString(role aggmodel.Role) string {
	slug, err := RoleSlug(role)
	if err != nil {
		return string(role)
	}
	return slug
}
