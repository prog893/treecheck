package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File is one entry the walk decided to check. Size comes from the walk's own
// stat rather than a separate pass.
//
// The shell implementation had to shell out to `du -k` for this, which reports
// allocated blocks while the hash covers logical contents. The two diverge in
// both directions: block rounding makes a one-byte file weigh a whole block,
// a sparse file's holes are hashed but never allocated, and a second path to
// an already-counted inode came back with no size at all. Reading the size
// here removes that entire class of divergence: this is the number of bytes
// the hash will actually read.
type File struct {
	Path string
	Rel  string
	Size int64
}

type walkResult struct {
	Files []File
	// Total counts every file the walk could have reached, so subtracting
	// the number checked reports what was passed over and why.
	Total int
	Bytes int64
	// Err is set when the traversal itself failed. A failed walk must never
	// be indistinguishable from an empty directory: reporting success over
	// whatever subset happened to be reachable is the exact failure this
	// tool exists to prevent.
	Err error
}

// walkTree enumerates files to check, in byte order.
//
// Hidden entries are pruned, not filtered after the fact. Descending into
// .Trashes, .DocumentRevisions-V100 and friends is what breaks on a real
// volume: they are unreadable, which turns an ordinary scan into a reported
// walk failure. The root itself is never pruned, however it is named, so
// `treecheck ~/.config` still walks.
func walkTree(root string, maxDepth int, exclude []string, hashExt string) walkResult {
	var res walkResult
	rootClean := filepath.Clean(root)

	excl := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		if e = strings.TrimSpace(e); e != "" {
			excl[e] = true
		}
	}

	var firstErr error
	err := filepath.WalkDir(rootClean, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// The root failing is fatal; anything deeper is a partial
			// traversal, which is still a failure but lets the rest be
			// reported. Either way the run cannot come back clean.
			if firstErr == nil {
				firstErr = err
			}
			if p == rootClean {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == rootClean {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") && len(name) > 1 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(rootClean, p)
		if rerr != nil {
			return nil
		}
		depth := len(strings.Split(rel, string(filepath.Separator)))
		if d.IsDir() {
			if excl[name] {
				return fs.SkipDir
			}
			if maxDepth > 0 && depth >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if maxDepth > 0 && depth > maxDepth {
			return nil
		}
		res.Total++
		if strings.HasSuffix(name, hashExt) {
			return nil
		}
		info, ierr := d.Info()
		var size int64
		if ierr == nil {
			size = info.Size()
		}
		res.Files = append(res.Files, File{Path: p, Rel: rel, Size: size})
		res.Bytes += size
		return nil
	})
	if err != nil && firstErr == nil {
		firstErr = err
	}
	res.Err = firstErr

	// Byte order, matching LC_ALL=C, so two runs over one tree can be diffed
	// against each other and both engines stay in step.
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	return res
}

// statSize is used by the checker when the walk could not stat an entry.
func statSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
