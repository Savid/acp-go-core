package lifecycle

import "crypto/rand"

// NewIncarnation names one newly opened lifecycle stream. Session identities
// survive process restarts, so fresh entropy keeps a resumed source from
// reopening a stream name an earlier process already published.
func NewIncarnation(sessionID string) string {
	return sessionID + ":" + rand.Text()
}
