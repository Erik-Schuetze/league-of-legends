package webtier

import (
	"fmt"
	"io/fs"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file is the executable form of the design freeze. The freeze is not a
// document that says the tokens are settled; it is these assertions, which fail
// when the settled state changes. Seven properties are checked:
//
//  1. Every token the served sheets declare is either consumed by a rule or
//     listed below with a reason. A token cannot be added, deleted, or silently
//     stop being used without a test failing.
//  2. Every var() reference in the served sheets resolves to something declared.
//  3. Every colour pair the served sheets actually put together meets its WCAG
//     2.x floor. This includes the pair that motivated the freeze: the accent on
//     the surface is AA (6.13:1), not AAA, so it must never be used as small
//     text.
//  4. The frozen layer is inlined last (TestFrozenLayerIsInlinedLast), so it
//     wins a specificity tie against the serviced baseline.
//  5. The frozen layer carries no prose and stays under its byte ceiling
//     (TestFrozenLayerStaysLean), and re-declares no token an earlier sheet
//     already provides with the same value
//     (TestFrozenLayerDeclaresNoRedundantToken).
//  6. A rule in the frozen layer that exists only to win a specificity tie
//     actually wins it (TestFrozenLayerWinsTheAriaCurrentTie). A rule that is
//     present in the file and still loses is the defect that motivated the
//     check: nothing in the served markup changes, so only computed style or
//     arbitration arithmetic can see it.
//  7. The standalone fault document inlines the frozen layer too
//     (TestStandaloneFaultFormInlinesFrozenLayer). It is the one document the
//     shell does not wrap, so its stylesheets are its own problem -- and it is
//     the document a reader sees when the snapshot is unreadable, which is
//     exactly when an unstyled page costs the most.
//
// The drift assertions are the other half: the porting traps this layer had to
// avoid are that --bg-light is a surface (not a theme), that square corners are
// implicit, and that overflow-x:hidden must not be inherited onto body. Each is
// pinned below so a regression fails a test rather than failing an eyeball.

// designTokensCSS and componentsCSS are the two sheets of the served design
// layer. The reasons a rule is what it is live in the commit that changed it and
// in the CHANGELOG - a <style> block is downloaded by every visitor, so there is
// deliberately no prose file beside these two.
const (
	designTokensCSS = "css/design-tokens.css"
	componentsCSS   = "css/components.css"
)

// reCSSComment matches a CSS block comment, including the terminator.
var reCSSComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// servedSheets is every stylesheet in the served path, in load order.
var servedSheets = []string{
	"astro/JsonLd.BEq7AnVK.css",
	"css/scoped-common.css",
	"css/scoped-champion.css",
	designTokensCSS,
	componentsCSS,
}

var (
	reTokenDecl  = regexp.MustCompile(`(--[a-z0-9-]+)\s*:`)
	reVarRef     = regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)`)
	reDecl       = regexp.MustCompile(`--[a-z0-9-]+\s*:[^;]*;`)
	reComment    = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reTokenVal   = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;}]+)`)
	reStyleBlock = regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`)
)

// declNames is the set of custom property names a stylesheet declares.
func declNames(css string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reTokenVal.FindAllStringSubmatch(css, -1) {
		out[m[1]] = true
	}
	return out
}

func readSheet(t *testing.T, name string) string {
	t.Helper()
	return string(asset(name))
}

func readTemplate(t *testing.T, name string) string {
	t.Helper()
	buf, err := fs.ReadFile(templateFS, "templates/"+name)
	if err != nil {
		t.Fatalf("read template %s: %v", name, err)
	}
	return string(buf)
}

// cssOf is a served sheet with its comments removed. Comments are stripped
// because design-tokens.css documents the rename of --bg-light in prose, and the
// assertions below are about what the cascade does, not what a comment says.
func cssOf(t *testing.T, name string) string {
	t.Helper()
	return reComment.ReplaceAllString(readSheet(t, name), "")
}

// declaredTokens returns every --token name declared anywhere in the served path.
func declaredTokens(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, name := range servedSheets {
		for _, m := range reTokenDecl.FindAllStringSubmatch(cssOf(t, name), -1) {
			out[m[1]] = append(out[m[1]], name)
		}
	}
	return out
}

// tokenUsage returns the tokens referenced from a rule body, with alias chains
// followed. A var() reference inside another token's value is an alias rather
// than a consumer, so token declarations are stripped first -- but a token that
// a consumed alias itself points at is consumed too, which is why the seed set is
// closed over the alias graph. Without the closure, --bg-fade-50 would look dead
// because only --nav-gradient names it, and --nav-gradient is what a rule uses.
func tokenUsage(t *testing.T) map[string]string {
	t.Helper()
	reachable := map[string]string{}
	aliases := map[string][]string{}
	for _, name := range servedSheets {
		sheet := cssOf(t, name)
		for _, m := range reTokenVal.FindAllStringSubmatch(sheet, -1) {
			for _, ref := range reVarRef.FindAllStringSubmatch(m[2], -1) {
				aliases[m[1]] = append(aliases[m[1]], ref[1])
			}
		}
		body := reDecl.ReplaceAllString(sheet, "")
		for _, m := range reVarRef.FindAllStringSubmatch(body, -1) {
			reachable[m[1]] = name
		}
	}
	queue := make([]string, 0, len(reachable))
	for name := range reachable {
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range aliases[cur] {
			if _, seen := reachable[next]; seen {
				continue
			}
			reachable[next] = "alias of " + cur
			queue = append(queue, next)
		}
	}
	return reachable
}

// unconsumedTokens are tokens the served sheets declare that no rule consumes.
// Each needs a reason, because "unused" is not automatically a defect: §1
// extracts the owner's whole token set, and a design system carries scale steps
// and capabilities that a given page inventory does not happen to reach. The
// value of the list is that it is exhaustive and pinned: a *new* dead token
// fails, and wiring one of these up also fails, which is what stops someone
// from wiring --dev-up-fg into the heatmap without measuring it first.
var unconsumedTokens = map[string]string{
	// Declared by the embedded baseline sheet, which this freeze does not edit.
	"--dev-up-fg":    "WOULD FAIL CONTRAST: it is --accent-color, which measures 3.55:1 on --dev-up-bg-strong (#a8b4d7), below the 4.5 floor. The up/down signal is carried by --dev-up-bg plus the signed value, so this token must stay unwired until a darker shade is derived. TestFrozenTokenContrast measures that pair live.",
	"--dev-down-fg":  "Dead for the same reason as --dev-up-fg: the down cell inherits --tier-ink (11.33:1 on --dev-down-bg).",
	"--dev-flat-fg":  "Dead: the flat cell inherits --tier-ink.",
	"--dev-up-hatch": "Dead: the value is `none`. The up state is a tint plus a sign; only the down and empty states need a hatch.",
	// Declared by this freeze.
	"--fallback-bg":   "Alias of --bg-color. The served markup reaches the same colour through --fallback-bg-light and the literal var(--bg-color); kept so a later component can name the palette role instead of the literal.",
	"--fallback-text": "Alias of --text-color, unused for the same reason as --fallback-bg.",
	"--radius":        "The ported spelling of --radius-card, declared because the embedded baseline declares it as a bare 0. Square corners are expressed through --radius-card, so this name stays as a compatibility alias rather than a second statement of the same value.",
	"--ring":          "Second spelling of --bw-rule + --ring-color for the same 2px ring. The served layer uses the outline form; consuming both invites the two drifting apart.",
	"--focus-ring":    "Alias of --ring, declared because the embedded baseline declares it. Unused for the same reason.",
	"--bp-mobile":     "A media query cannot take a var(), so this is documentation for template and island code rather than a value a rule can use.",
	"--float-amp":     "plan §7.2 #4 permits the hero float on the landing page only. The freeze adds no motion before the owner has seen the preview: a missing animation is not a defect, an unrequested one is.",
	"--shell-max":     "The owner's shell is 1400px; the served content column is --content-max (1200px). Widening the column is a layout decision that belongs to the preview verdict, not to the freeze.",
	"--dur-hover":     "§1's 0.3s. §7.2 #1 replaces it with --dur-fast (0.12s) for table hover; the 0.3s value is kept for the non-dense components a later page may add.",
	"--space-7":       "Scale step with no current consumer. Declared because §1 declares it.",
	"--bw-pre":        "No route in the inventory renders a <pre>, so there is nothing to consume it yet.",
}

func TestFrozenTokenContract(t *testing.T) {
	declared := declaredTokens(t)
	if len(declared) == 0 {
		t.Fatal("no tokens found: the served sheets were not read")
	}
	consumed := tokenUsage(t)

	var dead []string
	for name := range declared {
		if _, ok := consumed[name]; ok {
			continue
		}
		if _, ok := unconsumedTokens[name]; !ok {
			dead = append(dead, name)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Errorf("declared but neither consumed nor listed in unconsumedTokens (add a reason, or delete the token):\n  %s",
			strings.Join(dead, "\n  "))
	}

	// The reverse direction: a stale register entry is a claim that no longer
	// holds, so it must be removed rather than left to rot.
	var stale []string
	for name := range unconsumedTokens {
		if _, ok := declared[name]; !ok {
			stale = append(stale, name)
			continue
		}
		if _, ok := consumed[name]; ok {
			stale = append(stale, name+" (now consumed)")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("unconsumedTokens entries that are stale (not declared, or now consumed):\n  %s",
			strings.Join(stale, "\n  "))
	}

	// No dangling var() reference: a typo renders as an unstyled property, which
	// looks plausible, so it has to be a test failure.
	var dangling []string
	for name := range consumed {
		if _, ok := declared[name]; !ok {
			dangling = append(dangling, name)
		}
	}
	sort.Strings(dangling)
	if len(dangling) > 0 {
		t.Errorf("var() references with no declaration:\n  %s", strings.Join(dangling, "\n  "))
	}

	t.Logf("served layer: %d tokens declared, %d consumed by rules, %d registered unconsumed",
		len(declared), len(consumed), len(unconsumedTokens))
}

// pair is one foreground/background combination the served sheets put together,
// the floor it must clear, and whether it is registered as a known failure.
type pair struct {
	what string
	fg   string
	bg   string
	min  float64
	note string
	// knownFail marks a pair that is measured, expected to be below its floor,
	// and deliberately left unwired. It is the suite's positive control: the
	// instrument demonstrably reports FAIL on a real value in the served layer,
	// so a PASS elsewhere is not the checker being unable to fail.
	knownFail bool
}

// contrastPairs are every pair the served sheets actually put together. 4.5 is
// the WCAG 2.x AA floor for normal text; 3.0 is the floor for non-text UI.
var contrastPairs = []pair{
	{"body text on surface", "--text-color", "--surface", 4.5, "", false},
	{"body text on page field", "--text-color", "--bg-color", 4.5, "", false},
	{"heading on surface", "--primary-color", "--surface", 4.5, "", false},
	{"heading on page field", "--primary-color", "--bg-color", 4.5, "", false},
	{"accent text on surface", "--accent-color", "--surface", 4.5, "6.13:1, AA not AAA", false},
	{"accent text on page field", "--accent-color", "--bg-color", 4.5, "", false},
	{"muted text on surface", "--text-muted", "--surface", 4.5, "§1 recommends this over #666", false},
	{"muted text on page field", "--text-muted", "--bg-color", 4.5, "the tightest pair in the layer", false},
	{"small-text shade on surface", "--text-muted-alt", "--surface", 4.5, "", false},
	{"small-text shade on page field", "--text-muted-alt", "--bg-color", 4.5, "", false},
	{"footer text on primary", "--text-on-dark", "--primary-color", 4.5, "", false},
	{"tier ink on S+", "--tier-ink", "--tier-splus-bg", 4.5, "", false},
	{"tier ink on S", "--tier-ink", "--tier-s-bg", 4.5, "", false},
	{"tier ink on A", "--tier-ink", "--tier-a-bg", 4.5, "", false},
	{"tier ink on B", "--tier-ink", "--tier-b-bg", 4.5, "", false},
	{"tier ink on C", "--tier-ink", "--tier-c-bg", 4.5, "", false},
	{"tier ink on D", "--tier-ink", "--tier-d-bg", 4.5, "", false},
	{"tier ink on up cell", "--tier-ink", "--dev-up-bg", 4.5, "", false},
	{"tier ink on up-strong cell", "--tier-ink", "--dev-up-bg-strong", 4.5, "", false},
	{"tier ink on down cell", "--tier-ink", "--dev-down-bg", 4.5, "", false},
	{"tier ink on down-strong cell", "--tier-ink", "--dev-down-bg-strong", 4.5, "", false},
	{"tier ink on empty cell", "--tier-ink", "--dev-empty-bg", 4.5, "", false},
	{"accent on up cell (the unwired --dev-up-fg)", "--accent-color", "--dev-up-bg", 4.5, "clears here, which is what makes the next row a real finding", false},
	{"accent on up-strong cell (the unwired --dev-up-fg)", "--accent-color", "--dev-up-bg-strong", 4.5, "POSITIVE CONTROL: below the floor", true},
	{"focus ring on surface", "--ring-color", "--surface", 3.0, "non-text UI floor", false},
	{"focus ring on page field", "--ring-color", "--bg-color", 3.0, "non-text UI floor", false},
}

func TestFrozenTokenContrast(t *testing.T) {
	values := tokenValues(t)
	for _, p := range contrastPairs {
		fg, ok := values[p.fg]
		if !ok {
			t.Errorf("%s: %s is not declared", p.what, p.fg)
			continue
		}
		bg, ok := values[p.bg]
		if !ok {
			t.Errorf("%s: %s is not declared", p.what, p.bg)
			continue
		}
		ratio := contrastRatio(t, fg, bg)
		status := "pass"
		if ratio < p.min {
			status = "FAIL"
		}
		if p.note != "" {
			t.Logf("%-46s %-9s on %-9s %6.2f  %s  (%s)", p.what, fg, bg, ratio, status, p.note)
		} else {
			t.Logf("%-46s %-9s on %-9s %6.2f  %s", p.what, fg, bg, ratio, status)
		}
		switch {
		case ratio < p.min && !p.knownFail:
			t.Errorf("%s: %.2f:1 is below the %.1f floor", p.what, ratio, p.min)
		case ratio >= p.min && p.knownFail:
			t.Errorf("%s: %.2f:1 now clears its floor; wire the token or drop the knownFail flag", p.what, ratio)
		}
	}
}

// tokenValues resolves each token to a literal value, following alias chains.
// Only the 25 pairs above need colour resolution, so a token that resolves to a
// non-colour (a shadow, a gradient) is left as-is and rejected by the parser.
func tokenValues(t *testing.T) map[string]string {
	t.Helper()
	raw := map[string]string{}
	for _, name := range servedSheets {
		for _, m := range reTokenVal.FindAllStringSubmatch(cssOf(t, name), -1) {
			raw[m[1]] = strings.TrimSpace(m[2])
		}
	}
	resolved := map[string]string{}
	var resolve func(name string, depth int) (string, bool)
	resolve = func(name string, depth int) (string, bool) {
		if v, ok := resolved[name]; ok {
			return v, true
		}
		if depth > 8 {
			return "", false
		}
		v, ok := raw[name]
		if !ok {
			return "", false
		}
		if m := regexp.MustCompile(`^var\(\s*(--[a-z0-9-]+)(?:\s*,\s*([^)]+))?\)$`).FindStringSubmatch(v); m != nil {
			if inner, ok := resolve(m[1], depth+1); ok {
				resolved[name] = inner
				return inner, true
			}
			if m[2] != "" {
				resolved[name] = strings.TrimSpace(m[2])
				return resolved[name], true
			}
			return "", false
		}
		resolved[name] = v
		return v, true
	}
	for name := range raw {
		if _, ok := resolve(name, 0); !ok {
			t.Errorf("token %s could not be resolved to a literal", name)
		}
	}
	return resolved
}

var reHex = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

func contrastRatio(t *testing.T, fg, bg string) float64 {
	t.Helper()
	l1, l2 := relativeLuminance(t, fg), relativeLuminance(t, bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func relativeLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	m := reHex.FindStringSubmatch(strings.TrimSpace(hex))
	if m == nil {
		t.Fatalf("%q is not a #rrggbb colour, so it cannot be measured", hex)
	}
	digits := m[1]
	if len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	var channel [3]float64
	for i := 0; i < 3; i++ {
		raw, err := strconv.ParseInt(digits[i*2:i*2+2], 16, 32)
		if err != nil {
			t.Fatalf("%q: %v", hex, err)
		}
		c := float64(raw) / 255
		if c <= 0.04045 {
			channel[i] = c / 12.92
		} else {
			channel[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channel[0] + 0.7152*channel[1] + 0.0722*channel[2]
}

// TestFrozenDesignTraps pins the three porting traps that
// are silent when they regress. The positive control is that each of these
// predicates is false for a string that does contain the trap, so the assertion
// can fail.
func TestFrozenDesignTraps(t *testing.T) {
	all := ""
	for _, name := range servedSheets {
		all += cssOf(t, name)
	}

	// Trap 1: --bg-light is the surface, not a theme. It was renamed, and both
	// the declaration and the reference must stay gone.
	if strings.Contains(all, "--bg-light") {
		t.Error("--bg-light is back: it is the surface colour named as a theme, which inverts the hierarchy")
	}
	// Positive control for the predicate above.
	if !strings.Contains("body{background:var(--bg-light)}", "--bg-light") {
		t.Error("positive control failed: the --bg-light predicate cannot detect the trap")
	}

	// Trap 3 + §3.7: never inherit overflow-x:hidden on body. This app is
	// table-heavy, so the trap would silently clip every wide stat table.
	if !strings.Contains(all, "overflow-x: auto") && !strings.Contains(all, "overflow-x:auto") {
		t.Error("no scroll container: wide stat tables have no containment, which is the other half of §3.7")
	}
	if regexp.MustCompile(`(?s)body\s*\{[^}]*overflow-x\s*:\s*hidden`).MatchString(all) {
		t.Error("body{overflow-x:hidden} clips wide stat tables (§3.7); the scroll belongs on the table container")
	}
	// Positive control: the same predicate must fire on the upstream trap.
	if !regexp.MustCompile(`(?s)body\s*\{[^}]*overflow-x\s*:\s*hidden`).MatchString("body{overflow-x:hidden}") {
		t.Error("positive control failed: the body overflow-x predicate cannot detect the trap")
	}

	// Trap 2: square corners are implicit, so every radius must be one of the
	// documented exceptions. Anything else is a framework default reappearing.
	allowed := map[string]bool{"0": true, "var(--radius-btn)": true, "var(--radius-code)": true, "var(--radius-card)": true}
	var unexpected []string
	for _, m := range regexp.MustCompile(`border-radius\s*:\s*([^;}]+)`).FindAllStringSubmatch(all, -1) {
		v := strings.TrimSpace(strings.ToLower(m[1]))
		if allowed[v] {
			continue
		}
		unexpected = append(unexpected, v)
	}
	if len(unexpected) > 0 {
		t.Errorf("undeclared border-radius values (square corners are the default, §4): %v", unexpected)
	}
	// Positive control: an 8px radius, which is what a framework default looks
	// like, must be rejected by the same check.
	if allowed["8px"] {
		t.Error("positive control failed: the radius allow-list accepts an invented value")
	}
}

// TestFrozenLayerIsInlinedLast is the load-order half of the contract: the
// component layer only wins a specificity tie because the shell emits it after
// the scoped chunks, so the position is asserted rather than assumed.
func TestFrozenLayerIsInlinedLast(t *testing.T) {
	shell := readTemplate(t, "shell.tmpl")
	scoped := strings.Index(shell, "{{ .ScopedCSS }}")
	frozen := strings.Index(shell, "{{ .FrozenCSS }}")
	if scoped < 0 || frozen < 0 {
		t.Fatalf("shell does not inline both layers: scoped=%d frozen=%d", scoped, frozen)
	}
	if frozen < scoped {
		t.Error("the frozen layer is inlined before the scoped chunks, so it loses every specificity tie")
	}
	if n := strings.Count(shell, "{{ .FrozenCSS }}"); n != 1 {
		t.Errorf("shell inlines the frozen layer %d times", n)
	}

	// The token file must come before the component file: the component layer
	// consumes the tokens, so reversing them would render unstyled values.
	tokens := strings.Index(frozenCSS(), "--radius-card")
	components := strings.Index(frozenCSS(), "var(--radius-card)")
	if tokens < 0 || components < 0 || tokens > components {
		t.Errorf("design-tokens.css must precede components.css (card radius declared at %d, consumed at %d)", tokens, components)
	}
	if want := len(asset(designTokensCSS)) + len(asset(componentsCSS)); len(frozenCSS()) != want {
		t.Errorf("frozenCSS is %d bytes, want %d", len(frozenCSS()), want)
	}
}

// TestFrozenLayerStaysLean pins the two things about this layer that are costs
// rather than correctness claims: it is inlined into every document, so its
// bytes are paid on every page view and are not cacheable, and the prose in it
// is paid for by a browser that discards every byte of it.
//
// The layer first shipped at 19,646 B, of which 11,884 B (60%) was block
// comments explaining rules to a browser that discards them, and prose naming
// tokens (`--bg-color`) sat between `:root{` and `}`, which broke
// comment-unaware token parsing. The first version of this test tolerated
// comments as long as they were "less than half the block", so the prose grew
// back to 45.6% (6,612 B of 14,178 B) without ever failing it. A gate that
// cannot fail is why the budget below is a ceiling on the whole layer.
func TestFrozenLayerStaysLean(t *testing.T) {
	// The strip landed at 6,777 B, so the ceiling leaves about a kilobyte of
	// headroom for a rule that earns its place.
	const budget = 7800

	layer := frozenCSS()
	if len(layer) > budget {
		t.Errorf("the frozen layer is %d bytes, over the %d-byte budget: it is inlined into every document, so a rule that needs a paragraph of justification does not belong in it",
			len(layer), budget)
	}

	// A ban rather than a budget, and not only because the budget failed before:
	// a comment is the one thing in the block that cannot change what any
	// property computes to, so its correct budget is zero. The ban subsumes the
	// opener/terminator balance check that used to be here -- with no opener
	// there is no comment to leave unterminated.
	comments := reCSSComment.FindAllString(layer, -1)
	commentBytes := 0
	for _, c := range comments {
		commentBytes += len(c)
	}
	if len(comments) > 0 {
		t.Errorf("the frozen layer carries %d comment(s), %d of %d bytes, every one of which a browser discards: the reason belongs in the commit that added the rule",
			len(comments), commentBytes, len(layer))
	}
	// Positive control: the predicate has to reject the prose that was actually
	// removed, or "no comments" is a claim about a checker that cannot fail.
	if got := reCSSComment.FindAllString("/* body text, in-card links */\n.link{font-weight:var(--fw-nav)}", -1); len(got) != 1 {
		t.Errorf("positive control failed: the comment predicate found %d comments in prose it has to reject", len(got))
	}
}

// cssRule is one selector list and its declaration block, as they appear in a
// served sheet.
type cssRule struct {
	sel  string
	body string
}

// reCSSRule splits a stylesheet into rules. Nested at-rules fall out of the
// exclusive character classes: an @media header cannot reach a `}` without
// crossing the `{` of the rule inside it, so the rules inside it are what match.
var reCSSRule = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)

func cssRules(css string) []cssRule {
	var out []cssRule
	for _, m := range reCSSRule.FindAllStringSubmatch(css, -1) {
		out = append(out, cssRule{sel: strings.TrimSpace(m[1]), body: m[2]})
	}
	return out
}

// declValue returns the value a declaration block sets for one property, with
// the shorthand-expanded `border-bottom` form deliberately not counted: the two
// are different properties as far as this arbitration is concerned.
func declValue(body, prop string) string {
	m := regexp.MustCompile(`(?:^|;)\s*` + regexp.QuoteMeta(prop) + `\s*:\s*([^;}]+)`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(m[1]), " ")
}

// specOf is the (id, class, type) weight the cascade gives a selector. It is
// computed from the bytes rather than written down, because the whole of
// finding 2 is that a rule can be present in the served CSS and still lose:
// attribute selectors and pseudo-classes weigh as classes, so a scoped rule's
// two cid attributes are what beat the mandated fix.
func specOf(sel string) [3]int {
	ids, classes, types := 0, 0, 0
	for i := 0; i < len(sel); {
		switch c := sel[i]; {
		case c == '[' || c == '(':
			close := byte(']')
			if c == '(' {
				close = ')'
			}
			if c == '[' {
				classes++
			}
			for i < len(sel) && sel[i] != close {
				i++
			}
			i++
		case c == '#':
			ids++
			i = identEnd(sel, i+1)
		case c == '.':
			classes++
			i = identEnd(sel, i+1)
		case c == ':':
			if i+1 < len(sel) && sel[i+1] == ':' {
				i++ // pseudo-element
			} else {
				classes++
			}
			i = identEnd(sel, i+1)
		case identStart(c):
			types++
			i = identEnd(sel, i)
		default:
			i++ // combinator, comma, universal, whitespace
		}
	}
	return [3]int{ids, classes, types}
}

func identStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-' || c >= 0x80
}

func identEnd(s string, i int) int {
	for i < len(s) {
		c := s[i]
		if !identStart(c) && (c < '0' || c > '9') {
			break
		}
		i++
	}
	return i
}

// specGE reports whether a wins or ties against b. In the single-selector case
// a tie is broken by document order, so a tie is a win for the later layer.
func specGE(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

// cssCandidate is a rule in the served path that sets one property, kept with
// the sheet it came from so load order stays visible in a failure message.
type cssCandidate struct {
	sheet string
	sel   string
	body  string
}

// winner is the cascade's answer for one property on one element: the highest
// specificity wins, and equal specificities are broken by document order. Only
// normal declarations are modelled, because only normal declarations exist
// among this property's candidates.
func winner(cands []cssCandidate) (cssCandidate, bool) {
	var best cssCandidate
	found := false
	for _, c := range cands {
		if !found || specGE(specOf(c.sel), specOf(best.sel)) {
			best, found = c, true
		}
	}
	return best, found
}

// TestFrozenLayerWinsTheAriaCurrentTie is finding 2's regression guard.
//
// The matchups panel's current-page link is a.link[aria-current=page] inside
// ul.panel inside details.menu. The scoped sheets set its border-bottom-color
// twice: once with the accent, which is what the freeze mandates, and once to
// transparent on any .link inside a .panel. The second selector carries both cid
// attributes, so it weighs (0,4,0) against the mandated rule's (0,3,0): the
// accessibility fix is in the served CSS and loses anyway.
//
// Nothing about the markup changes when it regresses, so this cannot be checked
// by looking for the rule. The assertions below derive the arbitration from the
// served bytes; the computed value itself is a browser measurement.
func TestFrozenLayerWinsTheAriaCurrentTie(t *testing.T) {
	const prop = "border-bottom-color"

	scoped := cssOf(t, "css/scoped-common.css") + cssOf(t, "css/scoped-champion.css")
	frozen := frozenCSS()

	var ariaScoped, panelScoped, ariaFrozen string
	for _, r := range cssRules(scoped) {
		if declValue(r.body, prop) == "" {
			continue
		}
		switch {
		case strings.Contains(r.sel, "aria-current"):
			ariaScoped = r.sel
		case strings.Contains(r.sel, ".panel") && strings.Contains(r.sel, ".link"):
			panelScoped = r.sel
		}
	}
	for _, r := range cssRules(frozen) {
		if declValue(r.body, prop) == "" || !strings.Contains(r.sel, "aria-current") {
			continue
		}
		if ariaFrozen == "" || specGE(specOf(r.sel), specOf(ariaFrozen)) {
			ariaFrozen = r.sel
		}
	}
	if ariaScoped == "" || panelScoped == "" || ariaFrozen == "" {
		t.Fatalf("the competing rules are not all in the served CSS: scoped aria-current=%q scoped panel=%q frozen=%q",
			ariaScoped, panelScoped, ariaFrozen)
	}

	// The defect itself, derived rather than assumed. If the scoped panel rule
	// ever stops outranking the scoped aria-current rule, the frozen override has
	// become redundant rather than wrong, and that is a decision to re-measure
	// and take deliberately.
	if !specGE(specOf(panelScoped), specOf(ariaScoped)) || panelScoped == ariaScoped {
		t.Errorf("the scoped panel rule (%s, %v) no longer outranks the scoped aria-current rule (%s, %v): the frozen override is now redundant, so re-measure the computed border-bottom-color and delete whichever half is unnecessary",
			panelScoped, specOf(panelScoped), ariaScoped, specOf(ariaScoped))
	}
	if !specGE(specOf(ariaFrozen), specOf(panelScoped)) {
		t.Errorf("the frozen rule (%s, %v) loses to the scoped panel rule (%s, %v), so aria-current is defeated again",
			ariaFrozen, specOf(ariaFrozen), panelScoped, specOf(panelScoped))
	}

	// The arbitration end to end, in load order over every sheet: the answer has
	// to be the frozen accent rule. The positive control below is the same
	// computation with the frozen layer removed, which is the pre-fix served path.
	var cands []cssCandidate
	for _, name := range servedSheets {
		for _, r := range cssRules(cssOf(t, name)) {
			if declValue(r.body, prop) == "" || !strings.Contains(r.sel, ".link") {
				continue
			}
			cands = append(cands, cssCandidate{sheet: name, sel: r.sel, body: r.body})
		}
	}
	win, ok := winner(cands)
	if !ok {
		t.Fatal("no rule in the served path sets border-bottom-color on a .link")
	}
	if got := declValue(win.body, prop); win.sheet != componentsCSS || got != "var(--accent-color)" {
		t.Errorf("the served winner for %s on the matchups nav link is %q from %s (%s); it has to be the frozen layer's var(--accent-color)",
			prop, got, win.sheet, win.sel)
	}

	var withoutFrozen []cssCandidate
	for _, c := range cands {
		if c.sheet == designTokensCSS || c.sheet == componentsCSS {
			continue
		}
		withoutFrozen = append(withoutFrozen, c)
	}
	ctrl, ok := winner(withoutFrozen)
	if !ok || !strings.Contains(declValue(ctrl.body, prop), "#0000") {
		t.Errorf("positive control failed: without the frozen layer the winner would be %q from %s, not the transparent scoped rule, so this test cannot detect the defect it exists for",
			declValue(ctrl.body, prop), ctrl.sel)
	}
}

// redundantByDesign is finding 5's exception register. Each entry is a token the
// frozen layer re-declares with a value an earlier sheet in the load order
// already provides, which cannot change what any property computes to, and which
// no rule inside the frozen layer reads.
//
// Finding 5 lists 32 such names and deletes 29 of them, keeping
// --print-ink/-paper/-rule; the register below is that same exception plus
// --text-muted. It is deliberately near-empty: an entry is a
// claim that the bytes are worth keeping, so it has to name the consumer the
// predicate cannot see or the check the removal would make vacuous.
var redundantByDesign = map[string]string{
	"--print-ink":   "read by L1's five @media print blocks, which the layer-local predicate cannot see. The base sheet does declare the identical #000, so this is a byte decision rather than a behaviour one: Finding 5 keeps these three explicitly, and making the print colour of the scoped chunk depend on a sheet this package does not own is the wrong direction to save 15 B.",
	"--print-paper": "read by L1's five @media print blocks, which the layer-local predicate cannot see. The base sheet does declare the identical #fff, so this is a byte decision rather than a behaviour one: Finding 5 keeps these three explicitly, and making the print colour of the scoped chunk depend on a sheet this package does not own is the wrong direction to save 16 B.",
	"--print-rule":  "read by L1's five @media print blocks, which the layer-local predicate cannot see. The base sheet does declare the identical #767676, so this is a byte decision rather than a behaviour one: Finding 5 keeps these three explicitly, and making the print colour of the scoped chunk depend on a sheet this package does not own is the wrong direction to save 20 B.",
	"--text-muted":  "the base sheet already declares #615f57, so this costs 27 inlined bytes and cannot change the served colour. It stays because a11y_contract_test.go §3.8 is a positive control that rewrites exactly this literal (`--text-muted: #615f57`) to prove the contrast check can fail, and the base sheet's spelling of the same value is `--text-muted:#615f57` with no space, which that mutation does not match. Removing the declaration does not break the check, it makes the control vacuous -- a live check that proves more is worth 27 bytes. Re-pointing the mutation at the base sheet's spelling would let the declaration go, but that file is not this finding's to edit.",
}

