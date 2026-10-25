package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupRoundTripAndRemoval(t *testing.T) {
	d := mustParse(t, sample)
	e := d.Entries()[1]
	name := `常用 "模型" $(literal) \`
	if err := d.GroupModels([]*Entry{e}, name); err != nil {
		t.Fatal(err)
	}
	out, err := d.Generate()
	if err != nil || !strings.HasPrefix(out, sample) || !d.Dirty() {
		t.Fatal("group changed unrelated source or was not generated", err)
	}
	if strings.Contains(d.Preview(), name) || !strings.Contains(d.Preview(), "分组") {
		t.Fatal("group preview leaked a free-text value or omitted the change")
	}
	loaded := mustParse(t, out)
	if loaded.ReadOnly != "" || loaded.Entries()[1].Group() != name || loaded.Dirty() {
		t.Fatal("group did not roundtrip", loaded.ReadOnly)
	}
	if err := loaded.GroupModels([]*Entry{loaded.Entries()[1]}, "更新"); err != nil {
		t.Fatal(err)
	}
	updated, err := loaded.Generate()
	if err != nil || strings.Count(updated, "# coy:model-group ") != 1 {
		t.Fatal("group update duplicated metadata", err)
	}
	if mustParse(t, updated).Entries()[1].Group() != "更新" {
		t.Fatal("group update not persisted")
	}
	if err := loaded.GroupModels([]*Entry{loaded.Entries()[1]}, ""); err != nil {
		t.Fatal(err)
	}
	removed, err := loaded.Generate()
	if err != nil || strings.Contains(removed, "# coy:model-group ") || !strings.HasPrefix(removed, sample) {
		t.Fatal("ungroup damaged source", err)
	}
	loaded = mustParse(t, out)
	if err := loaded.Delete(loaded.Entries()[1]); err != nil {
		t.Fatal(err)
	}
	deleted, err := loaded.Generate()
	if err != nil || strings.Contains(deleted, "# coy:model-group ") || strings.Contains(deleted, "model add my-api/") || !strings.Contains(deleted, "# unrelated") {
		t.Fatal("deletion left group metadata or removed unrelated text", err)
	}
}

func TestFreshGroupAndInvalidBatchAreAtomic(t *testing.T) {
	d := mustParse(t, "")
	addProvider(t, d, "mine")
	if err := d.Put(nil, "model", "mine/new", nil); err != nil {
		t.Fatal(err)
	}
	e := d.Entries()[1]
	foreign := mustParse(t, sample).Entries()[1]
	for _, batch := range [][]*Entry{{e, foreign}, {e, d.Entries()[0]}, {e, nil}} {
		if err := d.GroupModels(batch, "常用"); err == nil || e.Group() != "" {
			t.Fatal("invalid batch partially grouped")
		}
	}
	for _, name := range []string{" bad", "bad\nname", "bad\x00", strings.Repeat("模", 81)} {
		if err := d.GroupModels([]*Entry{e}, name); err == nil || e.Group() != "" {
			t.Fatal("invalid group name accepted")
		}
	}
	if err := d.GroupModels([]*Entry{e}, "常用"); err != nil {
		t.Fatal(err)
	}
	out, err := d.Generate()
	if err != nil || mustParse(t, out).Entries()[1].Group() != "常用" {
		t.Fatal("fresh model group not reloaded", err)
	}
}

func TestBatchDeleteReferenceFailureLeavesAllModels(t *testing.T) {
	d := mustParse(t, sample+"model add my-api/another\nmodel small my-api/another\n")
	before, err := d.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteEntries([]*Entry{d.Entries()[1], d.Entries()[2]}); err == nil {
		t.Fatal("referenced model deleted")
	}
	after, err := d.Generate()
	if err != nil || after != before || d.Dirty() {
		t.Fatal("failed delete partially changed the draft", err)
	}
	if err := d.DeleteEntries([]*Entry{nil}); err == nil {
		t.Fatal("nil deletion accepted")
	}
}

func TestGroupMetadataSafety(t *testing.T) {
	for _, metadata := range []string{
		"# coy:model-group my-api/vendor/model unquoted\n",
		"# coy:model-group my-api/vendor/model \"bad\\nname\"\n",
		"# coy:model-group my-api/vendor/model \"a\"\n# coy:model-group my-api/vendor/model \"b\"\n",
		"# coy:model-group my-api/missing \"a\"\n",
		"option debug false # coy:model-group my-api/vendor/model \"a\"\n",
	} {
		d := mustParse(t, sample+metadata)
		if d.ReadOnly == "" {
			t.Fatal("invalid metadata accepted")
		}
		if err := d.GroupModels([]*Entry{d.Entries()[1]}, "常用"); err == nil {
			t.Fatal("read-only metadata modified")
		}
		out, err := d.Generate()
		if err != nil || out != sample+metadata {
			t.Fatal("invalid metadata was not preserved", err)
		}
	}
	d := mustParse(t, sample+"option attribution '\n# coy:model-group my-api/vendor/model \"not-a-comment\"\n'\n")
	if d.Entries()[1].Group() != "" {
		t.Fatal("quoted string interpreted as metadata")
	}
	d = mustParse(t, sample+"model add openai/gpt-test\n# coy:model-group openai/gpt-test \"built-in\"\n")
	if err := d.GroupModels([]*Entry{d.entries[len(d.entries)-1]}, "changed"); err == nil {
		t.Fatal("built-in model group modified")
	}
	out, err := d.Generate()
	if err != nil || out != d.source {
		t.Fatal("built-in metadata changed", err)
	}
}

func TestGroupSaveDryRunBackupAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crushrc")
	if err := os.WriteFile(path, []byte(sample), 0600); err != nil {
		t.Fatal(err)
	}
	s, d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.GroupModels([]*Entry{d.Entries()[1]}, "常用"); err != nil {
		t.Fatal(err)
	}
	want, err := d.Generate()
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Save(d, true)
	if err != nil || !r.Changed || !r.DryRun {
		t.Fatal("group dry-run failed", err)
	}
	names, err := os.ReadDir(dir)
	if err != nil || len(names) != 1 {
		t.Fatal("dry-run created files", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != sample {
		t.Fatal("dry-run modified original", err)
	}
	r, err = s.Save(d, false)
	if err != nil || r.Backup == "" || r.Document.Entries()[1].Group() != "常用" {
		t.Fatal("save did not retain groups", err)
	}
	backup, err := os.ReadFile(r.Backup)
	if err != nil || string(backup) != sample {
		t.Fatal("backup differs from original", err)
	}
	content, err = os.ReadFile(path)
	if err != nil || string(content) != want {
		t.Fatal("save differs from dry-run generation", err)
	}
	_, loaded, err := Load(path)
	if err != nil || loaded.Entries()[1].Group() != "常用" || loaded.Dirty() {
		t.Fatal("saved group not reloaded", err)
	}
}
