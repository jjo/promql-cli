package simple_storage

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const compressTestData = "# promql-cli: pinat=2020-01-01T00:00:00.000Z\nm{a=\"b\"} 1 1577836800000\n"

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func zstdBytes(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := zstd.NewWriter(&b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func readAll(t *testing.T, path string) (string, error) {
	t.Helper()
	r, err := OpenMaybeCompressed(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	return string(b), err
}

func TestOpenMaybeCompressedDetectsByContent(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content []byte
	}{
		{"plain", "x.prom", []byte(compressTestData)},
		{"gzip with .prom name", "x.prom", nil},
		{"zstd with .prom name", "x.prom", nil},
		{"plain content named .gz", "x.prom.gz", []byte(compressTestData)},
		{"plain content named .zst", "x.prom.zst", []byte(compressTestData)},
		{"gzip with .zst name", "x.prom.zst", nil},
		{"empty file", "x.prom", []byte{}},
	}
	tests[1].content = gzipBytes(t, compressTestData)
	tests[2].content = zstdBytes(t, compressTestData)
	tests[5].content = gzipBytes(t, compressTestData)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), tt.file)
			if err := os.WriteFile(p, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readAll(t, p)
			if err != nil {
				t.Fatal(err)
			}
			want := compressTestData
			if len(tt.content) == 0 {
				want = ""
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestOpenMaybeCompressedCorrupt(t *testing.T) {
	big := strings.Repeat(compressTestData, 2000)
	gz, zs := gzipBytes(t, big), zstdBytes(t, big)
	tests := []struct {
		name    string
		content []byte
	}{
		{"gzip magic only", gzip1(gz)},
		{"gzip truncated", gz[:len(gz)/2]},
		{"zstd magic only", zs[:4]},
		{"zstd truncated", zs[:len(zs)/2]},
		{"zstd garbage after magic", append([]byte{0x28, 0xb5, 0x2f, 0xfd}, []byte("not a frame at all")...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "bad.prom")
			if err := os.WriteFile(p, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readAll(t, p); err == nil {
				t.Fatal("expected an error for corrupt compressed data")
			}
		})
	}
}

func gzip1(b []byte) []byte { return b[:2] }

func TestCreateMaybeCompressedRoundTrip(t *testing.T) {
	tests := []struct {
		file  string
		magic []byte
	}{
		{"a.prom", []byte("# ")},
		{"a.prom.gz", []byte{0x1f, 0x8b}},
		{"a.prom.zst", []byte{0x28, 0xb5, 0x2f, 0xfd}},
		{"a.prom.zstd", []byte{0x28, 0xb5, 0x2f, 0xfd}},
		{"A.PROM.ZST", []byte{0x28, 0xb5, 0x2f, 0xfd}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), tt.file)
			w, err := CreateMaybeCompressed(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, compressTestData); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(p)
			if !bytes.HasPrefix(raw, tt.magic) {
				t.Errorf("file does not start with %x: %x", tt.magic, raw[:4])
			}
			got, err := readAll(t, p)
			if err != nil || got != compressTestData {
				t.Errorf("round trip = %q, %v", got, err)
			}
		})
	}
}

func TestCreateMaybeCompressedIsAtomic(t *testing.T) {
	for _, name := range []string{"snap.prom", "snap.prom.gz", "snap.prom.zst"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, name)
			if err := os.WriteFile(p, []byte("good\n"), 0o640); err != nil {
				t.Fatal(err)
			}

			// an aborted write leaves the previous file and no temporary file
			w, err := CreateMaybeCompressed(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("half a snapshot")); err != nil {
				t.Fatal(err)
			}
			w.Abort()
			if got, _ := os.ReadFile(p); string(got) != "good\n" {
				t.Fatalf("after Abort the file is %q, want the previous content", got)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("after Abort the directory has %d entries, want only %s", len(entries), name)
			}

			// a successful write replaces it, keeping its permissions
			w, err = CreateMaybeCompressed(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("m 1\n")); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			r, err := OpenMaybeCompressed(p)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(r)
			_ = r.Close()
			if err != nil || string(got) != "m 1\n" {
				t.Fatalf("read back %q (err %v), want %q", got, err, "m 1\n")
			}
			if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
				t.Fatalf("mode = %v, want the previous 0640", st.Mode().Perm())
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("after Close the directory has %d entries, want only %s", len(entries), name)
			}
		})
	}
}

func TestOpenMaybeCompressedShortAndMismatchedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		name, content string
	}{
		{"empty.prom", ""},
		{"one-byte.prom", "x"},
		{"gzip-magic-prefix-only.prom", "\x1f"},
		{"plain-named.prom.gz", "m 1\n"},
		{"plain-named.prom.zst", "m 1\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, tt.name)
			if err := os.WriteFile(p, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := OpenMaybeCompressed(p)
			if err != nil {
				t.Fatalf("OpenMaybeCompressed: %v", err)
			}
			defer func() { _ = r.Close() }()
			if got, err := io.ReadAll(r); err != nil || string(got) != tt.content {
				t.Fatalf("read %q (err %v), want the plain content %q", got, err, tt.content)
			}
		})
	}
}
