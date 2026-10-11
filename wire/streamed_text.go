package wire

import "strings"

// StreamedText accumulates, per native item, the assistant text a turn has
// streamed, so a terminal full-message frame emits only the suffix its deltas
// did not carry. Text grows in a strings.Builder, so a long stream costs
// linear copying. The zero value is ready; it is not safe for concurrent use.
type StreamedText struct {
	items map[string]*strings.Builder
}

// Add records a delta streamed for key.
func (s *StreamedText) Add(key, delta string) {
	if delta == "" {
		return
	}

	s.item(key).WriteString(delta)
}

// Suffix returns the part of final not yet streamed for key and records it as
// streamed. A final text that does not extend what was streamed yields
// nothing, so assistant text stays append-only.
func (s *StreamedText) Suffix(key, final string) string {
	item := s.item(key)

	suffix := UnstreamedSuffix(item.String(), final)
	item.WriteString(suffix)

	return suffix
}

// Text returns everything streamed for key.
func (s *StreamedText) Text(key string) string {
	if item := s.items[key]; item != nil {
		return item.String()
	}

	return ""
}

// Forget releases key's text once its item is complete.
func (s *StreamedText) Forget(key string) { delete(s.items, key) }

// Reset releases every item's text.
func (s *StreamedText) Reset() { clear(s.items) }

func (s *StreamedText) item(key string) *strings.Builder {
	if s.items == nil {
		s.items = make(map[string]*strings.Builder)
	}

	item := s.items[key]
	if item == nil {
		item = new(strings.Builder)
		s.items[key] = item
	}

	return item
}
