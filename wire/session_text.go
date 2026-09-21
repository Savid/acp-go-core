package wire

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"
)

// sessionTitleMaxRunes bounds the title a sibling derives from the first
// prompt text.
const sessionTitleMaxRunes = 256

// SelectPositionEncoding picks UTF-8 when the client offers it and UTF-16
// otherwise.
func SelectPositionEncoding(encodings []acp.PositionEncodingKind) acp.PositionEncodingKind {
	if slices.Contains(encodings, acp.PositionEncodingKindUtf8) {
		return acp.PositionEncodingKindUtf8
	}

	return acp.PositionEncodingKindUtf16
}

// PromptTitle derives the session title from the first text block that
// normalizes to a non-empty string.
func PromptTitle(prompt []acp.ContentBlock) string {
	for _, block := range prompt {
		if block.Text == nil {
			continue
		}

		if title := NormalizeTitle(block.Text.Text); title != "" {
			return title
		}
	}

	return ""
}

// NormalizeTitle collapses whitespace and bounds the title to
// sessionTitleMaxRunes runes, ending a truncated title with an ellipsis.
func NormalizeTitle(text string) string {
	title := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(title) <= sessionTitleMaxRunes {
		return title
	}

	runes := []rune(title)

	return strings.TrimSpace(string(runes[:sessionTitleMaxRunes-3])) + "..."
}

// ValidCommandName is the one sanitizer for advertised slash-command names. It
// rejects an empty name, a name containing a slash, invalid UTF-8, and any
// Unicode whitespace, control, or format rune.
func ValidCommandName(name string) bool {
	if name == "" || strings.Contains(name, "/") || !utf8.ValidString(name) {
		return false
	}

	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}

	return true
}

// UnstreamedSuffix returns the part of a finalized text that was not already
// streamed. A finalized text that does not extend the streamed prefix yields
// nothing, so assistant text stays append-only.
func UnstreamedSuffix(streamed string, full string) string {
	if streamed == "" {
		return full
	}

	if !strings.HasPrefix(full, streamed) {
		return ""
	}

	return full[len(streamed):]
}

// ContextResourceText renders an embedded text resource as the context
// envelope a harness receives inside the prompt text.
func ContextResourceText(uri string, text string) string {
	escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

	return "\n<context ref=\"" + escape.Replace(uri) + "\">\n" + escape.Replace(text) + "\n</context>"
}

// AudienceIsUserOnly reports whether a block's annotations address only the
// user, which keeps it out of the harness prompt.
func AudienceIsUserOnly(annotations *acp.Annotations) bool {
	return annotations != nil && len(annotations.Audience) == 1 && annotations.Audience[0] == acp.RoleUser
}

// MetaOptionPath names a vendor option member for a refusal, or the options
// object itself when key is empty.
func MetaOptionPath(vendor, key string) string {
	path := "_meta." + vendor + ".options"
	if key == "" {
		return path
	}

	return path + "." + key
}
