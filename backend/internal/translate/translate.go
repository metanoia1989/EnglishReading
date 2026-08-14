package translate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const myMemoryURL = "https://api.mymemory.translated.net/get"

// Client is a small cache-friendly wrapper around the free MyMemory
// translation API (no key required, rate-limited for anonymous use).
type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: 20 * time.Second}}
}

func Hash(source, target string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(source) + "\x00" + target))
	return hex.EncodeToString(sum[:])
}

// Translate translates English text into Simplified Chinese through MyMemory.
// Long paragraphs are split into API-sized chunks and re-joined with newlines.
func (c *Client) Translate(text, from, to string) (string, error) {
	chunks := chunkByRunes(strings.TrimSpace(text), 450)
	if len(chunks) == 0 {
		return "", nil
	}
	results := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		got, err := c.translateOne(chunk, from, to)
		if err != nil {
			return "", err
		}
		results = append(results, strings.TrimSpace(got))
	}
	return strings.Join(results, "\n"), nil
}

func chunkByRunes(s string, max int) []string {
	if utf8.RuneCountInString(s) <= max {
		return []string{s}
	}
	var chunks []string
	var b strings.Builder
	count := 0
	flush := func() {
		if b.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(b.String()))
			b.Reset()
			count = 0
		}
	}
	for _, r := range s {
		if count >= max {
			flush()
		}
		b.WriteRune(r)
		count++
	}
	flush()
	return chunks
}

type myMemoryResponse struct {
	ResponseData struct {
		TranslatedText string `json:"translatedText"`
	} `json:"responseData"`
	ResponseStatus json.RawMessage `json:"responseStatus"`
}

func (c *Client) translateOne(text, from, to string) (string, error) {
	q := url.Values{}
	q.Set("q", text)
	q.Set("langpair", from+"|"+to)
	req, err := http.NewRequest(http.MethodGet, myMemoryURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "EnglishReading/1.0 (learning app)")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("translation service unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("read translation response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("translation service returned HTTP %d", resp.StatusCode)
	}
	var payload myMemoryResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode translation response: %w", err)
	}
	translated := strings.TrimSpace(payload.ResponseData.TranslatedText)
	if translated == "" || strings.Contains(translated, "MYMEMORY WARNING") {
		return "", fmt.Errorf("free translation quota exhausted, try again later")
	}
	return translated, nil
}
