package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// LocalFolder scans a UNC / shared / local directory tree. Revision uses the
// (mtime,size) quick check for most files (doc/225 §8); content hashing is
// intentionally left to the downstream parse pipeline to avoid reading large
// payloads twice.
type LocalFolder struct{}

// Type implements Provider.
func (*LocalFolder) Type() string { return model.ConnectorProviderLocalFolder }

// errScanPageFull stops WalkDir as soon as the page budget is filled, so a
// page never materializes the whole tree (doc/225 §8 F5).
var errScanPageFull = errors.New("connector scan page full")

func specRoot(spec map[string]any) (string, error) {
	root, _ := spec["root"].(string)
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("root is required")
	}
	return filepath.Clean(root), nil
}

// ValidateSpec implements Provider.
func (*LocalFolder) ValidateSpec(spec map[string]any) error {
	_, err := specRoot(spec)
	return err
}

// Test implements Provider.
func (*LocalFolder) Test(ctx context.Context, spec map[string]any, _ map[string]string) error {
	root, err := specRoot(spec)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("local folder unreachable: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root is not a directory: %s", root)
	}
	return nil
}

func isHiddenOrTemp(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", ".git", "$recycle.bin", "system volume information",
		"thumbs.db", "desktop.ini", ".ds_store", "tmp", "temp", "tempdir":
		return true
	}
	return false
}

// List implements Provider. It walks the tree in WalkDir lexical order and
// streams at most ConnectorMaxBatch entries per call (bounded memory). The
// opaque token is the last delivered key; a rescan resumes after it. If the
// token vanished between pages the scan restarts from the top, which is safe
// because the ledger (not the cursor) owns idempotency.
func (p *LocalFolder) List(ctx context.Context, spec map[string]any, _ map[string]string, token string) ([]Entry, string, error) {
	root, err := specRoot(spec)
	if err != nil {
		return nil, "", err
	}
	entries, found, err := p.scanPage(ctx, root, token)
	if err != nil {
		return nil, "", err
	}
	if token != "" && !found {
		entries, _, err = p.scanPage(ctx, root, "")
		if err != nil {
			return nil, "", err
		}
	}
	next := ""
	if len(entries) >= model.ConnectorMaxBatch {
		// There may be more entries; the following call will confirm with an
		// empty page when the tree is exhausted.
		next = entries[len(entries)-1].Key
	}
	return entries, next, nil
}

func (p *LocalFolder) scanPage(ctx context.Context, root, after string) ([]Entry, bool, error) {
	entries := make([]Entry, 0, model.ConnectorMaxBatch)
	passed := after == ""
	found := after == ""
	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees; Test already verified root
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		key := filepath.ToSlash(rel)
		name := d.Name()
		if d.IsDir() {
			if isHiddenOrTemp(strings.SplitN(key, "/", 2)[0]) {
				return filepath.SkipDir
			}
			return nil
		}
		// Never follow symlinks: a link inside the root must not escape it.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if isHiddenOrTemp(name) {
			return nil
		}
		if !passed {
			if key == after {
				passed = true
				found = true
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		mtime := info.ModTime().UTC()
		entries = append(entries, Entry{
			Key:      key,
			Size:     info.Size(),
			MTime:    mtime,
			Revision: fmt.Sprintf("%d-%d", mtime.Unix(), info.Size()),
			Kind:     "file",
		})
		if len(entries) >= model.ConnectorMaxBatch {
			return errScanPageFull
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errScanPageFull) {
		return nil, false, walkErr
	}
	return entries, found, nil
}

// Download implements Provider: reads the file bytes under root.
func (*LocalFolder) Download(ctx context.Context, spec map[string]any, _ map[string]string, key string) ([]byte, error) {
	root, err := specRoot(spec)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	full := filepath.Join(root, filepath.FromSlash(key))
	rel, rerr := filepath.Rel(root, full)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("key escapes connector root")
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxObjectBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxObjectBytes {
		return nil, fmt.Errorf("object exceeds the 25 MiB connector limit")
	}
	return data, nil
}
