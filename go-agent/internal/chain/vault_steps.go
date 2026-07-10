package chain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/vault"
)

// ── Vault Write Step ──

// VaultWriteStep writes a wiki page to the vault filesystem.
// It parses the LLM-generated output to extract the title, determines
// the correct category directory, and writes the markdown file.
type VaultWriteStep struct {
	writer vault.Writer
	logger *zap.Logger
}

// NewVaultWriteStep creates a step that writes wiki pages to the vault.
func NewVaultWriteStep(writer vault.Writer, logger *zap.Logger) *VaultWriteStep {
	return &VaultWriteStep{writer: writer, logger: logger}
}

func (s *VaultWriteStep) Name() string { return "vault-write" }

func (s *VaultWriteStep) Run(ctx context.Context, state *ChainState) error {
	vaultName := state.Vault
	if vaultName == "" {
		vaultName = "personal"
	}

	wikiOutput, _ := state.GetString("wiki_output")
	if wikiOutput == "" {
		state.FinalAnswer = "no content to write"
		return nil
	}

	// Parse the LLM output to extract title and determine category.
	title := extractTitleFromFrontmatter(wikiOutput)
	if title == "" {
		title = extractTitleFromHeadings(wikiOutput)
	}
	if title == "" {
		title = "untitled-" + time.Now().Format("20060102-150405")
	}

	category := inferCategory(wikiOutput, title)
	slug := slugifyTitle(title)
	relPath := filepath.Join(category, slug+".md")

	// Write the page.
	if err := s.writer.WritePage(ctx, vaultName, relPath, []byte(wikiOutput)); err != nil {
		return fmt.Errorf("write page: %w", err)
	}

	state.Data["written_path"] = relPath
	state.Data["written_title"] = title
	state.Data["written_category"] = category

	s.logger.Info("wiki page written",
		zap.String("vault", vaultName),
		zap.String("path", relPath),
		zap.String("title", title),
	)

	return nil
}

// ── Index Update Step ──

// IndexUpdateStep updates index.md with an entry for a newly written page.
type IndexUpdateStep struct {
	writer     vault.Writer
	reader     vault.Reader
	personalPath string
	agentPath    string
	logger     *zap.Logger
}

// NewIndexUpdateStep creates a step that updates the vault index.
func NewIndexUpdateStep(writer vault.Writer, reader vault.Reader, personalPath, agentPath string, logger *zap.Logger) *IndexUpdateStep {
	return &IndexUpdateStep{
		writer:       writer,
		reader:       reader,
		personalPath: personalPath,
		agentPath:    agentPath,
		logger:       logger,
	}
}

func (s *IndexUpdateStep) Name() string { return "index-update" }

func (s *IndexUpdateStep) Run(ctx context.Context, state *ChainState) error {
	vaultName := state.Vault
	if vaultName == "" {
		vaultName = "personal"
	}

	title, _ := state.GetString("written_title")
	path, _ := state.GetString("written_path")
	if title == "" || path == "" {
		s.logger.Debug("index-update: nothing to index")
		return nil
	}

	// Read existing index to check for duplicates.
	existing, err := s.reader.ReadIndex(ctx, vaultName)
	if err != nil {
		s.logger.Warn("read index failed", zap.Error(err))
		// Continue anyway — we'll just append.
	}

	for _, entry := range existing {
		if entry.Path == path || entry.Title == title {
			s.logger.Debug("index-update: entry already exists, skipping",
				zap.String("title", title),
			)
			return nil
		}
	}

	// Append to index.md.
	entry := fmt.Sprintf("[%s](%s)", title, path)
	if err := s.writer.AppendLog(ctx, vaultName, "- "+entry); err != nil {
		// AppendLog writes to log.md, not index.md. We need a different approach.
		// Let's append directly to index.md for now.
		vp := s.vaultPath(vaultName)
		indexPath := filepath.Join(vp, "index.md")
		f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open index.md: %w", err)
		}
		defer f.Close()
		if _, err := fmt.Fprintf(f, "\n- [%s](%s)", title, path); err != nil {
			return fmt.Errorf("write index.md: %w", err)
		}
	}

	s.logger.Info("index updated",
		zap.String("vault", vaultName),
		zap.String("title", title),
		zap.String("path", path),
	)

	return nil
}

func (s *IndexUpdateStep) vaultPath(name string) string {
	switch name {
	case "personal":
		return s.personalPath
	case "agent":
		return s.agentPath
	default:
		return s.personalPath
	}
}

// ── Helpers for wiki output parsing ──

// extractTitleFromFrontmatter extracts "title" from YAML frontmatter.
func extractTitleFromFrontmatter(content string) string {
	// Match: title: "Some Title" or title: Some Title
	re := regexp.MustCompile(`(?m)^title:\s*["']?(.+?)["']?\s*$`)
	if matches := re.FindStringSubmatch(content); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// extractTitleFromHeadings extracts the first H1 or H2 heading as title.
func extractTitleFromHeadings(content string) string {
	// Try "## Title" first, then "# Title".
	for _, prefix := range []string{"## ", "# "} {
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			}
		}
	}
	return ""
}

// inferCategory guesses the vault category from content or title keywords.
func inferCategory(content, title string) string {
	lower := strings.ToLower(content + " " + title)

	// Check frontmatter category first.
	re := regexp.MustCompile(`(?m)^category:\s*["']?(.+?)["']?\s*$`)
	if matches := re.FindStringSubmatch(content); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Keyword-based inference.
	switch {
	case strings.Contains(lower, "skill") || strings.Contains(lower, "技能"):
		return "skills"
	case strings.Contains(lower, "reference") || strings.Contains(lower, "参考") || strings.Contains(lower, "ref"):
		return "references"
	case strings.Contains(lower, "project") || strings.Contains(lower, "项目"):
		return "projects"
	case strings.Contains(lower, "journal") || strings.Contains(lower, "日记") || strings.Contains(lower, "日志"):
		return "journal"
	case strings.Contains(lower, "synthesis") || strings.Contains(lower, "合成") || strings.Contains(lower, "综合"):
		return "synthesis"
	default:
		return "concepts"
	}
}

// slugifyTitle converts a title to a filesystem-safe slug.
func slugifyTitle(title string) string {
	// Keep CJK characters, ASCII letters, digits, hyphens.
	var result strings.Builder
	for _, r := range strings.ToLower(title) {
		if isSlugChar(r) {
			result.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			result.WriteRune('-')
		}
	}
	slug := result.String()
	// Collapse consecutive hyphens.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "page"
	}
	return slug
}

func isSlugChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
		(r >= 0x4E00 && r <= 0x9FFF) // CJK Unified
}
