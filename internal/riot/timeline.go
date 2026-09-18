package riot

import (
	"encoding/json"
)

// TimelineDTO is the subset of MATCH-V5
// GET /lol/match/v5/matches/{matchId}/timeline that this project reads:
// identity, the frame interval, and the frames themselves.
//
// The events inside a frame are deliberately NOT modelled. The event set is
// wide, polymorphic and changed repeatedly without notice - it carries twenty
// event types with per-type fields, several of which are documented wrongly or
// not at all - and the aggregation reads the verbatim payload with DuckDB's
// `json_extract`, exactly as it already does for the summary's events. Writing
// a struct per event type would freeze a guess per event type and buy nothing
// the JSON path does not already have.
//
// Frames carry typed participantFrames because they are the per-minute facts
// (CS, XP, gold, level, position) and because their shape is stable enough to
// depend on. Two hazards are worth naming here, because a reader who learns
// them from this comment does not have to learn them from a wrong chart:
//
//   - `participantFrames` is a JSON OBJECT keyed by the string form of the
//     1-based participantId, not an array, so it has no order to rely on.
//   - The frame interval is not the frame spacing. Riot has emitted frames at
//     roughly 60.5-71.3 second gaps since around patch 16.1, so the minute a
//     frame belongs to is derived from its timestamp. Frame index is not a
//     clock.
type TimelineDTO struct {
	Metadata TimelineMetadata `json:"metadata"`
	Info     TimelineInfo     `json:"info"`

	// raw is the response body this value was decoded from, mirroring
	// MatchDTO.raw: the archive stores bytes, not a re-encoding.
	raw []byte
}

// TimelineMetadata carries the match id and the participant puuids. The puuid
// list is the join back to the summary's participants by puuid; the summary's
// own participant array carries the 1-based participantId that keys the frames,
// so the two orders must not be conflated.
type TimelineMetadata struct {
	MatchID      string   `json:"matchId"`
	Participants []string `json:"participants"`
}

// TimelineInfo is the frame envelope.
type TimelineInfo struct {
	// FrameInterval is the interval Riot nominally intended, in milliseconds.
	// It is 60000 for a normal game, 0 for a game that was aborted, and it is
	// not a guarantee about the actual spacing - see TimelineDTO's comment.
	FrameInterval int64 `json:"frameInterval"`
	// Frames is typed as raw messages rather than as TimelineFrame so that a
	// frame which fails to decode is still retained in the archive payload.
	// The client leaves it unmodelled on purpose: nothing in Go reads a frame
	// field, because minute-level extraction happens in DuckDB.
	Frames []json.RawMessage `json:"frames"`
}

// TimelineRawPayload returns the exact response body this timeline was decoded
// from, when the client fetched it, and a re-encoding of the typed fields
// otherwise. It mirrors MatchDTO.RawPayload for the same reason: the archive
// promises the bytes, and a re-encoding cannot keep that promise.
func (t TimelineDTO) TimelineRawPayload() []byte {
	if len(t.raw) > 0 {
		out := make([]byte, len(t.raw))
		copy(out, t.raw)
		return out
	}
	encoded, err := json.Marshal(t)
	if err != nil {
		return nil
	}
	return encoded
}

// retainTimelineRaw records the body a timeline was decoded from.
func (t *TimelineDTO) retainTimelineRaw(body []byte) {
	t.raw = body
}
