package access

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func writeProfile(t *testing.T, configDir, name, content string) {
	t.Helper()
	dir := filepath.Join(configDir, "access")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const validProfile = `{
  "schema_version": 1,
  "destinations": [
    {"alias": "nas", "host": "nas.home.lan", "port": 22, "user": "claude",
     "host_keys": ["nas.home.lan ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"]}
  ]
}`

func TestLoadValid(t *testing.T) {
	configDir := t.TempDir()
	writeProfile(t, configDir, "homelab", validProfile)
	p, err := Load(configDir, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	if p.SchemaVersion != 1 || len(p.Destinations) != 1 {
		t.Fatalf("profile = %+v", p)
	}
	d := p.Destinations[0]
	if d.Alias != "nas" || d.Host != "nas.home.lan" || d.Port != 22 || d.User != "claude" {
		t.Errorf("destination = %+v", d)
	}
	wantRules := []string{"allow@nas.home.lan:tcp:22"}
	if got := p.NetRules(); !reflect.DeepEqual(got, wantRules) {
		t.Errorf("NetRules = %v, want %v", got, wantRules)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"unknown field", `{"schema_version":1,"destinations":[],"extra":true}`, "unknown field"},
		{"wrong schema version", `{"schema_version":2,"destinations":[{"alias":"a","host":"h.example","port":22,"user":"u","host_keys":["h ssh-ed25519 AAA"]}]}`, "unsupported schema_version"},
		{"no destinations", `{"schema_version":1,"destinations":[]}`, "at least one destination"},
		{"bad host", `{"schema_version":1,"destinations":[{"alias":"a","host":"*.example.com","port":22,"user":"u","host_keys":["a.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"]}]}`, "invalid host"},
		{"port zero", `{"schema_version":1,"destinations":[{"alias":"a","host":"h.example","port":0,"user":"u","host_keys":["a.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"]}]}`, "out of range"},
		{"bad user", `{"schema_version":1,"destinations":[{"alias":"a","host":"h.example","port":22,"user":"BAD USER","host_keys":["a.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"]}]}`, "invalid user"},
		{"no host keys", `{"schema_version":1,"destinations":[{"alias":"a","host":"h.example","port":22,"user":"u","host_keys":[]}]}`, "pinned host key"},
		{"comment host key", `{"schema_version":1,"destinations":[{"alias":"a","host":"h.example","port":22,"user":"u","host_keys":["# nope"]}]}`, "empty or a comment"},
		{"garbage host key", `{"schema_version":1,"destinations":[{"alias":"a","host":"h.example","port":22,"user":"u","host_keys":["justoneword"]}]}`, "not a known_hosts line"},
		{"trailing garbage", validProfile + "\n{}", "unexpected trailing"},
		{"top-level array", `[1]`, "must be an object"},
		{"empty document", "", "empty document"},
		{"bad alias", `{"schema_version":1,"destinations":[{"alias":"BAD ALIAS","host":"h.example","port":22,"user":"u","host_keys":["a.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"]}]}`, "invalid alias"},
	}
	for _, c := range cases {
		configDir := t.TempDir()
		writeProfile(t, configDir, "homelab", c.doc)
		if _, err := Load(configDir, "homelab"); err == nil {
			t.Errorf("%s: expected an error", c.name)
		} else if c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.wantErr)
		}
	}
}

func TestLoadRejectsDuplicateAliases(t *testing.T) {
	doc := `{
	  "schema_version": 1,
	  "destinations": [
	    {"alias": "nas", "host": "a.example", "port": 22, "user": "u", "host_keys": ["a.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"]},
	    {"alias": "nas", "host": "b.example", "port": 22, "user": "u", "host_keys": ["a.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"]}
	  ]
	}`
	configDir := t.TempDir()
	writeProfile(t, configDir, "homelab", doc)
	if _, err := Load(configDir, "homelab"); err == nil || !strings.Contains(err.Error(), "duplicate alias") {
		t.Errorf("duplicate aliases: err = %v", err)
	}
}

func TestLoadRejectsBadName(t *testing.T) {
	configDir := t.TempDir()
	writeProfile(t, configDir, "homelab", validProfile)
	for _, name := range []string{"", "UPPER", "../escape", "has_underscore", "."} {
		if _, err := Load(configDir, name); err == nil {
			t.Errorf("name %q should be rejected", name)
		}
	}
}

func TestResolveKeyDir(t *testing.T) {
	configDir := t.TempDir()
	realKeyDir := filepath.Join(configDir, "access", "keys", "homelab")
	if err := os.MkdirAll(realKeyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveKeyDir(configDir, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	// macOS temp roots live behind /private; compare canonical forms.
	wantDir, err := filepath.EvalSymlinks(realKeyDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantDir {
		t.Errorf("ResolveKeyDir = %q, want %q", got, wantDir)
	}

	// A symlink that escapes KeyRoot must be refused: this is how ~/.ssh or
	// a workspace directory would sneak in as a credential source.
	outside := t.TempDir()
	os.Remove(realKeyDir)
	if err := os.Symlink(outside, realKeyDir); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveKeyDir(configDir, "homelab"); err == nil {
		t.Error("an escaping symlink should be rejected")
	}
}

func makeKeyDir(t *testing.T, dir string, dirMode, keyMode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, PrivateKeyFile), []byte("key"), keyMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, PublicKeyFile), []byte("pub"), keyMode); err != nil {
		t.Fatal(err)
	}
}

