package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// sources holds the package's Go files. provenance.json records the digest of
// the non-test ones as the generator version.
//
//go:embed *.go
var sources embed.FS

// The fixed directories, relative to the module root.
const (
	artDir    = "tools/artgen/art"
	sampleDir = "tools/artgen/sample"
)

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/artgen (no arguments; run it from the module root)")
		os.Exit(2)
	}
	if err := run(artDir, sampleDir, os.Stdout); err != nil {
		var list errorList
		if errors.As(err, &list) {
			for _, e := range list {
				fmt.Fprintln(os.Stderr, e)
			}
			fmt.Fprintf(os.Stderr, "artgen: %d errors; nothing written\n", len(list))
		} else {
			fmt.Fprintln(os.Stderr, "artgen:", err)
		}
		os.Exit(1)
	}
}

// run generates the sample from artDir in memory and, only if that succeeds,
// writes it to outDir and removes every file and empty directory under outDir
// that the new sample does not contain.
func run(artDir, outDir string, stdout io.Writer) error {
	if fi, err := os.Stat(artDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s not found; run from the module root", artDir)
	}
	files, err := generate(os.DirFS(artDir))
	if err != nil {
		return err
	}
	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[f.path] = true
		dst := filepath.Join(outDir, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, f.data, 0o644); err != nil {
			return err
		}
	}
	if err := removeStale(outDir, keep); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "artgen: wrote %d files to %s\n", len(files), outDir)
	return nil
}

// removeStale removes the regular files under outDir whose slash-separated
// relative paths are not in keep, then the directories below outDir that are
// left empty. It never removes outDir itself.
func removeStale(outDir string, keep map[string]bool) error {
	var dirs []string
	err := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == outDir {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			return err
		}
		if keep[filepath.ToSlash(rel)] {
			return nil
		}
		return os.Remove(p)
	})
	if err != nil {
		return err
	}
	// WalkDir lists a directory before its contents, so the reverse order
	// visits every directory after its subdirectories.
	for _, d := range slices.Backward(dirs) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			if err := os.Remove(d); err != nil {
				return err
			}
		}
	}
	return nil
}
