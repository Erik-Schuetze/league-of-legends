package webtier

import (
	"html/template"
	"strings"
)

// The reference build escapes interpolated values with html-escaper semantics:
// < > & ' " become &lt; &gt; &amp; &#39; &quot;. Go's html/template escapes the
// same characters except that it writes &quot; as &#34;. The render-parity
// normaliser folds the two together, but the served bytes should be the ones the
// reference produced, so every view-provided string goes through here.
func textHTML(value string) template.HTML {
	return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(value), "&#34;", "&quot;"))
}

// EscapeText is textHTML for callers outside this package.
func EscapeText(value string) template.HTML { return textHTML(value) }

// EmptyState is the explicit empty state: "no data" on every statistics slot,
// and the selection-is-missing case for a snapshot that has nothing for it. It is
// never an error and never a zero.
type EmptyState struct {
	Title    string
	Body     string
	MinCellN int
	HasN     bool
}

func (e EmptyState) HTML() template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="fallback-empty" role="note"><p><strong>`)
	b.WriteString(string(textHTML(e.Title)))
	b.WriteString(`</strong></p><p>`)
	b.WriteString(string(textHTML(e.Body)))
	b.WriteString(`</p>`)
	if e.HasN {
		b.WriteString(`<p>A rate is published only for a cell with at least n = `)
		b.WriteString(IntegerAny(e.MinCellN))
		b.WriteString(` games. Nothing here is estimated or back-filled from a thinner sample.</p>`)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// emptyFunc adapts EmptyState to the template FuncMap. minCellN is a *int so a
// caller can say "the threshold is unknown" rather than "the threshold is zero".
func emptyFunc(title, body string, minCellN *int) template.HTML {
	state := EmptyState{Title: title, Body: body}
	if minCellN != nil {
		state.HasN = true
		state.MinCellN = *minCellN
	}
	return state.HTML()
}

// Prose values are escaped with the reference build's entity spellings, and the
// functions below keep that guarantee at the call site: a template that prints
// {{ hotRating }} or {{ .SomeSentence }} cannot accidentally emit a bare quote.
func proseFunc(f func(string) string) func(string) template.HTML {
	return func(value string) template.HTML { return textHTML(f(value)) }
}

func prose0(f func() string) func() template.HTML {
	return func() template.HTML { return textHTML(f()) }
}

func constProse(value string) func() template.HTML {
	return func() template.HTML { return textHTML(value) }
}

// Prose marks a Go string as already-safe page text for the templates.
func Prose(value string) template.HTML { return textHTML(value) }
