package vault

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// systemFiles lists vault-internal files that should not appear in search results or page counts.
var systemFiles = map[string]bool{
	"index.md": true, "log.md": true, "hot.md": true,
	".manifest.json": true, "AGENTS.md": true,
}

// --  Public interfaces -- 

// Reader provides read access to vault pages and metadata.
type Reader interface {
	ReadIndex(ctx context.Context, vault string) ([]IndexEntry, error)
	ReadPage(ctx context.Context, vault string, relPath string) (*Page, error)
	Search(ctx context.Context, vault string, keyword string) ([]SearchResult, error)
	Status(ctx context.Context) (*Status, error)
}

// Writer provides write access to vault pages and logs.
type Writer interface {
	AppendLog(ctx context.Context, vault string, entry string) error
	WritePage(ctx context.Context, vault string, relPath string, content []byte) error
}

// --  Data types -- 

// IndexEntry represents a row in index.md.
type IndexEntry struct {
	Title string
	Path  string
}

// Page is a parsed wiki page with YAML frontmatter.
type Page struct {
	Title      string
	Tags       []string
	Category   string
	Created    string
	Updated    string
	Body       string
	RawContent []byte
}

// SearchResult is a keyword match in a vault page.
type SearchResult struct {
	Path    string
	Title   string
	Snippet string // surrounding context of the match
}

// Status holds vault-level statistics.
type Status struct {
	Personal struct {
		Path       string
		PageCount  int
		TotalBytes int64
	}
	Agent struct {
		Path       string
		PageCount  int
		TotalBytes int64
	}
}

// --  File-based implementation -- 

// FileReader reads vault pages directly from the filesystem.
// It does NOT shell out to obsidian-wiki CLI.
type FileReader struct {
	personalPath string
	agentPath    string
}

// NewFileReader creates a filesystem-backed vault reader.
func NewFileReader(personalPath, agentPath string) *FileReader {
	return &FileReader{
		personalPath: personalPath,
		agentPath:    agentPath,
	}
}

// FileWriter writes vault pages directly to the filesystem.
type FileWriter struct {
	personalPath string
	agentPath    string
}

// NewFileWriter creates a filesystem-backed vault writer.
func NewFileWriter(personalPath, agentPath string) *FileWriter {
	return &FileWriter{
		personalPath: personalPath,
		agentPath:    agentPath,
	}
}

// vaultPath resolves the named vault to its absolute path.
func (r *FileReader) vaultPath(name string) (string, error) {
	switch name {
	case "personal":
		return r.personalPath, nil
	case "agent":
		return r.agentPath, nil
	default:
		return "", fmt.Errorf("vault: unknown vault %q", name)
	}
}

// ReadIndex parses index.md lines into entries.
func (r *FileReader) ReadIndex(ctx context.Context, vaultName string) ([]IndexEntry, error) {
	vp, err := r.vaultPath(vaultName)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(vp, "index.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("vault: read index: %w", err)
	}

	var entries []IndexEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "- ") {
			continue
		}
		// Expected format: "- [Title](path/to/page.md)"
		line = strings.TrimPrefix(line, "- ")
		if idx := strings.Index(line, "]("); idx > 0 {
			title := line[1:idx]
			rest := line[idx+2:]
			if end := strings.Index(rest, ")"); end > 0 {
				entries = append(entries, IndexEntry{
					Title: title,
					Path:  rest[:end],
				})
			}
		}
	}
	return entries, nil
}

// ReadPage reads a single wiki page with YAML frontmatter parsing.
func (r *FileReader) ReadPage(ctx context.Context, vaultName string, relPath string) (*Page, error) {
	vp, err := r.vaultPath(vaultName)
	if err != nil {
		return nil, err
	}

	fullPath := filepath.Join(vp, relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("vault: read page %s: %w", relPath, err)
	}

	return ParsePage(data)
}

// Search performs a case-insensitive keyword search across vault markdown files.
func (r *FileReader) Search(ctx context.Context, vaultName string, keyword string) ([]SearchResult, error) {
	vp, err := r.vaultPath(vaultName)
	if err != nil {
		return nil, err
	}

	tokens := tokenizeQuery(strings.ToLower(keyword))
	if len(tokens) == 0 {
		return nil, nil
	}

	// BM25-ranked search over vault markdown files.
	bm25Results, err := bm25Search(vp, tokens, systemFiles)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	for _, br := range bm25Results {
		fullPath := filepath.Join(vp, br.Path)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		// Extract a snippet around the first matched token.
		content := strings.ToLower(string(data))
		snippet := extractSnippet(string(data), content, tokens)

		title := strings.TrimSuffix(filepath.Base(br.Path), ".md")
		if page, err := ParsePage(data); err == nil && page.Title != "" {
			title = page.Title
		}
		results = append(results, SearchResult{
			Path:    br.Path,
			Title:   title,
			Snippet: snippet,
		})
	}

	return results, nil
}

// extractSnippet finds the first occurrence of any query token and returns
// surrounding context text.
func extractSnippet(original, contentLower string, tokens []string) string {
	for _, tok := range tokens {
		idx := strings.Index(contentLower, tok)
		if idx < 0 {
			continue
		}
		start := idx - 40
		if start < 0 {
			start = 0
		}
		end := idx + len(tok) + 40
		if end > len(original) {
			end = len(original)
		}
		return original[start:end]
	}
	// Fallback: first 120 chars.
	if len(original) > 120 {
		return original[:120]
	}
	return original
}

