package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveRepairsAllExistingModelNames(t *testing.T) {
	const source = "# preserved\n" +
		"provider add mine --type openai-compat --base-url https://example.invalid\n" +
		"model add mine/vendor/old --unknown keep # comment\n" +
		"model add mine/empty --name ''\n" +
		"model add mine/named --name 'My name'\n" +
		"model add mine/dynamic --name \"$MODEL_NAME\"\n" +
		"model add openai/override\n" +
		"model add other/old\n" +
		"model large mine/vendor/old\n"
	want := strings.NewReplacer(
		"--unknown keep #", "--unknown keep --name 'vendor/old' #",
		"--name ''", "--name 'empty'",
		"model add openai/override\n", "model add openai/override --name 'override'\n",
		"model add other/old\n", "model add other/old --name 'old'\n",
	).Replace(source)
	for _, addNew := range []bool{false, true} {
		t.Run(map[bool]string{false: "no edits", true: "add new model"}[addNew], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "crushrc")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			s, d, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := want
			if addNew {
				if err := d.Put(nil, "model", "mine/new", nil); err != nil {
					t.Fatal(err)
				}
				expected += "model add 'mine/new' --name 'new'\n"
			}
			before, err := d.Generate()
			if err != nil {
				t.Fatal(err)
			}
			preview, err := s.Save(d, true)
			if err != nil || !preview.Changed || !preview.DryRun || !strings.Contains(preview.Preview, "~ model mine/vendor/old\n    --name:") {
				t.Fatal("dry-run omitted repairs to existing models", err, preview.Preview)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != source {
				t.Fatal("dry-run changed configuration", err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 {
				t.Fatal("dry-run created files", err)
			}
			after, err := d.Generate()
			if err != nil || after != before {
				t.Fatal("preview mutated the editing draft", err)
			}
			result, err := s.Save(d, false)
			if err != nil || !result.Changed || result.Preview != preview.Preview {
				t.Fatal("save differs from dry-run", err)
			}
			data, err = os.ReadFile(path)
			if err != nil || string(data) != expected {
				t.Fatalf("existing names not repaired or unrelated text changed: %v\n%s", err, data)
			}
			backup, err := os.ReadFile(result.Backup)
			if err != nil || string(backup) != source {
				t.Fatal("backup must contain the unchanged original", err)
			}
			if result.Document.Dirty() {
				t.Fatal("saved document should be clean")
			}
			again, err := s.Save(result.Document, false)
			if err != nil || again.Changed || again.Backup != "" {
				t.Fatal("name repair must be idempotent", err)
			}
			files, err = os.ReadDir(dir)
			if err != nil || len(files) != 2 {
				t.Fatal("repeated save created redundant backups", err)
			}
		})
	}
}

func TestModelNameRepairFailurePreservesDraftAndOriginal(t *testing.T) {
	const source = "provider add mine --type openai-compat --base-url https://example.invalid\nmodel add mine/old\n"
	for _, failure := range []string{"backup", "conflict"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "crushrc")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			s, d, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := source
			if failure == "backup" {
				s.writeTemp = func(string, string, []byte, fs.FileMode) (string, error) {
					return "", errors.New("simulated backup failure")
				}
			} else {
				expected += "# external modification\n"
				if err := os.WriteFile(path, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Save(d, false); err == nil {
				t.Fatal("repair bypassed save failure")
			}
			out, err := d.Generate()
			if err != nil || out != source || d.Dirty() {
				t.Fatal("failed repair mutated the draft", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != expected {
				t.Fatal("failed repair changed the original", err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 {
				t.Fatal("failed repair left files behind", err)
			}
		})
	}
}

func TestModelNameRepairPreservesReadOnlyConfiguration(t *testing.T) {
	const source = "model add mine/old\nsource other.sh\n"
	path := filepath.Join(t.TempDir(), "crushrc")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s, d, err := Load(path)
	if err != nil || d.ReadOnly == "" {
		t.Fatal("expected read-only configuration", err)
	}
	for _, dryRun := range []bool{true, false} {
		result, err := s.Save(d, dryRun)
		if err != nil || result.Changed || result.Backup != "" {
			t.Fatal("unsafe configuration must remain unchanged", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != source {
		t.Fatal("read-only source was changed", err)
	}
}
