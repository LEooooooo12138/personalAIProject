package vault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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

type IndexWriter interface {
	UpdateIndex(context.Context, string, IndexEntry) error
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
	policy       ContentPolicy
}

// NewFileReader creates a filesystem-backed vault reader.
func NewFileReader(personalPath, agentPath string) *FileReader {
	return NewFileReaderWithPolicy(personalPath, agentPath, ContentPolicy{})
}

func NewFileReaderWithPolicy(personalPath, agentPath string, policy ContentPolicy) *FileReader {
	if root, ok := policy.roots["personal"]; ok {
		personalPath = root
	}
	if root, ok := policy.roots["agent"]; ok {
		agentPath = root
	}
	return &FileReader{
		policy:       policy,
		personalPath: personalPath,
		agentPath:    agentPath,
	}
}

// FileWriter writes vault pages directly to the filesystem.
type FileWriter struct {
	mu           sync.Mutex
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vp, err := r.vaultPath(vaultName)
	if err != nil {
		return nil, err
	}

	if !r.policy.Allows(vaultName, "index.md") {
		return nil, fmt.Errorf("vault: index excluded by content policy")
	}
	data, err := readRootFile(vp, "index.md")
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
				if !r.policy.Allows(vaultName, rest[:end]) {
					continue
				}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vp, err := r.vaultPath(vaultName)
	if err != nil {
		return nil, err
	}

	if !r.policy.Allows(vaultName, relPath) || (r.policy.roots != nil && systemFiles[filepath.Base(relPath)]) {
		return nil, fmt.Errorf("vault: page excluded by content policy")
	}
	data, err := readRootFile(vp, relPath)
	if err != nil {
		return nil, fmt.Errorf("vault: read page %s: %w", relPath, err)
	}

	page, err := ParsePage(data)
	if err != nil {
		return nil, fmt.Errorf("vault: parse page %s: %w", relPath, err)
	}
	if page.Title == "" {
		page.Title = strings.TrimSuffix(filepath.Base(relPath), ".md")
	}
	return page, nil
}

// Search performs a case-insensitive keyword search across vault markdown files.
func (r *FileReader) Search(ctx context.Context, vaultName string, keyword string) ([]SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vp, err := r.vaultPath(vaultName)
	if err != nil {
		return nil, err
	}

	tokens := tokenizeQuery(strings.ToLower(keyword))
	if len(tokens) == 0 {
		return nil, nil
	}

	// BM25-ranked search over vault markdown files.
	bm25Results, err := bm25SearchWithPolicy(vp, vaultName, tokens, systemFiles, r.policy)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	for _, br := range bm25Results {
		if !r.policy.Allows(vaultName, br.Path) {
			continue
		}
		data, err := readRootFile(vp, br.Path)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := ParsePage(data)
		if errors.Is(err, ErrInvalidFrontmatter) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if IsInternalPage(page) {
			continue
		}

		// Extract a snippet around the first matched token.
		content := strings.ToLower(string(data))
		snippet := extractSnippet(string(data), content, tokens)

		title := strings.TrimSuffix(filepath.Base(br.Path), ".md")
		if page.Title != "" {
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
	runes := []rune(original)
	for _, tok := range tokens {
		idx := strings.Index(contentLower, tok)
		if idx < 0 {
			continue
		}
		// Lowercasing may change UTF-8 byte widths, but preserves rune positions.
		idx = len([]rune(contentLower[:idx]))
		start := idx - 40
		if start < 0 {
			start = 0
		}
		end := idx + len([]rune(tok)) + 40
		if end > len(runes) {
			end = len(runes)
		}
		return string(runes[start:end])
	}
	// Fallback: first 120 chars.
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return original
}

// Status returns vault-level statistics for both vaults.
func (r *FileReader) Status(ctx context.Context) (*Status, error) {
	var s Status
	s.Personal.Path = r.personalPath
	s.Agent.Path = r.agentPath

	if err := statVaultWithPolicy(r.personalPath, "personal", r.policy, &s.Personal.PageCount, &s.Personal.TotalBytes); err != nil {
		return nil, fmt.Errorf("vault: personal status: %w", err)
	}
	if err := statVaultWithPolicy(r.agentPath, "agent", r.policy, &s.Agent.PageCount, &s.Agent.TotalBytes); err != nil {
		return nil, fmt.Errorf("vault: agent status: %w", err)
	}
	return &s, nil
}

// ErrInvalidFrontmatter identifies a page whose declared metadata cannot be trusted.
var ErrInvalidFrontmatter = errors.New("vault: invalid frontmatter")

// ParsePage rejects malformed or ill-typed metadata without exposing partial pages.
// Missing metadata is ordinary Markdown; a caller with a path supplies its title.
func ParsePage(data []byte) (*Page, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	page := &Page{RawContent: data}
	lines := strings.Split(content, "\n")
	if lines[0] != "---" {
		page.Body = strings.TrimSpace(content)
		return page, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("%w: missing closing delimiter", ErrInvalidFrontmatter)
	}
	fm := strings.Join(lines[1:end], "\n")
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(fm))
	if err := decoder.Decode(&document); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
	}
	// A frontmatter block is exactly one YAML document. Unmarshal otherwise
	// silently ignores content after an explicit YAML document terminator.
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing YAML content", ErrInvalidFrontmatter)
	}
	if len(document.Content) > 0 {
		node := document.Content[0]
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%w: expected mapping", ErrInvalidFrontmatter)
		}
		// Decoding a map rejects duplicate keys, including nested extension metadata.
		var checked map[string]interface{}
		if err := node.Decode(&checked); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
		}
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			if node.Content[i].Tag != "!!str" {
				return nil, fmt.Errorf("%w: invalid metadata key", ErrInvalidFrontmatter)
			}
			switch key {
			case "title", "category", "created", "updated":
				isDate := (key == "created" || key == "updated") && value.Tag == "!!timestamp"
				if value.Kind != yaml.ScalarNode || (value.Tag != "!!str" && !isDate) {
					return nil, fmt.Errorf("%w: %s must be a string", ErrInvalidFrontmatter, key)
				}
				switch key {
				case "title":
					page.Title = value.Value
				case "category":
					page.Category = value.Value
				case "created":
					page.Created = value.Value
				case "updated":
					page.Updated = value.Value
				}
			case "tags":
				if value.Kind != yaml.SequenceNode {
					return nil, fmt.Errorf("%w: tags must be a string list", ErrInvalidFrontmatter)
				}
				for _, tag := range value.Content {
					if tag.Kind != yaml.ScalarNode || tag.Tag != "!!str" {
						return nil, fmt.Errorf("%w: tags must contain strings", ErrInvalidFrontmatter)
					}
					page.Tags = append(page.Tags, tag.Value)
				}
			}
		}
	}
	page.Body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
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

	dir, err := os.OpenRoot(vp)
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.OpenFile("log.md", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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

	if err := ctx.Err(); err != nil {
		return err
	}
	return writeRootFile(vp, relPath, content, 0644)
}

