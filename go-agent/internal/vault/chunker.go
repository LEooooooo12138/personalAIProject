package vault

import "strings"

// ── Page Chunker ──
//
// Splits wiki pages into semantic chunks at heading boundaries (##).
// Each chunk carries source page metadata for provenance tracking.
//
// Strategy:
//   - Pages shorter than 1500 chars are kept as a single chunk.
//   - Longer pages are split at `## ` heading boundaries.
//   - Adjacent sections share a 150-char overlap window so that
//     sentences spanning a heading boundary are retrievable from both sides.
//   - If a section is still too long, it is further split at paragraph boundaries.

const (
	chunkMaxLen     = 1500 // max chars per chunk
	chunkMinLen     = 100  // min chars for a chunk to be useful
	chunkOverlapLen = 150  // chars from previous section prepended as context bridge
)

// Chunk is a searchable segment of a wiki page.
type Chunk struct {
	ID           string // unique: "pagePath#section-N"
	PagePath     string // relative path to the source page
	PageTitle    string // source page title
	SectionTitle string // heading title for this chunk (empty for first chunk)
	Content      string // chunk body text (includes overlap prefix from preceding section)
	Tags         []string
	Category     string
}

// ChunkPage splits a wiki page body into chunks at `## ` heading boundaries
// with a sliding overlap window between adjacent sections.
func ChunkPage(page *Page) []Chunk {
	body := page.Body
	if body == "" {
		return nil
	}

	// Short page: single chunk — no overlap needed.
	if len([]rune(body)) <= chunkMaxLen {
		return []Chunk{{
			ID:           pageToChunkID(page, 0),
			PagePath:     "", // filled by caller
			PageTitle:    page.Title,
			SectionTitle: "",
			Content:      body,
			Tags:         page.Tags,
			Category:     page.Category,
		}}
	}

	// Split at ## headings.
	sections := splitByH2(body)
	var chunks []Chunk
	var prevTail string // last chunkOverlapLen chars of the previous section

	for i, sec := range sections {
		rawBody := strings.TrimSpace(sec.body)
		if len([]rune(rawBody)) < chunkMinLen {
			// Still track its tail for the next section's overlap.
			prevTail = lastNChars(rawBody, chunkOverlapLen)
			continue
		}

		// Prepend overlap from the previous section so that queries
		// targeting boundary-spanning content can still match this chunk.
		content := rawBody
		if prevTail != "" {
			content = "[↑] " + prevTail + "\n\n" + content
		}

		// Further split very long sections.
		subs := splitLongSection(content)
		for _, sub := range subs {
			chunks = append(chunks, Chunk{
				ID:           pageToChunkID(page, len(chunks)),
				PageTitle:    page.Title,
				SectionTitle: sec.heading,
				Content:      sub,
				Tags:         page.Tags,
				Category:     page.Category,
			})
		}

		// Save the tail of this section for the next section's overlap.
		prevTail = lastNChars(rawBody, chunkOverlapLen)
		_ = i // suppress unused
	}
	if len(chunks) == 0 {
		return []Chunk{{
			ID:           pageToChunkID(page, 0),
			PageTitle:    page.Title,
			SectionTitle: "",
			Content:      body,
			Tags:         page.Tags,
			Category:     page.Category,
		}}
	}
	return chunks
}

type h2Section struct {
	heading string
	body    string
}

func splitByH2(body string) []h2Section {
	var sections []h2Section
	parts := strings.Split(body, "\n## ")
	if len(parts) <= 1 {
		return []h2Section{{heading: "", body: body}}
	}
	// First part is before any ## heading.
	sections = append(sections, h2Section{heading: "", body: parts[0]})
	for _, part := range parts[1:] {
		idx := strings.Index(part, "\n")
		heading := part
		rest := ""
		if idx >= 0 {
			heading = part[:idx]
			rest = part[idx+1:]
		}
		sections = append(sections, h2Section{heading: heading, body: rest})
	}
	return sections
}

func splitLongSection(content string) []string {
	if len([]rune(content)) <= chunkMaxLen {
		return []string{content}
	}
	// Split at paragraph boundaries (double newline).
	var chunks []string
	paras := strings.Split(content, "\n\n")
	current := ""
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len([]rune(current))+len([]rune(p)) > chunkMaxLen && current != "" {
			chunks = append(chunks, current)
			current = p
		} else {
			if current != "" {
				current += "\n\n"
			}
			current += p
		}
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}

// lastNChars returns the last n characters (runes) of s.
// If s is shorter than n, returns the whole string.
func lastNChars(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

func pageToChunkID(page *Page, idx int) string {
	return page.Title + "#section-" + string(rune('0'+idx%10))
}
