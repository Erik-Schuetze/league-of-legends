package webtier

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Schema enforcement, with the same semantics as the frontend's
// `assertArtifact` (web/src/lib/artifacts.ts + web/src/lib/schema-check.ts).
//
// The one behaviour worth stating up front is that an artifact this tier cannot
// understand is a fault, not an empty page: an unknown `schema` version fails
// closed and the request is answered with a 503 and a visible error page. The
// alternative - rendering the layout with blank cells - publishes a page that
// looks like "no games were played" when what actually happened is "this reader
// does not understand the snapshot".
//
// The schema itself is not restated here. It is generated from the artifact
// structs by internal/aggmodel, so the validator and the types cannot drift:
// there is one definition of the contract and both the producer and this reader
// are checked against it.

var (
	schemaOnce sync.Once
	schemaDoc  map[string]any
	schemaFail error
)

// artifactsSchema returns the generated JSON Schema document for agg/v1. The
// document is round-tripped through encoding/json so that its values have the
// shapes a decoded JSON document has ([]any, float64, ...) rather than the Go
// types the generator happens to use.
func artifactsSchema() (map[string]any, error) {
	schemaOnce.Do(func() {
		raw, err := aggmodel.MarshalSchema()
		if err != nil {
			schemaFail = fmt.Errorf("marshal artifact schema: %w", err)
			return
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			schemaFail = fmt.Errorf("decode artifact schema: %w", err)
			return
		}
		schemaDoc = doc
	})
	return schemaDoc, schemaFail
}

// SchemaVersionError is returned when a document declares a schema version this
// reader does not implement. It is separate from a plain validation failure so
// the error page can say which version was found and which is supported.
type SchemaVersionError struct {
	Artifact string
	Found    int
	Want     int
}

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf("%s declares schema version %d, this reader implements version %d",
		e.Artifact, e.Found, e.Want)
}

// ArtifactError is a fault in the aggregate tree: an artifact the manifest
// advertises is missing, unreadable, not JSON, or does not satisfy the schema.
// Every one of them is answered with 503 rather than a partial page.
type ArtifactError struct {
	Path string
	Err  error
}

func (e *ArtifactError) Error() string {
	if e.Path == "" {
		return e.Err.Error()
	}
	return e.Path + ": " + e.Err.Error()
}

func (e *ArtifactError) Unwrap() error { return e.Err }

func artifactFault(path string, format string, args ...any) *ArtifactError {
	return &ArtifactError{Path: path, Err: fmt.Errorf(format, args...)}
}

// validateArtifact decodes raw into a generic value and checks it against the
// generated schema for the named artifact type. typeName is the Go type name
// (`TierList`, `Champion`, `Matchups`, `Manifest`, `StaticChampions`, ...) and
// is also the `$defs` key in the generated document.
//
// `path` is only used to make the error say where the fault is.
func validateArtifact(typeName string, path string, raw []byte) error {
	doc, err := artifactsSchema()
	if err != nil {
		return err
	}
	defs, _ := doc["$defs"].(map[string]any)
	target, ok := defs[typeName].(map[string]any)
	if !ok {
		return fmt.Errorf("no schema definition for artifact %q", typeName)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return artifactFault(path, "is not valid JSON: %v", err)
	}
	if problems := validateValue(target, defs, value, "$"); len(problems) > 0 {
		sort.Strings(problems)
		shown := problems
		if len(shown) > 4 {
			shown = shown[:4]
		}
		suffix := ""
		if len(problems) > len(shown) {
			suffix = fmt.Sprintf(" (and %d more)", len(problems)-len(shown))
		}
		return artifactFault(path, "does not satisfy the %s schema: %s%s",
			typeName, strings.Join(shown, "; "), suffix)
	}
	return nil
}

// declaredSchemaVersion reads the `schema` field of an already-decoded
// envelope, reporting whether one was declared at all. The static projections
// (champions, items, runes, summoner spells, patches) do not declare one, so
// "absent" is a legitimate answer rather than a fault.
func declaredSchemaVersion(raw []byte) (int, bool, error) {
	var envelope struct {
		Schema *int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, false, err
	}
	if envelope.Schema == nil {
		return 0, false, nil
	}
	return *envelope.Schema, true, nil
}

