package du

import (
	"os"
	"path/filepath"
	"sort"
)

// Item is one top-level entry under a scanned path.
type Item struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Scan lists the direct children of root and reports the total size of each.
// Directories are summed recursively; symlinks/junctions are skipped to avoid
// cycles. It returns the biggest `limit` entries (0 means all).
func Scan(root string, limit int) ([]Item, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			items = append(items, Item{Name: e.Name(), Size: dirSize(filepath.Join(root, e.Name()))})
		} else if info, err := e.Info(); err == nil {
			items = append(items, Item{Name: e.Name(), Size: info.Size()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			// skip reparse points / symlinks to avoid infinite loops
			if d.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
