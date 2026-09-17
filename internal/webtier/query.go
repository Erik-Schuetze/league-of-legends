package webtier

import (
	"net/url"
	"strconv"
	"strings"
)

// The interaction state of a statistics page, carried in the URL.
//
// This is the whole difference between this tier and the build it replaces:
// sorting, filtering, pagination, patch switching and comparing are server
// state, addressed by query parameters, so every view of the data is a URL that
// can be shared, bookmarked, cached and reached with JavaScript disabled. The
// islands still enhance the same DOM, but nothing here needs them.
//
// Every field is validated against a whitelist before it is used, and an
// unparseable value falls back to the default rather than erroring: a typo in a
// query string is a reader's request, not a server fault, and answering it with
// the default view is more useful than answering it with a 400.

// Query is the requested view of a statistics page.
type Query struct {
	// Sort is one of the page's column keys.
	Sort string
	// Dir is DirAsc or DirDesc.
	Dir string
	// Filter is a case-insensitive substring match over champion name and role.
	Filter string
	// Page is the 1-based page of rows, used only when Per > 0.
	Page int
	// Per is the rows per page. Zero means every row.
	Per int
	// Compare is the champion slugs to compare against the page, in order.
	Compare []string
	// Patch is the requested patch when the route does not carry one.
	Patch string
}

// Sort directions.
const (
	DirAsc  = "asc"
	DirDesc = "desc"
)

// Rows-per-page bounds. The ceiling exists so that a hand-written per=100000
// cannot make the server render a page nobody can read.
const (
	MinPer = 10
	MaxPer = 200
	// MaxCompare is how many champions ?compare= may name at once.
	MaxCompare = 5
	// MaxFilterLen caps the filter string, which is echoed into the filter
	// input's value attribute and into the hidden link fields.
	MaxFilterLen = 96
)

// DefaultTierListQuery is the view a tier-list route serves with no query
// string. Its sort order is the reference build's, which is what makes the
// default render byte-comparable with the static site.
func DefaultTierListQuery() Query {
	return Query{Sort: "win_rate", Dir: DirDesc}
}

// DefaultMatchupQuery is the view a matchup route serves with no query string.
func DefaultMatchupQuery() Query {
	return Query{Sort: "win_rate", Dir: DirDesc}
}

// ParseQuery reads a query string into a Query, falling back to `fallback` for
// anything missing or malformed. `keys` is the page's sortable column set.
func ParseQuery(values url.Values, fallback Query, keys []string) Query {
	query := fallback
	if query.Dir != DirAsc {
		query.Dir = DirDesc
	}
	if sort := strings.TrimSpace(values.Get("sort")); sort != "" {
		for _, key := range keys {
			if key == sort {
				query.Sort = key
				break
			}
		}
	}
	switch strings.TrimSpace(values.Get("dir")) {
	case DirAsc:
		query.Dir = DirAsc
	case DirDesc:
		query.Dir = DirDesc
	}
	query.Filter = truncateRunes(strings.TrimSpace(values.Get("q")), MaxFilterLen)
	query.Page = positiveInt(values.Get("page"), 1)
	query.Per = boundedPer(values.Get("per"))
	// A comparison may be spelled either way a reader would write it by hand:
	// `?compare=ahri,leblanc` or `?compare=ahri&compare=leblanc`. Both name the
	// same panel, so both resolve to the same list of champions.
	query.Compare = parseCompare(strings.Join(values["compare"], ","))
	query.Patch = patchShaped(values.Get("patch"))
	if query.Sort == "" {
		query.Sort = fallback.Sort
	}
	return query
}

// IsDefault reports whether the query asks for the page's default view. The
// default view is the one that has to stay byte-comparable with the reference
// build, so the renderer branches on this rather than on each field.
func (q Query) IsDefault(def Query) bool {
	return q.Sort == def.Sort && q.Dir == def.Dir && q.Filter == "" &&
		q.Page <= 1 && q.Per == 0 && len(q.Compare) == 0 && q.Patch == ""
}

// Values renders the query as URL values, dropping defaults so that a link
// built from a default view stays clean.
func (q Query) Values(def Query) url.Values {
	values := url.Values{}
	if q.Sort != "" && q.Sort != def.Sort {
		values.Set("sort", q.Sort)
	}
	if q.Dir != "" && q.Dir != def.Dir {
		values.Set("dir", q.Dir)
	}
	if q.Filter != "" {
		values.Set("q", q.Filter)
	}
	if q.Per > 0 {
		values.Set("per", itoa(q.Per))
		if q.Page > 1 {
			values.Set("page", itoa(q.Page))
		}
	}
	if len(q.Compare) > 0 {
		values.Set("compare", strings.Join(q.Compare, ","))
	}
	if q.Patch != "" {
		values.Set("patch", q.Patch)
	}
	return values
}

// Href builds a link to a page path carrying this query.
func (q Query) Href(def Query, path string) string {
	encoded := q.Values(def).Encode()
	if encoded == "" {
		return path
	}
	return path + "?" + encoded
}

// With returns a copy of the query with one field overridden. Links are built
// from the current view plus one change, so a reader who is on page 3 of a
// filtered, ascending table stays there when they follow a column link.
func (q Query) With(field string, value string) Query {
	next := q
	switch field {
	case "sort":
		next.Sort = value
	case "dir":
		next.Dir = value
	case "q":
		next.Filter = truncateRunes(value, MaxFilterLen)
	case "page":
		next.Page = positiveInt(value, 1)
	case "per":
		next.Per = boundedPer(value)
	case "compare":
		next.Compare = parseCompare(value)
	case "patch":
		next.Patch = patchShaped(value)
	}
	return next
}

// positiveInt is the page number a link asks for, clamped to at least one.
func positiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

// boundedPer reads ?per=. Zero and anything out of range means "no pagination",
// which is the default and therefore the parity path.
func boundedPer(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < MinPer || value > MaxPer {
		return 0
	}
	return value
}

// parseCompare reads ?compare=a,b. Slugs are shape-checked here and resolved
// against the snapshot by the caller, so an unknown slug is ignored rather than
// becoming a link to a page that does not exist.
func parseCompare(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := make([]string, 0, MaxCompare)
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		slug := strings.ToLower(strings.TrimSpace(part))
		if slug == "" || seen[slug] || !slugShaped(slug) {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
		if len(out) == MaxCompare {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// patchShaped accepts only major.minor, so ?patch= cannot become a path
// traversal into the artifact tree.
func patchShaped(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 16 {
		return ""
	}
	for index, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r == '.' && index > 0 && index < len(value)-1:
		default:
			return ""
		}
	}
	if strings.Count(value, ".") != 1 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return ""
	}
	return value
}

// slugShaped accepts the champion slugs the static dataset publishes: lower
// case letters and digits, with internal separators.
func slugShaped(raw string) bool {
	if raw == "" || len(raw) > 48 {
		return false
	}
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// truncateRunes cuts a string to at most n runes.
func truncateRunes(value string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[:n])
}