func (w *FileWriter) UpdateIndex(ctx context.Context, vaultName string, entry IndexEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := relativePath(entry.Path); err != nil {
		return err
	}
	vp, err := w.vaultPath(vaultName)
	if err != nil {
		return err
	}
	existing, err := NewFileReader(w.personalPath, w.agentPath).ReadIndex(ctx, vaultName)
	if err != nil {
		return err
	}
	for _, item := range existing {
		if filepath.Clean(item.Path) == filepath.Clean(entry.Path) {
			return nil
		}
	}
	data, err := readRootFile(vp, "index.md")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	title := strings.NewReplacer("\r", " ", "\n", " ", "[", "", "]", "").Replace(entry.Title)
	data = append(data, []byte(fmt.Sprintf("\n- [%s](%s)\n", title, filepath.ToSlash(entry.Path)))...)
	return writeRootFile(vp, "index.md", data, 0644)
}

// --  helpers --

func statVault(root string, count *int, totalBytes *int64) error {
	return statVaultWithPolicy(root, "", ContentPolicy{}, count, totalBytes)
}

func statVaultWithPolicy(root, vaultName string, policy ContentPolicy, count *int, totalBytes *int64) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != root {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil || !policy.Allows(vaultName, rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
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
//  1. Pages whose frontmatter category matches entityTriggerCategories.
//  2. Pages whose frontmatter tags match entityTriggerTags.
//  3. Short titles (<=6 runes) that do not match wiki meta patterns.
//
// extraEntities provides a manual override list merged into the result.
// Duplicates are removed.
func ExtractTriggerEntities(personalPath string, extraEntities []string) ([]string, error) {
	return extractTriggerEntities(context.Background(), personalPath, extraEntities)
}

// TriggerEntities scans only the selected vault on every request. No startup
// snapshot or metadata-only cache can retain a removed or newly private entity.
func (r *FileReader) TriggerEntities(ctx context.Context, vaultName string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := r.vaultPath(vaultName)
	if err != nil {
		return nil, err
	}
	return extractTriggerEntitiesWithPolicy(ctx, root, vaultName, r.policy, nil)
}

func extractTriggerEntities(ctx context.Context, personalPath string, extraEntities []string) ([]string, error) {
	return extractTriggerEntitiesWithPolicy(ctx, personalPath, "", ContentPolicy{}, extraEntities)
}

func extractTriggerEntitiesWithPolicy(ctx context.Context, personalPath, vaultName string, policy ContentPolicy, extraEntities []string) ([]string, error) {
	seen := make(map[string]bool)
	for _, e := range extraEntities {
		e = strings.TrimSpace(e)
		if e != "" {
			seen[e] = true
		}
	}

	err := filepath.WalkDir(personalPath, func(path string, d os.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		if path != personalPath {
			rel, relErr := filepath.Rel(personalPath, path)
			if relErr != nil {
				return relErr
			}
			if !policy.Allows(vaultName, rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if path != personalPath && (strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")) {
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

		relPath, relErr := filepath.Rel(personalPath, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := readRootFile(personalPath, relPath)
		if readErr != nil {
			return readErr
		}

		page, parseErr := ParsePage(data)
		if errors.Is(parseErr, ErrInvalidFrontmatter) {
			return nil
		}
		if parseErr != nil {
			return parseErr
		}
		if IsInternalPage(page) {
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
	sort.Strings(entities)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return entities, nil
}
