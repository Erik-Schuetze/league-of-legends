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
// when the settled state changes. Three properties are checked:
//
//  1. Every token the served sheets declare is either consumed by a rule or
//     listed below with a reason. A token cannot be added, deleted, or silently
//     stop being used without a test failing.
//  2. Every var() reference in the served sheets resolves to something declared.
//  3. Every colour pair the served sheets actually put together meets its WCAG
//     2.x floor. This includes the pair that motivated the freeze: the accent on
//     the surface is AA (6.13:1), not AAA, so §3.8 requires it never be used as
//     small text.
//
// The drift assertions are the other half: the porting traps in design-tokens.md
// §4 are that --bg-light is a surface (not a theme), that square corners are
// implicit, and that overflow-x:hidden must not be inherited onto body. Each is
// pinned below so a regression fails a test rather than failing an eyeball.

// designTokensCSS and componentsCSS are the two files this freeze owns, and
// designFreezeDoc is where the reasons for both of them live: a <style> block
// is downloaded by every visitor, so the prose is a separate file.
const (
	designTokensCSS = "css/design-tokens.css"
	componentsCSS   = "css/components.css"
	designFreezeDoc = "css/DESIGN-FREEZE.md"
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
	reTokenDecl = regexp.MustCompile(`(--[a-z0-9-]+)\s*:`)
	reVarRef    = regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)`)
	reDecl      = regexp.MustCompile(`--[a-z0-9-]+\s*:[^;]*;`)
	reComment   = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reTokenVal  = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;}]+)`)
)

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
	{"accent text on surface", "--accent-color", "--surface", 4.5, "design-tokens.md §3.8: 6.13:1, AA not AAA", false},
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

// TestFrozenDesignTraps pins the three porting traps in design-tokens.md §4 that
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

// TestFrozenLayerStaysLean pins the one thing about this layer that is a
// product cost rather than a correctness claim: it is inlined into every
// document, so its bytes are paid on every page view and are not cacheable.
//
// The layer first shipped at 19,646 B, of which 11,884 B (60%) was block
// comments explaining rules to a browser that discards them, and prose naming
// tokens (`--bg-color`) sat between `:root{` and `}`, which broke
// comment-unaware token parsing. The reasons moved to DESIGN-FREEZE.md. This
// test is what stops them drifting back in one helpful edit at a time.
func TestFrozenLayerStaysLean(t *testing.T) {
	const budget = 16000

	layer := frozenCSS()
	if len(layer) > budget {
		t.Errorf("the frozen layer is %d bytes, over the %d-byte budget: it is inlined into every document, so a rule that needs a paragraph of justification needs that paragraph in DESIGN-FREEZE.md instead",
			len(layer), budget)
	}

	comments := reCSSComment.FindAllString(layer, -1)
	commentBytes := 0
	for _, c := range comments {
		commentBytes += len(c)
	}
	// A comment budget rather than a ban: a rule whose reason cannot be
	// recovered from its own selectors earns one line, and the one-line
	// per-value notes in the token sheet are cheap.
	if share := 100 * commentBytes / len(layer); share > 50 {
		t.Errorf("comments are %d%% of the frozen layer (%d of %d bytes); a browser discards all of them, so shrink them or move them to DESIGN-FREEZE.md",
			share, commentBytes, len(layer))
	}

	// A nested comment opener makes a comment unterminated for any parser that
	// only looks for the first terminator, and `/legal/*` in prose is an easy
	// way to write one by accident.
	if strings.Count(layer, "/*") != strings.Count(layer, "*/") {
		t.Errorf("the frozen layer has %d comment openers and %d terminators",
			strings.Count(layer, "/*"), strings.Count(layer, "*/"))
	}

	// The register has to live in the doc and has to name the divergence it
	// justifies. Pointers such as "see DIVERGENCE REGISTER" may stay in the
	// sheet -- they are one clause, not prose -- but the register itself may not.
	doc := string(asset(designFreezeDoc))
	for _, want := range []string{"Divergence register", "--text-muted", "#615f57", "#666"} {
		if !strings.Contains(doc, want) {
			t.Errorf("DESIGN-FREEZE.md does not mention %q, so the divergence register is no longer recorded", want)
		}
	}
	if strings.Contains(layer, "Adopted the served value") {
		t.Error("the divergence register's entries are back in the inlined sheet")
	}
}

var _ = fmt.Sprintf
