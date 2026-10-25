package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDoesNotCreateConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "crushrc")
	var out bytes.Buffer
	if err := run([]string{"--check", "--config", path}, &out); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("check wrote files")
	}
}

func TestVersionAndInvalidArguments(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil || !strings.Contains(out.String(), "crush-on-you") {
		t.Fatal(out.String(), err)
	}
	if err := run([]string{"unexpected"}, &out); err == nil {
		t.Fatal("ignored positional argument")
	}
}
