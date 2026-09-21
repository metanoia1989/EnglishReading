// Package anchor defines how annotation anchors are identified.
//
// An annotation is anchored to
//
//	article_id + paragraph_hash + sentence_index + word_index
//
// The paragraph is identified by a hash of its text rather than by a database
// row id or a positional index. That is what lets article bodies live in files
// without renumbering anything when paragraphs are inserted, removed or
// reordered elsewhere in the article: every untouched paragraph keeps its
// identity, and therefore keeps its annotations.
//
// Editing a paragraph's text does change its hash. That is deliberate — it
// turns silent misalignment into a detectable "the text under this annotation
// changed" condition.
//
// The package is deliberately dependency-free so both the file store, the
// database layer and the one-time migration can share exactly one definition.
package anchor

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Hash is the on-disk and on-column representation: lowercase hex sha256.
const HashLength = 64

// ParagraphHash hashes one paragraph. Leading and trailing whitespace is
// ignored so that a re-indented file does not invalidate annotations, but any
// change inside the text does.
func ParagraphHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

// ArticleHash hashes the anchorable content of an article — its paragraph
// hashes, in order, joined unambiguously.
//
// Metadata (title, level, author, published_at) is deliberately excluded:
// retitling an article must not look like a content change, while any edit to
// the body must.
func ArticleHash(paragraphHashes []string) string {
	var b strings.Builder
	for _, h := range paragraphHashes {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
