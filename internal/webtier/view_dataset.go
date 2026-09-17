package webtier

import (
	"errors"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The schema.org Dataset node, which is a claim about where the numbers came
// from and therefore only supportable by an ingested snapshot.
//
// jsonLdDataset in the reference build throws on anything but the live state
// rather than rendering a weaker sentence, because `measurementTechnique`
// reading "Aggregated from Riot MATCH-V5 match records" over synthetic fixtures
// is exactly the false provenance claim this site exists not to make. The error
// is carried over: a build that would publish that node stops instead.

// errNotLive is the refusal. It is returned rather than logged, so a caller that
// ignores the state check gets an error and not a false claim.
var errNotLive = errors.New("jsonLdDataset: refusing to describe a non-live data state as a Dataset of Riot match records")

type datasetVariable struct {
	Name string
	Unit string
}

type datasetNodeInput struct {
	URL          string
	Name         string
	Description  string
	Patch        string
	SourceWindow aggmodel.Window
	GeneratedAt  time.Time
	MinCellN     int
	SampleSize   int
	Variables    []datasetVariable
	State        string
}

// datasetNode is lib/seo.ts's jsonLdDataset, field for field and in the same
// order, because the serialised node is what a crawler reads.
type datasetNodeView struct {
	Context              string            `json:"@context"`
	Type                 string            `json:"@type"`
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	URL                  string            `json:"url"`
	InLanguage           string            `json:"inLanguage"`
	IsAccessibleForFree  bool              `json:"isAccessibleForFree"`
	DateModified         string            `json:"dateModified"`
	TemporalCoverage     string            `json:"temporalCoverage"`
	Creator              *OrganizationNode `json:"creator"`
	VariableMeasured     []propertyValue   `json:"variableMeasured"`
	MeasurementTechnique string            `json:"measurementTechnique"`
	Size                 string            `json:"size"`
	Citation             string            `json:"citation"`
}

type propertyValue struct {
	Type     string `json:"@type"`
	Name     string `json:"name"`
	UnitText string `json:"unitText"`
}

func datasetNode(input datasetNodeInput) (datasetNodeView, error) {
	if DataState(input.State) != StateLive {
		return datasetNodeView{}, errNotLive
	}
	variables := make([]propertyValue, 0, len(input.Variables))
	for _, variable := range input.Variables {
		variables = append(variables, propertyValue{Type: "PropertyValue", Name: variable.Name, UnitText: variable.Unit})
	}
	return datasetNodeView{
		Context:             "https://schema.org",
		Type:                "Dataset",
		Name:                input.Name,
		Description:         input.Description,
		URL:                 input.URL,
		InLanguage:          "en",
		IsAccessibleForFree: true,
		DateModified:        input.GeneratedAt.UTC().Format(time.RFC3339),
		TemporalCoverage:    input.SourceWindow.From + "/" + input.SourceWindow.To,
		Creator:             &OrganizationNode{Type: "Organization", Name: SiteName},
		VariableMeasured:    variables,
		MeasurementTechnique: "Aggregated from Riot MATCH-V5 match records for patch " + input.Patch +
			"; rates are published only for cells holding at least n = " + IntegerAny(float64(input.MinCellN)) + " games.",
		Size:     IntegerAny(float64(input.SampleSize)) + " games in the published snapshot",
		Citation: "League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc.",
	}, nil
}