func TestValidateKeyDir(t *testing.T) {
	base := t.TempDir()

	good := filepath.Join(base, "good")
	makeKeyDir(t, good, 0o700, 0o600)
	if err := ValidateKeyDir(good); err != nil {
		t.Errorf("good key dir rejected: %v", err)
	}

	openDir := filepath.Join(base, "opendir")
	makeKeyDir(t, openDir, 0o755, 0o600)
	if err := ValidateKeyDir(openDir); err == nil {
		t.Error("mode-0755 key dir should be rejected")
	}

	openKey := filepath.Join(base, "openkey")
	makeKeyDir(t, openKey, 0o700, 0o644)
	if err := ValidateKeyDir(openKey); err == nil {
		t.Error("mode-0644 private key should be rejected")
	}

	missing := filepath.Join(base, "missing")
	os.MkdirAll(missing, 0o700)
	if err := ValidateKeyDir(missing); err == nil {
		t.Error("missing private key should be rejected")
	}

	// A symlinked private key is not a regular file we can lock down.
	linkDir := filepath.Join(base, "linkdir")
	makeKeyDir(t, linkDir, 0o700, 0o600)
	os.Rename(filepath.Join(linkDir, PrivateKeyFile), filepath.Join(linkDir, "real"))
	os.Symlink(filepath.Join(linkDir, "real"), filepath.Join(linkDir, PrivateKeyFile))
	if err := ValidateKeyDir(linkDir); err == nil {
		t.Error("symlinked private key should be rejected")
	}
}

func TestRenderConfig(t *testing.T) {
	p := &Profile{SchemaVersion: 1, Destinations: []Destination{{
		Alias: "nas", Host: "nas.home.lan", Port: 22, User: "claude",
		HostKeys: []string{"nas.home.lan ssh-ed25519 AAAA"},
	}}}
	cfg := RenderConfig(p)
	for _, want := range []string{
		"IdentitiesOnly yes",
		"IdentityFile /run/ai-sandbox/ssh/id_ed25519",
		"UserKnownHostsFile /run/ai-sandbox/ssh/known_hosts",
		"StrictHostKeyChecking yes",
		"ForwardAgent no",
		"ClearAllForwardings yes",
		"PasswordAuthentication no",
		"Host nas\n    HostName nas.home.lan\n    Port 22\n    User claude",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("rendered config missing %q:\n%s", want, cfg)
		}
	}
}

func TestRenderKnownHostsBareKeyLine(t *testing.T) {
	// A bare "<algo> <key>" line (no host prefix) must get the destination's
	// host prepended: known_hosts matches by the exact name used to connect,
	// and a bare key line would silently never match.
	p := &Profile{SchemaVersion: 1, Destinations: []Destination{{
		Alias: "zazu", Host: "zazu.home.lan", Port: 22, User: "sandbox-ssh",
		HostKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"},
	}}}
	want := "# Generated by ai-sandbox from an access profile; edits are overwritten.\n" +
		"zazu.home.lan ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq\n"
	if got := RenderKnownHosts(p); got != want {
		t.Errorf("RenderKnownHosts = %q, want %q", got, want)
	}
}

func TestRenderKnownHostsSelector(t *testing.T) {
	// Port 22 renders as the bare host; any other port renders as
	// [host]:port, matching how ssh itself matches known_hosts entries.
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq"
	t.Run("port 22 renders the bare host", func(t *testing.T) {
		p := &Profile{SchemaVersion: 1, Destinations: []Destination{{
			Alias: "nas", Host: "nas.home.lan", Port: 22, User: "claude",
			HostKeys: []string{key},
		}}}
		want := "# Generated by ai-sandbox from an access profile; edits are overwritten.\n" +
			"nas.home.lan ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq\n"
		if got := RenderKnownHosts(p); got != want {
			t.Errorf("RenderKnownHosts = %q, want %q", got, want)
		}
	})

	t.Run("port 2222 renders [host]:port", func(t *testing.T) {
		p := &Profile{SchemaVersion: 1, Destinations: []Destination{{
			Alias: "nas", Host: "nas.home.lan", Port: 2222, User: "claude",
			HostKeys: []string{key},
		}}}
		want := "# Generated by ai-sandbox from an access profile; edits are overwritten.\n" +
			"[nas.home.lan]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq\n"
		if got := RenderKnownHosts(p); got != want {
			t.Errorf("RenderKnownHosts = %q, want %q", got, want)
		}
	})
}

