package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCreateRecordingFileCreatesUniqueFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "Logs")
	first, err := createRecordingFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.WriteString("first\n"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := createRecordingFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	pattern := regexp.MustCompile(`^matchscope-\d{8}-\d{6}-\d+\.log$`)
	if !filepath.IsAbs(first.Name()) || filepath.Dir(first.Name()) != dir || first.Name() == second.Name() || !pattern.MatchString(filepath.Base(first.Name())) || !pattern.MatchString(filepath.Base(second.Name())) {
		t.Fatalf("录制文件路径或命名错误: %q %q", first.Name(), second.Name())
	}
	data, err := os.ReadFile(first.Name())
	if err != nil || string(data) != "first\n" {
		t.Fatalf("后续启动覆盖先前录制: %q %v", data, err)
	}
}

func TestRecordingDirectoryFailurePrecedesReadiness(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var stderr bytes.Buffer
	err = run("127.0.0.1:0", filepath.Join(blocker, "Logs"), writer, log.New(&stderr, "", 0))
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	ready, readErr := io.ReadAll(reader)
	if err == nil || !strings.Contains(err.Error(), blocker) || readErr != nil || len(ready) != 0 {
		t.Fatalf("录制目录错误时不应报告就绪: err=%v ready=%q readErr=%v", err, ready, readErr)
	}
	if _, err := createRecordingFile(""); err == nil {
		t.Fatal("空录制目录应被拒绝")
	}
}
