package vault

import (
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// relativePath rejects cross-platform traversal; os.Root also prevents symlink
// escapes at the actual filesystem operation, avoiding check/use races.
func relativePath(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.Contains(name, ":") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("vault: invalid relative path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("vault: path traversal rejected")
		}
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if !filepath.IsLocal(clean) || clean == "." {
		return "", fmt.Errorf("vault: invalid relative path %q", name)
	}
	return clean, nil
}

func readRootFile(root, name string) ([]byte, error) {
	name, err := relativePath(name)
	if err != nil {
		return nil, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadFile(name)
}

func writeRootFile(root, name string, data []byte, perm fs.FileMode) error {
	name, err := relativePath(name)
	if err != nil {
		return err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	if info, err := dir.Lstat(name); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("vault: refusing symlink destination")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), fmt.Sprintf(".write-%x", nonce))
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer dir.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return dir.Rename(tmp, name)
}

// A complete, content-hashed manifest detects additions and changes even when
// an editor preserves the mtime and file length.
func markdownFiles(root string, excluded map[string]bool) (map[string]string, error) {
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") || excluded[d.Name()] {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
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
