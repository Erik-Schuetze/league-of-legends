package aggregate

import "strings"

// The published region and the Riot platform id are two spellings of one thing,
// and the aggregate has to work with both.
//
// docs/contracts.md freezes the published path as `.../p/<patch>/EUW/420/all`,
// where EUW is the region. A match payload's `info.platformId` is the platform
// id Riot actually serves from: EUW1. Filtering the archive on the published
// region would therefore match nothing at all, and the build would fail closed
// with an empty window on every real archive - a failure that no fixture with a
// single spelling can reveal. So the archive filter accepts both spellings of
// the configured region and the published path always uses the region as
// configured.
//
// The table is the platform route Riot documents, and the fallback is the
// region itself, which is correct for every platform whose id carries no digit
// (KR, RU, and the new-account regions that ship as their own platform id).

// regionPlatforms maps the published region to the platform id Riot uses in
// match payloads.
var regionPlatforms = map[string]string{
	"EUW":  "EUW1",
	"EUNE": "EUN1",
	"NA":   "NA1",
	"BR":   "BR1",
	"JP":   "JP1",
	"LAN":  "LA1",
	"LAS":  "LA2",
	"OC":   "OC1",
	"TR":   "TR1",
	"RU":   "RU",
	"KR":   "KR",
}

// PlatformForRegion resolves the platform id to filter the archive on.
//
// The configured platform is honoured verbatim when the operator named one,
// because an operator who names a platform knows what their archive holds. The
// empty string falls back to the region itself, which keeps a region that is
// already a platform id (EUW1, KR) working without configuration.
func PlatformForRegion(region, platform string) string {
	if platform = strings.ToUpper(strings.TrimSpace(platform)); platform != "" {
		return platform
	}
	region = strings.ToUpper(strings.TrimSpace(region))
	if mapped, ok := regionPlatforms[region]; ok {
		return mapped
	}
	return region
}

// platformFilter lists the spellings of one region the archive filter accepts:
// the configured region and the platform id it resolves to, each once, in a
// stable order. Accepting both means an archive written by a crawler that
// recorded the platform id and one written from the configured region are read
// the same way, and neither silently yields an empty window.
func platformFilter(region, platform string) []string {
	region = strings.ToUpper(strings.TrimSpace(region))
	resolved := PlatformForRegion(region, platform)
	out := make([]string, 0, 2)
	for _, value := range []string{region, resolved} {
		if value == "" || contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