func TestLoadRejectsBadHostKeys(t *testing.T) {
	cases := []struct {
		name    string
		hostKey string
		port    int
		wantErr string
	}{
		{"wildcard pattern", "*.home.lan ssh-ed25519 AAAA", 22, "unsupported host pattern"},
		{"negated pattern", "!nas.home.lan ssh-ed25519 AAAA", 22, "unsupported host pattern"},
		{"comma-separated patterns", "nas.home.lan,other ssh-ed25519 AAAA", 22, "unsupported host pattern"},
		{"cert-authority marker", "@cert-authority ssh-ed25519 AAAA", 22, "unsupported host pattern"},
		{"revoked marker", "@revoked ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq", 22, "unsupported host pattern"},
		{"mismatched selector", "other.host ssh-ed25519 AAAA", 22, "does not match the destination selector"},
		{"mismatched non-default selector", "nas.home.lan ssh-ed25519 AAAA", 2222, "does not match the destination selector"},
		{"unknown algorithm", "nas.home.lan ssh-weird AAAA", 22, "unsupported key algorithm"},
		{"malformed key material", "nas.home.lan ssh-ed25519 not!base64!!", 22, "malformed key material"},
		{"too few fields", "ssh-ed25519", 22, "not a known_hosts line"},
		{"too many fields", "nas.home.lan ssh-ed25519 AAAA extra", 22, "not a known_hosts line"},
	}
	for _, c := range cases {
		doc := fmt.Sprintf(`{"schema_version":1,"destinations":[{"alias":"a","host":"nas.home.lan","port":%d,"user":"u","host_keys":[%q]}]}`, c.port, c.hostKey)
		configDir := t.TempDir()
		writeProfile(t, configDir, "homelab", doc)
		if _, err := Load(configDir, "homelab"); err == nil {
			t.Errorf("%s: expected an error", c.name)
		} else if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.wantErr)
		}
	}
}

func TestLoadAcceptsFullAndBareHostKeysOnNonDefaultPort(t *testing.T) {
	for _, hc := range []string{
		`[nas.home.lan]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq`,
		`ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPEqqWmcHasScOzNO2MFtiFY/x1M1WwoTHHS/wb7jISq`,
	} {
		doc := fmt.Sprintf(`{"schema_version":1,"destinations":[{"alias":"a","host":"nas.home.lan","port":2222,"user":"u","host_keys":[%q]}]}`, hc)
		configDir := t.TempDir()
		writeProfile(t, configDir, "homelab", doc)
		if _, err := Load(configDir, "homelab"); err != nil {
			t.Errorf("host_keys %q rejected: %v", hc, err)
		}
	}
}

func TestMaterializeRefusesSymlinkTarget(t *testing.T) {
	p := &Profile{SchemaVersion: 1, Destinations: []Destination{{
		Alias: "nas", Host: "nas.home.lan", Port: 22, User: "claude",
		HostKeys: []string{"nas.home.lan ssh-ed25519 AAAA"},
	}}}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(outside, []byte("original"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "config")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Materialize(dir, p); err == nil {
		t.Fatal("materializing over a symlinked config should be refused")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "original" {
		t.Errorf("symlink target was modified: %q (err %v)", got, err)
	}

	// A directory where a generated file belongs is equally unusable.
	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, "known_hosts"), 0o700)
	if err := Materialize(dir2, p); err == nil {
		t.Fatal("materializing over a directory target should be refused")
	}
}

func TestMaterializeLeavesNoTemporaryFiles(t *testing.T) {
	p := &Profile{SchemaVersion: 1, Destinations: []Destination{{
		Alias: "nas", Host: "nas.home.lan", Port: 22, User: "claude",
		HostKeys: []string{"nas.home.lan ssh-ed25519 AAAA"},
	}}}
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Materialize(dir, p); err != nil {
				t.Errorf("concurrent materialize: %v", err)
			}
		}()
	}
	wg.Wait()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	kh, err := os.ReadFile(filepath.Join(dir, "known_hosts"))
	if err != nil || string(kh) != RenderKnownHosts(p) {
		t.Errorf("known_hosts wrong after concurrent writes (err %v): %q", err, kh)
	}
	info, err := os.Stat(filepath.Join(dir, "config"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600 (err %v)", info.Mode(), err)
	}
}

func TestMaterialize(t *testing.T) {
	p := &Profile{SchemaVersion: 1, Destinations: []Destination{{
		Alias: "nas", Host: "nas.home.lan", Port: 22, User: "claude",
		HostKeys: []string{"nas.home.lan ssh-ed25519 AAAA"},
	}}}
	dir := t.TempDir()
	if err := Materialize(dir, p); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg) != RenderConfig(p) {
		t.Error("materialized config drifted from RenderConfig")
	}
	kh, err := os.ReadFile(filepath.Join(dir, "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	if string(kh) != RenderKnownHosts(p) {
		t.Error("materialized known_hosts drifted from RenderKnownHosts")
	}
	if !strings.HasSuffix(string(kh), "nas.home.lan ssh-ed25519 AAAA\n") {
		t.Errorf("known_hosts = %q", kh)
	}
	info, err := os.Stat(filepath.Join(dir, "config"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600 (err %v)", info.Mode(), err)
	}

	// Materialize is idempotent.
	if err := Materialize(dir, p); err != nil {
		t.Fatalf("second materialize: %v", err)
	}
}
