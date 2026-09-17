package webtier

import "html/template"

// The three conversions in this file are the package's unescaped-markup
// boundary, kept in one place so the trust argument can be read once.
//
// Everything converted here is markup this package built itself: literal tag
// and attribute names ported from the reference components, plus values that
// went through textHTML (components.go), which escapes on the way in. What a
// request contributes - the path and the query string - reaches a page as
// values in template actions, where html/template escapes them, or through
// textHTML; it is never converted here. The page body is this package's own
// renderer output, and the JSON-LD node is serialised from values the same way.

// trustedHTML marks a page body or a component's markup as HTML.
func trustedHTML(markup string) template.HTML { // #nosec G203 -- markup is built here, not passed in from a request
	return template.HTML(markup)
}

// trustedCSS marks an inline style chunk as CSS. The chunk is a slice of the
// embedded stylesheet, selected by scopedCSS, and carries no request text.
func trustedCSS(css string) template.CSS { // #nosec G203 -- a slice of the embedded stylesheet
	return template.CSS(css)
}

// trustedJS marks a JSON-LD script body as JavaScript. JSONLDNode serialises it
// from values built by the views, never from a request.
func trustedJS(script string) template.JS { // #nosec G203 -- serialised from view values, not from a request
	return template.JS(script)
}
