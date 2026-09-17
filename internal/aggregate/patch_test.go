package aggregate

import (
	"errors"
	"strings"
	"testing"
)

// The patch is chosen twice: once from the envelope, before the audit row is
// opened, and once from the feature spill, which is what the artifacts are
// actually reduced from. These pin the two statements' agreement and the row the
// chooser reports, because both are load-bearing in a way a tally hides: the
// second choice can refuse an archive the first accepted, and a disagreement
// between them would publish a partition whose boundary was not chosen from the
// rows that were published.

func TestChoosePatchReportsTheRowItChose(t *testing.T) {
	t.Parallel()

	rows := []patchRow{
		{Patch: "16.17", Matches: 412, ParticipantRows: 4120},
		{Patch: "16.18", Matches: 97, ParticipantRows: 968},
	}

	for _, tc := range []struct {
		name   string
		pinned string
		want   patchRow
	}{
		{
			// The newest patch wins, and the counts reported are that patch's
			// own: the envelope row is what the audit trail quotes, so a total
			// across the window would describe a partition that was not built.
			name: "the newest patch and its own counts",
			want: rows[1],
		},
		{
			name:   "a pinned patch that is present",
			pinned: "16.17",
			want:   rows[0],
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := choosePatch(rows, tc.pinned)
			if err != nil {
				t.Fatalf("choosePatch: %v", err)
			}
			if got != tc.want {
				t.Errorf("choosePatch = %+v, want %+v", got, tc.want)
			}
		})
	}

	// A pinned patch that the window does not hold is the operator's typo, and
	// the choice is refused rather than falling back to the newest patch: the
	// build would otherwise publish a partition nobody asked for.
	if _, err := choosePatch(rows, "16.19"); !errors.Is(err, ErrEmptyWindow) {
		t.Errorf("choosePatch with an absent pin = %v, want %v", err, ErrEmptyWindow)
	}
	if _, err := choosePatch(nil, ""); !errors.Is(err, ErrEmptyWindow) {
		t.Errorf("choosePatch with no rows = %v, want %v", err, ErrEmptyWindow)
	}

	// A game version the normaliser cannot read cannot be a partition boundary.
	if _, err := choosePatch([]patchRow{{Patch: "not-a-patch"}}, ""); !errors.Is(err, ErrMalformedArchive) {
		t.Errorf("choosePatch with an unreadable patch = %v, want %v", err, ErrMalformedArchive)
	}
}

func TestCheckPatchEvidenceComparesEveryColumn(t *testing.T) {
	t.Parallel()

	agreed := []patchRow{
		{Patch: "16.17", Matches: 4, ParticipantRows: 40},
		{Patch: "16.18", Matches: 2, ParticipantRows: 20},
	}
	if err := checkPatchEvidence(agreed, agreed); err != nil {
		t.Errorf("checkPatchEvidence on equal lists = %v, want nil", err)
	}

	for _, tc := range []struct {
		name     string
		envelope []patchRow
		features []patchRow
		want     string
	}{
		{
			// The narrow case the comparison exists for: the same patch set,
			// with a match that went missing between the two statements.
			name:     "a patch that lost a match",
			envelope: agreed,
			features: []patchRow{{Patch: "16.17", Matches: 4, ParticipantRows: 40}, {Patch: "16.18", Matches: 1, ParticipantRows: 10}},
			want:     "2 matches and 20 participant rows in the envelope, but 1 and 10",
		},
		{
			name:     "a patch that lost a participant row",
			envelope: agreed,
			features: []patchRow{{Patch: "16.17", Matches: 4, ParticipantRows: 41}, {Patch: "16.18", Matches: 2, ParticipantRows: 20}},
			want:     "4 matches and 40 participant rows in the envelope, but 4 and 41",
		},
		{
			name:     "a patch the envelope never saw",
			envelope: []patchRow{{Patch: "16.18", Matches: 2, ParticipantRows: 20}},
			features: agreed,
			want:     "carries matches in the extracted features but none in the envelope",
		},
		{
			name:     "a patch the features never produced",
			envelope: agreed,
			features: []patchRow{{Patch: "16.18", Matches: 2, ParticipantRows: 20}},
			want:     "the envelope holds 2 patches in the window and the extracted features hold 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkPatchEvidence(tc.envelope, tc.features)
			if err == nil {
				t.Fatal("checkPatchEvidence accepted lists that disagree")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("checkPatchEvidence = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}
