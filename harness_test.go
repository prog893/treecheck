package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree builds a fixture and returns its root. Entries are declared as a map so
// a test reads as the shape it is asserting about rather than a script.
type tree struct {
	// files maps a relative path to its contents.
	files map[string]string
	// sidecars maps a relative path to the literal bytes of its .sha256.
	// A path present here with an empty value gets an empty sidecar, which
	// is a distinct case from having none at all.
	sidecars map[string]string
	// seed writes a correct sidecar for each listed path.
	seed []string
	// mode applies a chmod after everything else, for the unreadable cases.
	mode map[string]os.FileMode
	dirs []string
}

func (tr tree) build(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range tr.dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, content := range tr.files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range tr.seed {
		content, ok := tr.files[p]
		if !ok {
			t.Fatalf("seed names %q, which is not in files", p)
		}
		if err := os.WriteFile(filepath.Join(root, p+hashExt),
			[]byte(sha256Hex(content)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range tr.sidecars {
		if err := os.WriteFile(filepath.Join(root, p+hashExt), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, m := range tr.mode {
		full := filepath.Join(root, p)
		if err := os.Chmod(full, m); err != nil {
			t.Fatal(err)
		}
		// t.TempDir cleanup cannot remove a directory it may not enter.
		t.Cleanup(func() { _ = os.Chmod(full, 0o755) })
	}
	return root
}

// result captures one run end to end, exactly as a caller would see it.
type result struct {
	code   int
	stdout string
	stderr string
}

func (r result) line(t *testing.T, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(l, prefix))
		}
	}
	t.Fatalf("no line starting %q in:\n%s", prefix, r.stdout)
	return ""
}

func (r result) counter(t *testing.T, name string) string {
	t.Helper()
	return r.line(t, name+":")
}

// runCLI drives the real entry point with real file descriptors, so what the
// test asserts on is the bytes a user would actually get. Pipes rather than
// a terminal, which also pins the non-interactive path.
func runCLI(t *testing.T, argv ...string) result {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outC := make(chan string, 1)
	errC := make(chan string, 1)
	go func() { b, _ := os.ReadFile("/dev/stdin"); _ = b }()
	go func() { outC <- readAll(outR) }()
	go func() { errC <- readAll(errR) }()

	code := run(argv, outW, errW)
	outW.Close()
	errW.Close()
	return result{code: code, stdout: <-outC, stderr: <-errC}
}

func readAll(f *os.File) string {
	defer f.Close()
	var sb strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			return sb.String()
		}
	}
}
