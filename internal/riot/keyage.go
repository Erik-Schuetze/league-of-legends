package riot

import "time"

// Age reports how long the current key has been in use by this process, and
// whether that is known.
//
// It lives on the client and not only on the provider because the crawl worker
// discovers this surface by asserting on its Fetcher - which is the client -
// and never sees the provider behind it. The assertion is what publishes
// lolstats_riot_key_age_seconds, and until this method existed it was satisfied
// by the crawl test fake alone: the gauge was scraped as a constant 0 in
// production while CI stayed green, because a fake cannot notice that the
// production type it stands in for is missing a method.
func (c *Client) Age() (time.Duration, bool) {
	if c.keys == nil {
		return 0, false
	}
	return c.keys.AgeKnown()
}

// The crawl worker's key-age assertion, pinned here as well as at the consumer:
// a client that loses Age, or changes its signature, stops compiling instead of
// quietly returning the gauge to a zero that reads like a key rotated a moment
// ago.
var _ interface {
	Age() (time.Duration, bool)
} = (*Client)(nil)
