package store

import "strings"

// NormalizeDictText turns the escape sequences ECDICT writes inside a
// definition into real newlines.
//
// ECDICT separates the Chinese gloss from the per-part-of-speech glosses with a
// *literal* two-character `\n` (backslash followed by "n"), not with a line
// feed — e.g. `绝对的, 专制的, 完全的, 独立的\nn. 绝对事物`. The seed JSON stores
// that convention verbatim, so `\n` ends up inside `dictionary.senses_json` and
// is shipped to the client unchanged. The reader renders definitions with
// `white-space: pre-line`, which only breaks on real newlines, so the user sees
// a literal "\n" in the middle of the definition instead of a line break.
//
// Call this wherever dictionary text enters or leaves the database:
//
//   - seed: freshly imported rows are clean at rest;
//   - the dictionary API: databases seeded before this existed are cleaned on
//     read, so no data migration / re-seed is required.
//
// It is idempotent — real newlines and any other text are left untouched.
func NormalizeDictText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	// CRLF first: the replacement below inserts real newlines, which must not
	// be re-examined by the later passes.
	s = strings.ReplaceAll(s, `\r\n`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\r`, "\n")
	return s
}
