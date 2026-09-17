package riot

import "encoding/json"

// RawPayload returns the exact response body this match was decoded from, when
// the client fetched it, and a re-encoding of the typed fields otherwise.
//
// The archive wants the former. MATCH-V5 responses carry fields v1 does not
// model, the retention promise is that a later transform can read them without
// a re-crawl, and a re-encoding cannot keep a promise about bytes it never saw.
// The fallback exists so a value built in a test or from a fixture still has a
// payload, which keeps the archive writer free of a "was this fetched" branch.
func (m MatchDTO) RawPayload() []byte {
	if len(m.raw) > 0 {
		out := make([]byte, len(m.raw))
		copy(out, m.raw)
		return out
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return encoded
}

// retainRaw records the body a value was decoded from, so that RawPayload can
// return bytes rather than a re-encoding. Only the client calls it.
func (m *MatchDTO) retainRaw(body []byte) {
	m.raw = body
}

// LeagueEntriesPayload returns the JSON array of one LEAGUE-V4 page. Elements
// the client decoded keep their original bytes; an entry built in code is
// re-encoded.
func LeagueEntriesPayload(entries []LeagueEntryDTO) ([]byte, error) {
	elements := make([]json.RawMessage, 0, len(entries))
	for i := range entries {
		if len(entries[i].raw) > 0 {
			elements = append(elements, entries[i].raw)
			continue
		}
		encoded, err := json.Marshal(entries[i])
		if err != nil {
			return nil, err
		}
		elements = append(elements, encoded)
	}
	return json.Marshal(elements)
}

// decodeLeagueEntries decodes a LEAGUE-V4 array element by element so each
// entry keeps the exact bytes it arrived as.
func decodeLeagueEntries(body []byte) ([]LeagueEntryDTO, error) {
	var elements []json.RawMessage
	if err := json.Unmarshal(body, &elements); err != nil {
		return nil, err
	}
	entries := make([]LeagueEntryDTO, 0, len(elements))
	for _, element := range elements {
		var entry LeagueEntryDTO
		if err := json.Unmarshal(element, &entry); err != nil {
			return nil, err
		}
		entry.raw = element
		entries = append(entries, entry)
	}
	return entries, nil
}
