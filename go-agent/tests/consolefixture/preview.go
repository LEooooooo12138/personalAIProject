package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuanleyao/ai-agent/internal/gateway"
)

const previewMeta = `<meta name="console-preview" content="mock">`

// Copy built assets into the fixture's disposable root. Never mark the shared
// build: a production process may serve that same build directory.
func preparePreviewAssets(sourceRoot, targetRoot string) error {
	source, err := filepath.Abs(sourceRoot)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(targetRoot)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(source, target)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("preview target must be outside the source build")
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(source)) {
		return fmt.Errorf("preview source must not contain symlinks")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	foundIndex := false
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("preview assets must not contain symlinks")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("preview asset escapes build directory")
		}
		if rel == "." {
			return nil
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.Mkdir(destination, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("preview asset is not a regular file")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rel == "index.html" {
			if bytes.Count(content, []byte("</head>")) != 1 || bytes.Contains(content, []byte(`name="console-preview"`)) {
				return fmt.Errorf("preview requires an unmarked built HTML document")
			}
			content = bytes.Replace(content, []byte("</head>"), []byte(previewMeta+"\n</head>"), 1)
			foundIndex = true
		}
		return os.WriteFile(destination, content, 0600)
	})
	if err != nil {
		return err
	}
	if !foundIndex {
		return fmt.Errorf("preview index.html is missing")
	}
	return nil
}

// Called only by the standalone fixture process, after bootstrap fixes all data
// and dependency paths. Tests do not change their process-global directory.
func (f *fixture) enterPreviewDirectory() (func(), error) {
	original, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	preview := filepath.Join(f.dir, "preview")
	if err := preparePreviewAssets(filepath.Join(original, "static", "console"), filepath.Join(preview, "static", "console")); err != nil {
		return nil, err
	}
	if err := os.Chdir(preview); err != nil {
		return nil, err
	}
	// Static routes capture their absolute build path at construction time.
	// Recreate the fixture-only gateway after entering the private build root.
	f.server = gateway.NewServerFromApp(f.app)
	return func() { _ = os.Chdir(original) }, nil
}