// Status returns vault-level statistics for both vaults.
func (r *FileReader) Status(ctx context.Context) (*Status, error) {
	var s Status
	s.Personal.Path = r.personalPath
	s.Agent.Path = r.agentPath

	if err := statVault(r.personalPath, &s.Personal.PageCount, &s.Personal.TotalBytes); err != nil {
		return nil, fmt.Errorf("vault: personal status: %w", err)
	}
	if err := statVault(r.agentPath, &s.Agent.PageCount, &s.Agent.TotalBytes); err != nil {
		return nil, fmt.Errorf("vault: agent status: %w", err)
	}
	return &s, nil
}

// ParsePage extracts YAML frontmatter and body from raw markdown.
func ParsePage(data []byte) (*Page, error) {
	// Strip UTF-8 BOM if present (Windows PowerShell/Out-File adds it).
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	content := string(data)
	page := &Page{RawContent: data}

	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---\n")
		if end > 0 {
			fm := content[4 : 4+end]
			var frontmatter struct {
				Title    string   `yaml:"title"`
				Tags     []string `yaml:"tags"`
				Category string   `yaml:"category"`
				Created  string   `yaml:"created"`
				Updated  string   `yaml:"updated"`
			}
			if err := yaml.Unmarshal([]byte(fm), &frontmatter); err == nil {
				page.Title = frontmatter.Title
				page.Tags = frontmatter.Tags
				page.Category = frontmatter.Category
				page.Created = frontmatter.Created
				page.Updated = frontmatter.Updated
			}
			page.Body = strings.TrimSpace(content[4+end+5:])
		} else {
			page.Body = strings.TrimSpace(content)
		}
	} else {
		page.Body = strings.TrimSpace(content)
	}

	if page.Title == "" {
		// Fallback: use filename without extension as title.
		page.Title = "untitled"
	}
	return page, nil
}

// --  FileWriter methods -- 

func (w *FileWriter) vaultPath(name string) (string, error) {
	switch name {
	case "personal":
		return w.personalPath, nil
	case "agent":
		return w.agentPath, nil
	default:
		return "", fmt.Errorf("vault: unknown vault %q", name)
	}
}

// AppendLog adds a timestamped line to the vault's log.md.
func (w *FileWriter) AppendLog(ctx context.Context, vaultName string, entry string) error {
	vp, err := w.vaultPath(vaultName)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(filepath.Join(vp, "log.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("vault: append log: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "- %s\n", entry)
	return err
}

// WritePage writes a wiki page to the vault.
func (w *FileWriter) WritePage(ctx context.Context, vaultName string, relPath string, content []byte) error {
	vp, err := w.vaultPath(vaultName)
	if err != nil {
		return err
	}

	fullPath := filepath.Join(vp, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("vault: write page: %w", err)
	}
	return os.WriteFile(fullPath, content, 0644)
}

// --  helpers -- 

func statVault(root string, count *int, totalBytes *int64) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			// Skip hidden dirs and internal vault scaffolding.
			if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		// Skip system/tracking files --  they match everything and crowd out real pages.
		if systemFiles[filepath.Base(path)] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		*count++
		*totalBytes += info.Size()
		return nil
	})
}

// --- Entity extraction for RAG trigger routing ---

// entityPageExcludePatterns are title substrings that indicate a page is NOT a trigger entity.
var entityPageExcludePatterns = []string{
	"index", "category", "method", "architecture", "pattern", "guide", "manual",
	"spec", "log", "template", "overview", "description", "record",
	"development", "design", "summary", "note", "learning", "tutorial",
}

// entityTriggerCategories are categories whose pages are treated as trigger entities.
var entityTriggerCategories = map[string]bool{
	"person":  true,
	"project": true,
	"entity":  true,
}

// entityTriggerTags are tags whose pages are treated as trigger entities.
var entityTriggerTags = map[string]bool{
	"person":                true,
	"entity":                true,
	"project":               true,
	"knowledge-base/person": true,
}

// ExtractTriggerEntities walks the vault and collects entity names
// that should trigger RAG search when mentioned in a user query.
//
// Entities are extracted from:
//   1. Pages whose frontmatter category matches entityTriggerCategories.
//   2. Pages whose frontmatter tags match entityTriggerTags.
//   3. Short titles (<=6 runes) that do not match wiki meta patterns.
//
// extraEntities provides a manual override list merged into the result.
// Duplicates are removed.
func ExtractTriggerEntities(personalPath string, extraEntities []string) ([]string, error) {
	seen := make(map[string]bool)
	for _, e := range extraEntities {
		e = strings.TrimSpace(e)
		if e != "" {
			seen[e] = true
		}
	}

	err := filepath.WalkDir(personalPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable files
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		base := filepath.Base(path)
		if systemFiles[base] {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		page, parseErr := ParsePage(data)
		if parseErr != nil || page == nil {
			return nil
		}

		title := strings.TrimSpace(page.Title)
		if title == "" || title == "untitled" {
			return nil
		}

		// Check category match.
		if entityTriggerCategories[strings.ToLower(page.Category)] {
			seen[title] = true
			return nil
		}

		// Check tag match.
		for _, tag := range page.Tags {
			if entityTriggerTags[strings.ToLower(tag)] {
				seen[title] = true
				return nil
			}
		}

		// Short-title heuristic: short names without wiki meta words.
		if len([]rune(title)) <= 6 {
			titleLower := strings.ToLower(title)
			excluded := false
			for _, pat := range entityPageExcludePatterns {
				if strings.Contains(titleLower, pat) {
					excluded = true
					break
				}
			}
			if !excluded {
				seen[title] = true
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("vault: extract entities: %w", err)
	}

	entities := make([]string, 0, len(seen))
	for e := range seen {
		entities = append(entities, e)
	}
	return entities, nil
}
