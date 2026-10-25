package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = "# keep this comment\nprovider add my-api --type openai-compat --base-url 'https://example.com/v1' --api-key \"$MY_KEY\" --extra-header X-Private secret-header\nmodel add my-api/vendor/model --name 'Original' --context-window 32000\n# unrelated\nmcp add docs --command server\nprovider add openai --api-key 'built-in-secret'\n"

func mustParse(t *testing.T, s string) *Document {
	t.Helper()
	d, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func fields(e *Entry) map[string]string {
	v := map[string]string{}
	for _, f := range Fields(e.Kind) {
		v[f.Name] = e.Value(f.Name)
	}
	return v
}

func addProvider(t *testing.T, d *Document, id string) {
	t.Helper()
	if err := d.Put(nil, "provider", id, map[string]string{"type": "openai-compat", "base-url": "https://example.com/v1", "api-key": "env:MY_KEY"}); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTripAndSurgicalEdit(t *testing.T) {
	d := mustParse(t, sample)
	if d.ReadOnly != "" {
		t.Fatal(d.ReadOnly)
	}
	if len(d.Entries()) != 2 {
		t.Fatalf("expected 2 custom entries: %d", len(d.Entries()))
	}
	s, err := d.Generate()
	if err != nil || s != sample {
		t.Fatalf("roundtrip changed source: %v", err)
	}
	e := d.Entries()[1]
	v := fields(e)
	v["name"] = "Edited"
	if err = d.Put(e, e.Kind, e.ID, v); err != nil {
		t.Fatal(err)
	}
	s, err = d.Generate()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sample, "'Original'", "'Edited'", 1)
	if s != want {
		t.Fatalf("unrelated content changed\ngot: %s\nwant: %s", s, want)
	}
}

func TestQuotedValuesCannotExecute(t *testing.T) {
	d := mustParse(t, "")
	addProvider(t, d, "custom")
	e := d.Entries()[0]
	v := fields(e)
	v["name"] = "It's $(touch /tmp/coy-never-execute)"
	v["api-key"] = "$(echo literal-secret)"
	if err := d.Put(e, e.Kind, e.ID, v); err != nil {
		t.Fatal(err)
	}
	s, err := d.Generate()
	if err != nil {
		t.Fatal(err)
	}
	loaded := mustParse(t, s)
	if loaded.ReadOnly != "" {
		t.Fatal("literal data became executable shell")
	}
	if loaded.Entries()[0].Value("name") != v["name"] {
		t.Fatalf("quote roundtrip: %q", s)
	}
	if strings.Contains(d.Preview(), "literal-secret") || strings.Contains(d.Preview(), "touch") {
		t.Fatal("preview leaked field values")
	}
}

func TestDynamicConfigurationsAreReadOnly(t *testing.T) {
	for _, src := range []string{
		"source other.sh\n", "if true; then provider add foo; fi\n", "provider add \"$ID\" --type openai\n",
		"provider add foo --api-key \"$(echo secret)\"\n", "provider add foo; provider add foo\n",
		"provider remove foo\n", "provider add foo --name=a\n", "provider add foo > output\n",
	} {
		t.Run(src, func(t *testing.T) {
			d := mustParse(t, src)
			if d.ReadOnly == "" {
				t.Fatal("expected read-only")
			}
			if err := d.Put(nil, "provider", "new", map[string]string{"type": "openai", "base-url": "https://example.com"}); err == nil {
				t.Fatal("unsafe mutation allowed")
			}
			out, err := d.Generate()
			if err != nil || out != src {
				t.Fatal("source not preserved")
			}
		})
	}
}

func TestDeleteDependenciesAndSemicolons(t *testing.T) {
	d := mustParse(t, sample)
	if err := d.Delete(d.Entries()[0]); err == nil {
		t.Fatal("provider with models deleted")
	}
	if err := d.Delete(d.Entries()[1]); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(d.Entries()[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Generate(); err != nil {
		t.Fatal(err)
	}
	d = mustParse(t, "provider add mine --type openai --base-url https://example.com; option debug false\n")
	if err := d.Delete(d.Entries()[0]); err != nil {
		t.Fatal(err)
	}
	s, err := d.Generate()
	if err != nil || strings.Contains(s, "provider add") || !strings.Contains(s, "option debug false") {
		t.Fatalf("bad semicolon delete: %q %v", s, err)
	}
	d = mustParse(t, sample+"model large my-api/vendor/model\n")
	if err := d.Delete(d.Entries()[1]); err == nil {
		t.Fatal("selected model deleted")
	}
}

func TestValidation(t *testing.T) {
	d := mustParse(t, "")
	for _, id := range []string{"openai", "hyper", "bad id", "$(evil)"} {
		if err := d.Put(nil, "provider", id, map[string]string{"type": "openai", "base-url": "https://example.com"}); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	addProvider(t, d, "mine")
	if err := d.Put(nil, "model", "missing/foo", nil); err == nil {
		t.Fatal("orphan model accepted")
	}
	for _, v := range []string{"NaN", "+Inf", "-1"} {
		if err := d.Put(nil, "model", "mine/foo", map[string]string{"price-input": v}); err == nil {
			t.Fatalf("accepted price %s", v)
		}
	}
}

func TestModelNameIsAlwaysWritten(t *testing.T) {
	for _, tc := range []struct {
		label, source, name, want string
	}{
		{label: "new without name", want: "google/gemma-3-4b-it"},
		{label: "custom name", name: "Gemma's $(literal)", want: "Gemma's $(literal)"},
		{label: "edit unnamed", source: "model add mine/google/gemma-3-4b-it --unknown keep\n", want: "google/gemma-3-4b-it"},
		{label: "clear name", source: "model add mine/google/gemma-3-4b-it --name 'Old'\n", want: "google/gemma-3-4b-it"},
		{label: "preserve expression", source: "model add mine/google/gemma-3-4b-it --name \"$MODEL_NAME\"\n", name: `"$MODEL_NAME"`, want: `"$MODEL_NAME"`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			d := mustParse(t, "provider add mine --type openai-compat --base-url https://example.invalid\n"+tc.source)
			var e *Entry
			if tc.source != "" {
				e = d.Entries()[1]
			}
			input := map[string]string{"name": tc.name}
			if err := d.Put(e, "model", "mine/google/gemma-3-4b-it", input); err != nil {
				t.Fatal(err)
			}
			if input["name"] != tc.name {
				t.Fatal("Put mutated caller fields")
			}
			out, err := d.Generate()
			if err != nil {
				t.Fatal(err)
			}
			loaded := mustParse(t, out)
			if loaded.ReadOnly != "" || loaded.Entries()[1].Value("name") != tc.want || !strings.Contains(out, "--name ") {
				t.Fatalf("model name missing or unsafe: %q", out)
			}
			if strings.Contains(tc.source, "--unknown keep") && !strings.Contains(out, "--unknown keep") {
				t.Fatal("unrelated model option changed")
			}
		})
	}
}

func TestSaveBackupDryRunAndConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crushrc")
	if err := os.WriteFile(path, []byte(sample), 0640); err != nil {
		t.Fatal(err)
	}
	s, d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	e := d.Entries()[1]
	v := fields(e)
	v["name"] = "Changed"
	if err = d.Put(e, e.Kind, e.ID, v); err != nil {
		t.Fatal(err)
	}
	r, err := s.Save(d, true)
	if err != nil || !r.DryRun || !r.Changed {
		t.Fatalf("dry run: %+v %v", r, err)
	}
	names, _ := os.ReadDir(dir)
	if len(names) != 1 {
		t.Fatal("dry run created files")
	}
	data, _ := os.ReadFile(path)
	if string(data) != sample {
		t.Fatal("dry run wrote config")
	}
	r, err = s.Save(d, false)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(r.Backup)
	if err != nil || string(backup) != sample {
		t.Fatal("backup mismatch", err)
	}
	info, _ := os.Stat(r.Backup)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("backup exposes secrets")
	}
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("file mode changed")
	}
	d = r.Document
	if d.Dirty() {
		t.Fatal("save did not reset draft")
	}
	names, _ = os.ReadDir(dir)
	count := len(names)
	r, err = s.Save(d, false)
	if err != nil || r.Changed {
		t.Fatal("no-op save should not write")
	}
	names, _ = os.ReadDir(dir)
	if len(names) != count {
		t.Fatal("no-op created backup")
	}
	e = d.Entries()[1]
	v = fields(e)
	v["name"] = "Again"
	if err = d.Put(e, e.Kind, e.ID, v); err != nil {
		t.Fatal(err)
	}
	r, err = s.Save(d, false)
	if err != nil {
		t.Fatal(err)
	}
	d = r.Document
	names, _ = os.ReadDir(dir)
	if len(names) != count+1 {
		t.Fatal("second save did not keep separate backup")
	}
	if err = os.WriteFile(path, []byte("# external edit\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(d, false); err == nil {
		t.Fatal("external edit not detected")
	}
}

func TestNewFileDryRunCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "crushrc")
	s, d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	addProvider(t, d, "mine")
	generated, err := d.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(d, true); err != nil {
		t.Fatal(err)
	}
	names, _ := os.ReadDir(dir)
	if len(names) != 0 {
		t.Fatal("dry run created directories")
	}
	r, err := s.Save(d, false)
	if err != nil || !r.Created || r.Backup != "" {
		t.Fatalf("create: %+v %v", r, err)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != generated {
		t.Fatal("saved content differs from dry-run generation")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("new config not private")
	}
}

