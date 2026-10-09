package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ContentPolicy excludes operational directories before any RAG read or index.
// Its zero value preserves the original constructors' visibility behavior.
// Constructed policies also refuse symlinks below a Vault root: an operational
// file must not gain a public alias; every read checks the current path again.
type ContentPolicy struct {
	roots    map[string]string
	excluded map[string][]string
}

func NewContentPolicy(vaultRoots map[string]string, excludedAbsDirs []string) (ContentPolicy, error) {
	policy := ContentPolicy{roots: make(map[string]string), excluded: make(map[string][]string)}
	lexical := make(map[string]string)
	for name, root := range vaultRoots {
		if err := canonicalAbsolute(root); err != nil {
			return ContentPolicy{}, fmt.Errorf("vault policy: invalid root for %s: %w", name, err)
		}
		resolved, err := resolveDirectory(root)
		if err != nil {
			return ContentPolicy{}, fmt.Errorf("vault policy: resolve root for %s: %w", name, err)
		}
		lexical[name] = root
		policy.roots[name] = resolved
	}
	for _, dir := range excludedAbsDirs {
		if err := canonicalAbsolute(dir); err != nil {
			return ContentPolicy{}, fmt.Errorf("vault policy: invalid excluded directory: %w", err)
		}
		resolved, err := resolveDirectory(dir)
		if err != nil {
			return ContentPolicy{}, fmt.Errorf("vault policy: resolve excluded directory: %w", err)
		}
		for name, root := range policy.roots {
			if within(dir, lexical[name]) || within(resolved, root) {
				return ContentPolicy{}, fmt.Errorf("vault policy: operational directory contains vault %s", name)
			}
			if within(lexical[name], dir) && !within(root, resolved) {
				return ContentPolicy{}, fmt.Errorf("vault policy: operational directory escapes vault %s", name)
			}
			if within(root, resolved) {
				policy.excluded[name] = append(policy.excluded[name], resolved)
			}
		}
	}
	return policy, nil
}

func canonicalAbsolute(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("expected canonical absolute directory")
	}
	return nil
}

// Resolve existing parents too, since the first run may not have written the
// archive yet. Other errors must fail initialization instead of becoming public.
func resolveDirectory(path string) (string, error) {
	missing := []string{}
	current := path
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("expected directory")
			}
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, linkErr := os.Lstat(current); linkErr == nil {
			return "", fmt.Errorf("unresolvable directory link")
		} else if !os.IsNotExist(linkErr) {
			return "", linkErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func within(root, path string) bool {
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		path = strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func (p ContentPolicy) Allows(vaultName, relPath string) bool {
	if p.roots == nil {
		return true
	}
	root, ok := p.roots[vaultName]
	if !ok {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !within(root, resolvedRoot) || !within(resolvedRoot, root) {
		return false
	}
	normalized := strings.ReplaceAll(relPath, `\`, "/")
	rel, err := relativePath(normalized)
	if err != nil || filepath.ToSlash(rel) != normalized {
		return false
	}
	target := filepath.Join(root, rel)
	for _, excluded := range p.excluded[vaultName] {
		if within(excluded, target) {
			return false
		}
	}
	// Fail closed on links (including Windows junctions) or inaccessible sources.
	// A root alias was resolved during initialization; only below-root aliases
	// are refused. New plain files below missing parents remain allowed.
	current := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return true
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func (p ContentPolicy) markdownFiles(root, vaultName string) (map[string]string, error) {
	if p.roots == nil {
		return markdownFiles(root, systemFiles)
	}
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !p.Allows(vaultName, rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") || systemFiles[d.Name()] {
			return nil
		}
		data, err := readRootFile(root, rel)
		if err != nil {
			return err
		}
		files[rel] = hashContent(string(data))
		return nil
	})
	return files, err
}
