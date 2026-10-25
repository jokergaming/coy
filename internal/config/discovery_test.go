package config

import (
	"strings"
	"testing"
)

func TestImportModelsIsAtomicAndPreservesConfiguration(t *testing.T) {
	const source = "# keep\nprovider add mine --type openai-compat --base-url 'https://example.invalid/v1' --extra-body '{}'\nmodel add mine/existing --name 'Custom' --unknown 'keep'\n"
	d, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddModels("mine", map[string]string{"valid": "Valid", "z invalid": "Invalid"}); err == nil || d.Dirty() {
		t.Fatal("invalid discovery must not partially import")
	}
	count, err := d.AddModels("mine", map[string]string{"existing": "Replace", "vendor/new": "New", "vendor/unnamed": ""})
	if err != nil || count != 2 {
		t.Fatal("import failed", err)
	}
	out, err := d.Generate()
	if err != nil || !strings.HasPrefix(out, source) || !strings.Contains(out, "model add 'mine/vendor/new' --name 'New'") {
		t.Fatal("import changed existing configuration", err)
	}
	if !strings.Contains(out, "model add 'mine/vendor/unnamed' --name 'vendor/unnamed'") {
		t.Fatal("ID-only discovery must write an explicit model name")
	}
	if _, err := d.AddModels("openai", map[string]string{"new": "New"}); err == nil {
		t.Fatal("import must not manage built-in provider")
	}
}

func TestRequestValueResolvesOnlySimpleEnvironmentReferences(t *testing.T) {
	t.Setenv("COY_TEST_DISCOVERY_KEY", "fictional-key")
	for _, expression := range []string{`"${COY_TEST_DISCOVERY_KEY}"`, `"$COY_TEST_DISCOVERY_KEY"`, `$COY_TEST_DISCOVERY_KEY`, `${COY_TEST_DISCOVERY_KEY}`} {
		d, err := Parse("provider add mine --type openai-compat --base-url 'https://example.invalid' --api-key " + expression + "\n")
		if err != nil {
			t.Fatal(err)
		}
		e := d.Entries()[0]
		v, err := e.RequestValue("api-key")
		if err != nil || v != "fictional-key" || d.Dirty() {
			t.Fatal("simple reference not resolved safely", err)
		}
		if err := d.Put(e, "provider", "mine", map[string]string{"type": "openai-compat", "base-url": e.Value("base-url"), "api-key": "env:COY_TEST_DISCOVERY_KEY"}); err != nil {
			t.Fatal(err)
		}
		if e.Dynamic("api-key") {
			t.Fatal("edited expression should use literal form value")
		}
		if v, err := e.RequestValue("api-key"); err != nil || v != "fictional-key" {
			t.Fatal("env: form value not resolved", err)
		}
		t.Setenv("COY_TEST_DISCOVERY_KEY", "")
		if _, err := e.RequestValue("api-key"); err == nil {
			t.Fatal("unset environment variable must fail explicitly")
		}
		t.Setenv("COY_TEST_DISCOVERY_KEY", "fictional-key")
	}
	for _, expression := range []string{`"${COY_TEST_DISCOVERY_KEY:-fallback}"`, `"prefix${COY_TEST_DISCOVERY_KEY}"`, `"$(do-not-execute)"`} {
		d, err := Parse("provider add mine --type openai-compat --base-url 'https://example.invalid' --api-key " + expression + "\n")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Entries()[0].RequestValue("api-key"); err == nil {
			t.Fatal("complex shell expression should be rejected")
		}
	}
}