// checkEnvelopeSchema fails closed on an artifact that declares a schema
// version other than the one this reader implements. A document that declares
// no version at all is left to the structural check.
func checkEnvelopeSchema(artifact string, path string, raw []byte) error {
	version, declared, err := declaredSchemaVersion(raw)
	if err != nil {
		return artifactFault(path, "is not valid JSON: %v", err)
	}
	if declared && version != aggmodel.SchemaVersion {
		return &ArtifactError{Path: path, Err: &SchemaVersionError{
			Artifact: artifact,
			Found:    version,
			Want:     aggmodel.SchemaVersion,
		}}
	}
	return nil
}

// validateValue implements the subset of JSON Schema the artifacts use: $ref,
// type, enum, required, properties, items, oneOf and a map's
// additionalProperties. `additionalProperties: false` is deliberately not
// enforced, matching the frontend checker: a snapshot that carries a field this
// reader does not know yet is forward compatible, and rejecting it would make
// every additive producer change a site outage.
func validateValue(schema map[string]any, defs map[string]any, value any, at string) []string {
	if len(schema) == 0 {
		return nil
	}

	if ref, ok := schema["$ref"].(string); ok {
		target := resolveRef(ref, defs)
		if target == nil {
			return []string{fmt.Sprintf("%s: unresolvable $ref %s", at, ref)}
		}
		return validateValue(target, defs, value, at)
	}

	if branches, ok := schema["oneOf"].([]any); ok {
		var last []string
		for _, branch := range branches {
			sub, ok := branch.(map[string]any)
			if !ok {
				continue
			}
			if problems := validateValue(sub, defs, value, at); len(problems) == 0 {
				return nil
			} else {
				last = problems
			}
		}
		if len(last) > 0 {
			return []string{fmt.Sprintf("%s: matches none of the %d artifact types", at, len(branches))}
		}
	}

	if want, ok := schema["type"].(string); ok {
		if !typeMatches(want, value) {
			return []string{fmt.Sprintf("%s: expected %s, found %s", at, want, describeValue(value))}
		}
	}

	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, allowed := range enum {
			if equalJSON(allowed, value) {
				found = true
				break
			}
		}
		if !found {
			return []string{fmt.Sprintf("%s: %v is not one of %s", at, value, enumList(enum))}
		}
	}

	var problems []string
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, name := range required {
				key, _ := name.(string)
				if _, present := typed[key]; !present {
					problems = append(problems, fmt.Sprintf("%s: required property %q is missing", at, key))
				}
			}
		}
		for key, sub := range properties {
			child, present := typed[key]
			if !present {
				continue
			}
			subSchema, ok := sub.(map[string]any)
			if !ok {
				continue
			}
			problems = append(problems, validateValue(subSchema, defs, child, at+"."+key)...)
		}
		if additional, ok := schema["additionalProperties"].(map[string]any); ok {
			for key, child := range typed {
				if _, known := properties[key]; known {
					continue
				}
				problems = append(problems, validateValue(additional, defs, child, at+"."+key)...)
			}
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		if items != nil {
			for i, child := range typed {
				problems = append(problems, validateValue(items, defs, child, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	}
	return problems
}

func resolveRef(ref string, defs map[string]any) map[string]any {
	const prefix = "#/$defs/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	target, _ := defs[strings.TrimPrefix(ref, prefix)].(map[string]any)
	return target
}

func typeMatches(want string, value any) bool {
	switch want {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == math.Trunc(number) && !math.IsInf(number, 0)
	case "number":
		_, ok := value.(float64)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func describeValue(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func equalJSON(left, right any) bool {
	l, lok := left.(string)
	r, rok := right.(string)
	if lok && rok {
		return l == r
	}
	lf, lok := left.(float64)
	rf, rok := right.(float64)
	if lok && rok {
		return lf == rf
	}
	return false
}

func enumList(enum []any) string {
	parts := make([]string, 0, len(enum))
	for _, value := range enum {
		parts = append(parts, fmt.Sprintf("%v", value))
	}
	return strings.Join(parts, " | ")
}