// redundantTokens returns the tokens a layer re-declares at a value an earlier
// sheet already provides and that nothing in the layer reads. "Reads" is any
// var() reference in the layer, including one inside another declaration: a
// layer whose own --nav-gradient names --bg-color is using it even when the rule
// that consumes --nav-gradient lives in the scoped chunk.
//
// Comparing the text after whitespace normalisation keeps the predicate on
// identical spellings: `.75rem` and `0.75rem` are the same size to a browser but
// different strings here, and for a deletion claim the conservative direction is
// to keep what the predicate cannot prove identical.
func redundantTokens(layer, earlier string) []string {
	earlierValues := map[string]string{}
	for _, m := range reTokenVal.FindAllStringSubmatch(earlier, -1) {
		earlierValues[m[1]] = strings.Join(strings.Fields(m[2]), " ")
	}
	read := map[string]bool{}
	for _, m := range reVarRef.FindAllStringSubmatch(layer, -1) {
		read[m[1]] = true
	}
	var out []string
	for _, m := range reTokenVal.FindAllStringSubmatch(layer, -1) {
		name, value := m[1], strings.Join(strings.Fields(m[2]), " ")
		if read[name] {
			continue
		}
		if prev, ok := earlierValues[name]; ok && prev == value {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// TestFrozenLayerDeclaresNoRedundantToken pins finding 5. A declaration that
// repeats an identical value from an earlier sheet, and that nothing in the
// layer reads, is bytes in the one block no visitor can cache and no browser can
// act on. Twenty-eight of them were removed (800 B of declaration text, 828 with
// the line endings); the register above is why the twenty-ninth stays.
//
// The predicate reproduces the measured finding exactly: run against the layer
// as it stood before the strip it returns the 32 names Finding 5 lists, so a
// disagreement here is a real change in the layer rather than a disagreement
// about the arithmetic.
func TestFrozenLayerDeclaresNoRedundantToken(t *testing.T) {
	earlier := ""
	for _, name := range []string{"astro/JsonLd.BEq7AnVK.css", "css/scoped-common.css", "css/scoped-champion.css"} {
		earlier += cssOf(t, name)
	}
	layer := cssOf(t, designTokensCSS) + cssOf(t, componentsCSS)
	if len(reTokenDecl.FindAllString(layer, -1)) == 0 {
		t.Fatal("the frozen layer declares no tokens: the sheets were not read")
	}
	got := redundantTokens(layer, earlier)

	want := make([]string, 0, len(redundantByDesign))
	for name := range redundantByDesign {
		want = append(want, name)
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the frozen layer re-declares tokens an earlier sheet already provides at the same value and nothing in the layer reads:\n  found: %v\n  registered: %v\nEither remove the declaration or add it to redundantByDesign with a reason.",
			got, want)
	}

	// Positive control: the predicate has to report a re-declaration of an
	// earlier identical value.
	if ctrl := redundantTokens(":root{--fs-md: 0.9375rem;}", ":root{--fs-md:0.9375rem;}"); len(ctrl) != 1 {
		t.Errorf("positive control failed: the redundancy predicate reported %v for a re-declaration it has to detect", ctrl)
	}
	// Negative controls, one per half of the predicate: a different value, and a
	// value the layer itself reads, are both allowed to be re-declared.
	if ctrl := redundantTokens(":root{--fs-md: 0.9rem;}", ":root{--fs-md:0.9375rem;}"); len(ctrl) != 0 {
		t.Errorf("negative control failed: the redundancy predicate reported %v for a different value", ctrl)
	}
	if ctrl := redundantTokens(":root{--fs-md: 0.9375rem;}p{font-size:var(--fs-md)}", ":root{--fs-md:0.9375rem;}"); len(ctrl) != 0 {
		t.Errorf("negative control failed: the redundancy predicate reported %v for a token the layer reads", ctrl)
	}
}

// TestStandaloneFaultFormInlinesFrozenLayer pins finding 7. The standalone
// document is what a reader gets when the shell itself cannot be rendered, so it
// carries its own stylesheets -- and before this lane it carried two of the
// three: the base sheet by link and the scoped chunk, with no frozen layer.
// Every token only the frozen layer declares therefore resolved to nothing on
// that page, and every token the base sheet spells differently resolved to the
// baseline's value, on the one document whose whole job is to be legible when
// the rest of the tier is not.
//
// The shelled fault page is not asserted here: it is wrapped by shell.tmpl, so
// it inherits the load order TestFrozenLayerIsInlinedLast already pins.
func TestStandaloneFaultFormInlinesFrozenLayer(t *testing.T) {
	renderer := newFixtureRenderer(t)
	doc, err := renderer.RenderStandaloneError("/tier-list/top", 503, FaultArtifact, "")
	if err != nil {
		t.Fatalf("RenderStandaloneError: %v", err)
	}
	html := string(doc)

	blocks := reStyleBlock.FindAllStringSubmatch(html, -1)
	if len(blocks) != 2 {
		t.Fatalf("the standalone document inlines %d style blocks, want 2 (scoped chunk, then frozen layer)", len(blocks))
	}
	scoped, frozen := blocks[0][1], blocks[1][1]

	// The block has to be the frozen layer itself, not a copy of it that can
	// drift: the shell and the standalone document have to be serving the same
	// bytes or the freeze has two definitions.
	if want := frozenCSSChunk(); frozen != want {
		t.Errorf("the standalone document's last style block is not the frozen layer (%d bytes, want %d)", len(frozen), len(want))
	}
	if strings.Contains(scoped, "--radius-card") {
		t.Error("the scoped chunk is carrying frozen tokens, so this test cannot tell the two layers apart")
	}

	// Positive control: the block has to contribute something the other two
	// sources in the document cannot. A token only the frozen layer declares,
	// and that a rule in the frozen layer reads, is undefined without the block
	// -- which is the defect in its most visible form.
	base := cssOf(t, "astro/JsonLd.BEq7AnVK.css") + scoped
	earlier := declNames(base)
	layer := cssOf(t, designTokensCSS) + cssOf(t, componentsCSS)
	read := map[string]bool{}
	for _, m := range reVarRef.FindAllStringSubmatch(layer, -1) {
		read[m[1]] = true
	}
	var frozenOnly []string
	for name := range declNames(layer) {
		if !earlier[name] && read[name] {
			frozenOnly = append(frozenOnly, name)
		}
	}
	if len(frozenOnly) == 0 {
		t.Fatal("the frozen layer declares no consumed token the other two sources do not, so this test cannot detect its absence")
	}
	sort.Strings(frozenOnly)
	t.Logf("frozen-only tokens consumed on the standalone document: %s", strings.Join(frozenOnly, " "))
}

var _ = fmt.Sprintf