func TestLockAndSymlinkSafety(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crushrc")
	if err := os.WriteFile(path, []byte(sample), 0600); err != nil {
		t.Fatal(err)
	}
	s, d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	addProvider(t, d, "other")
	if err = os.WriteFile(path+".coy.lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(d, false); err == nil {
		t.Fatal("concurrent save allowed")
	}
	data, _ := os.ReadFile(path)
	if string(data) != sample {
		t.Fatal("failed save altered config")
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(path, link); err != nil {
		t.Skip(err)
	}
	if _, _, err = Load(link); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestGlobalPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CRUSH_GLOBAL_CONFIG", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	p, err := GlobalPath()
	if err != nil || p != filepath.Join(dir, "crushrc") {
		t.Fatal(p, err)
	}
	t.Setenv("CRUSH_GLOBAL_CONFIG", "")
	p, err = GlobalPath()
	if err != nil || p != filepath.Join(dir, "xdg", "crush", "crushrc") {
		t.Fatal(p, err)
	}
}

func TestBackupFailurePreventsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crushrc")
	if err := os.WriteFile(path, []byte(sample), 0600); err != nil {
		t.Fatal(err)
	}
	s, d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	addProvider(t, d, "another")
	calls := 0
	s.writeTemp = func(_ string, pattern string, data []byte, _ fs.FileMode) (string, error) {
		calls++
		if pattern != "crushrc.backup-*" || string(data) != sample {
			t.Fatal("must back up the original before any new content is written")
		}
		return "", errors.New("simulated disk full")
	}
	if _, err = s.Save(d, false); err == nil || !strings.Contains(err.Error(), "备份失败") {
		t.Fatal("backup error not reported", err)
	}
	if calls != 1 {
		t.Fatal("save continued after failed backup")
	}
	data, _ := os.ReadFile(path)
	if string(data) != sample || !d.Dirty() {
		t.Fatal("failed backup lost original or edits")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("failed save left temporary files or lock")
	}
}
