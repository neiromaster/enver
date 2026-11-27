package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestFileReadsOnlyTheFile pins the read scope: show --file resolves the one
// file alone, so neither the global config nor the local .enver.yaml leaks into
// the output and every entry presents the file layer as its provenance.
func TestFileReadsOnlyTheFile(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  g:\n    env:\n      FROM_GLOBAL: \"1\"\n")
	s.writeLocal("profiles:\n  l:\n    env:\n      FROM_LOCAL: \"1\"\n")
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      FROM_FILE: \"1\"\n")
	r := s.run("show", "--file", "team.yaml", "t")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "FROM_FILE=1") {
		t.Fatalf("show --file: exit %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	for _, gone := range []string{"FROM_GLOBAL", "FROM_LOCAL"} {
		if strings.Contains(r.Stdout, gone) {
			t.Fatalf("--file leaked %s:\n%s", gone, r.Stdout)
		}
	}
	if !strings.Contains(r.Stdout, "# from t (file)") {
		t.Fatalf("provenance under --file:\n%s", r.Stdout)
	}
}

// TestFileCompletionOffersTheFile pins read-command completion: --file offers
// the file's profiles and not the global config's.
func TestFileCompletionOffersTheFile(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  g:\n    env:\n      A: \"1\"\n")
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	tokens := completionTokens(s.run("__complete", "show", "--file", "team.yaml", "").Stdout)
	if !slices.Contains(tokens, "t") || slices.Contains(tokens, "g") {
		t.Fatalf("completion under --file = %q", tokens)
	}
}

// TestFileWritesGoToTheFile pins the write target: a mutator under --file
// writes the file and never the global config.
func TestFileWritesGoToTheFile(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  g:\n    env:\n      A: \"1\"\n")
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	if r := s.run("default", "--file", "team.yaml", "t"); r.ExitCode != 0 {
		t.Fatalf("default --file: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if cfg := s.readFile(filepath.Join(s.project, "team.yaml")); !strings.Contains(cfg, "default: t") {
		t.Fatalf("the write went elsewhere:\n%s", cfg)
	}
	if strings.Contains(s.readFile(s.configPath()), "default:") {
		t.Fatal("the global config was written")
	}
}

// TestFileWriterKeepsUnknownTopLevelKeys pins the rewrite contract: encrypt
// --file seals the marked values and still keeps the top-level keys it does not
// model.
func TestFileWriterKeepsUnknownTopLevelKeys(t *testing.T) {
	s := newSandbox(t)
	requireKeygen(t, s)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "x-notes: keep\nprofiles:\n  p:\n    env:\n      ~K: v\n")
	if r := s.run("encrypt", "--file", "team.yaml"); r.ExitCode != 0 {
		t.Fatalf("encrypt --file: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if cfg := s.readFile(filepath.Join(s.project, "team.yaml")); !strings.Contains(cfg, "x-notes: keep") || !strings.Contains(cfg, "enc:v3:") {
		t.Fatalf("encrypt --file rewrote the document:\n%s", cfg)
	}
}

// TestFileLayerFlagConflicts pins the refusals: --file cannot be combined with
// the layer-steering flags, on the root or on pull.
func TestFileLayerFlagConflicts(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	for _, c := range []struct {
		args  []string
		names string
	}{
		{[]string{"--file", "team.yaml", "--config", "other.yaml", "show", "t"}, "--config"},
		{[]string{"--file", "team.yaml", "--no-local", "show", "t"}, "--no-local"},
		{[]string{"--file", "team.yaml", "-g", "show", "t"}, "--global"},
		{[]string{"--file", "team.yaml", "pull", "--local", "x"}, "--local"},
	} {
		r := s.run(c.args...)
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, c.names) || !strings.Contains(r.Stderr, "--file") {
			t.Fatalf("%v: exit %d, stderr: %s", c.args, r.ExitCode, r.Stderr)
		}
	}
}

// TestFileRefusesDirectoryAndEnFile pins the unusable paths: a directory is not
// a config file, and a .enver regular file beside X would leave the store
// nowhere to live.
func TestFileRefusesDirectoryAndEnFile(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	for _, args := range [][]string{{"show", "--file", s.project, "t"}, {"default", "--file", s.project, "x"}} {
		r := s.run(args...)
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, "is a directory") {
			t.Fatalf("%v as --file: exit %d, stderr: %s", args, r.ExitCode, r.Stderr)
		}
	}
	s.writeFile(filepath.Join(s.project, ".enver"), "not a directory")
	r := s.run("show", "--file", filepath.Join(s.project, "team.yaml"), "t")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "is not a directory") {
		t.Fatalf("a .enver file beside X: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

// TestFileRelativeToChdir pins the resolution order: --chdir runs first, so a
// relative --file resolves against the new directory.
func TestFileRelativeToChdir(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "sub", "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	r := s.run("--chdir", filepath.Join(s.project, "sub"), "--file", "team.yaml", "show", "t")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "A=1") {
		t.Fatalf("--chdir with a relative --file: exit %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// TestFileMissingStaysMissingOnRead pins the read side of a missing file: it
// reads as empty and no command creates it as a side effect.
func TestFileMissingStaysMissingOnRead(t *testing.T) {
	s := newSandbox(t)
	r := s.run("list", "--file", "new.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "Create one at: new.yaml") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(s.project, "new.yaml")); !os.IsNotExist(err) {
		t.Fatal("a read created the file")
	}
}

// TestFileMissingIsCreatedByPull pins the write side of a missing file: a pull
// creates it with its record and stores the content beside it.
func TestFileMissingIsCreatedByPull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs SSL_CERT_FILE, which Windows does not read")
	}
	s := newSandbox(t)
	srv := serveRepo(t, s, remoteBody)
	created := filepath.Join(s.project, "new.yaml")
	if r := s.run("pull", "--file", created, srv.URL+"/llm.yaml", "--as", "base"); r.ExitCode != 0 {
		t.Fatalf("pull into a missing file: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if cfg := s.readFile(created); !strings.Contains(cfg, "  base:") {
		t.Fatalf("the file was not created with its record:\n%s", cfg)
	}
}

// TestFileHintsNameTheFile pins the hint contract: every pull hint a command
// prints under --file spells `--file <path as typed>`, including the divergence
// warning of a record whose sidecar records another source.
func TestFileHintsNameTheFile(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\nremotes:\n  r:\n    url: https://example.invalid/r.yaml\n")
	store := filepath.Join(s.project, ".enver", "team.yaml")
	s.writeFile(filepath.Join(store, "r.yaml"), "profiles:\n  default:\n    env:\n      R: \"1\"\n")
	s.writeFile(filepath.Join(store, "r.meta"), "url: https://other.example/r.yaml\npulled-at: 2026-01-01T00:00:00Z\n")
	r := s.run("show", "--file", "team.yaml", "t")
	if r.ExitCode != 0 || !strings.Contains(r.Stderr, `run "enver pull --file team.yaml r" to re-sync`) {
		t.Fatalf("the hint does not name the file: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	r = s.run("pull", "--file", "team.yaml", "nope")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "enver pull --file team.yaml <source> --as nope") {
		t.Fatalf("the registration hint does not name the file: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}

	// The placeholder hints an empty registry shows name the file the same way.
	empty := newSandbox(t)
	empty.writeFile(filepath.Join(empty.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	listed := empty.run("pull", "--file", "team.yaml", "--list")
	if listed.ExitCode != 0 || !strings.Contains(listed.Stdout, "register one with enver pull --file team.yaml <source> --as <name>") {
		t.Fatalf("the empty-registry hint does not name the file: exit %d, stdout: %s, stderr: %s", listed.ExitCode, listed.Stdout, listed.Stderr)
	}
	r = empty.run("pull", "--file", "team.yaml", "--as", "x")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "run enver pull --file team.yaml <source> --as <name>") {
		t.Fatalf("the as-without-source hint does not name the file: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

// TestFileHintsCarryTheFlagOutsidePull pins the hint seam beyond pull: every
// hint that spells an enver command line carries `--file <path>` right after
// the verb, so following one under --file stays on the file.
func TestFileHintsCarryTheFlagOutsidePull(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	for _, args := range [][]string{{"show", "--file", "team.yaml"}, {"default", "--file", "team.yaml"}} {
		r := s.run(args...)
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, "set one with `enver default --file team.yaml <profile>`") {
			t.Fatalf("%v: the no-default hint does not name the file: exit %d, stderr: %s", args, r.ExitCode, r.Stderr)
		}
	}
	envfile := filepath.Join(s.project, "secrets.env")
	s.writeFile(envfile, "A=1\n")
	r := s.run("import", "--file", "team.yaml", envfile, "p")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "enver edit --file team.yaml p") || !strings.Contains(r.Stdout, "enver encrypt --file team.yaml p --all") {
		t.Fatalf("the import summary does not name the file: exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// TestFilePullUsesTheStoreBesideIt pins the store split: a pull under --file
// writes the record into the file and the content into the store beside it, the
// shared store under the config home stays untouched, and the shared pull
// --list never sees the file's record. --rm then removes the record and says so.
func TestFilePullUsesTheStoreBesideIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs SSL_CERT_FILE, which Windows does not read")
	}
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  g:\n    env:\n      A: \"1\"\n")
	srv := serveRepo(t, s, remoteBody)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    extends: base/ollama\n")
	r := s.run("pull", "--file", "team.yaml", srv.URL+"/llm.yaml", "--as", "base")
	if r.ExitCode != 0 {
		t.Fatalf("pull --file: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if cfg := s.readFile(filepath.Join(s.project, "team.yaml")); !strings.Contains(cfg, "  base:") || !strings.Contains(cfg, "url: "+srv.URL+"/llm.yaml") {
		t.Fatalf("the record was not written into the file:\n%s", cfg)
	}
	if _, err := os.Stat(filepath.Join(s.project, ".enver", "team.yaml", "base.yaml")); err != nil {
		t.Fatalf("the store beside the file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.remotesDir(), "base.yaml")); err == nil {
		t.Fatal("the shared store was written")
	}
	shown := s.run("show", "--file", "team.yaml", "t")
	if shown.ExitCode != 0 || !strings.Contains(shown.Stdout, "OLLAMA_HOST=http://localhost:11434") || !strings.Contains(shown.Stdout, "(remote:base)") {
		t.Fatalf("show --file after pull: exit %d, %q, stderr: %s", shown.ExitCode, shown.Stdout, shown.Stderr)
	}
	// The global profile g must be absent from the listing; the check reads the
	// profile column of every line rather than substring-matching g anywhere,
	// and the file's own profile t anchors the scan on a listing that printed
	// rows, so the absence claim cannot pass vacuously.
	listed := s.run("list", "--file", "team.yaml")
	if listed.ExitCode != 0 {
		t.Fatalf("list --file: exit %d, %q, stderr: %s", listed.ExitCode, listed.Stdout, listed.Stderr)
	}
	sawT := false
	for _, line := range strings.Split(strings.TrimRight(listed.Stdout, "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == "*" {
			f = f[1:]
		}
		if len(f) > 0 && f[0] == "t" {
			sawT = true
		}
		if len(f) > 0 && f[0] == "g" {
			t.Fatalf("list --file offers the global profile:\n%s", listed.Stdout)
		}
	}
	if !sawT {
		t.Fatalf("list --file never showed the file's own profile in the scan:\n%s", listed.Stdout)
	}
	if seen := s.run("pull", "--list"); seen.ExitCode != 0 || strings.Contains(seen.Stdout, "base") {
		t.Fatalf("the shared pull --list saw the file's record: %q", seen.Stdout)
	}
	if rm := s.run("pull", "--file", "team.yaml", "--rm", "base"); rm.ExitCode != 0 || !strings.Contains(rm.Stdout, `removed remote "base" from the file team.yaml`) {
		t.Fatalf("pull --rm --file: exit %d, stdout: %s, stderr: %s", rm.ExitCode, rm.Stdout, rm.Stderr)
	}
	if cfg := s.readFile(filepath.Join(s.project, "team.yaml")); strings.Contains(cfg, "  base:") {
		t.Fatalf("the record survived removal:\n%s", cfg)
	}
}

// TestKeygenForceLosesShorthand pins the shorthand move: -f is the value-taking
// --file shorthand now, so keygen -f without a value is a flag parse error
// where it used to run a forced keygen, and the long flag still works.
func TestKeygenForceLosesShorthand(t *testing.T) {
	s := newSandbox(t)
	if r := s.run("keygen", "-f"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "flag needs an argument") {
		t.Fatalf("keygen -f: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if ok := s.run("keygen", "--force", "--random"); ok.ExitCode != 0 {
		t.Fatalf("keygen --force --random: exit %d, stderr: %s", ok.ExitCode, ok.Stderr)
	}
}

// TestFileCompletionOffersTheFileToMutators pins strict mutator completion:
// remove under --file offers the write target's profiles, not the global
// config's and not a remote-projected name, which it would then refuse. add and
// edit are the exception — they bootstrap a patch copy over such a name.
func TestFileCompletionOffersTheFileToMutators(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  g:\n    env:\n      A: \"1\"\n")
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\nremotes:\n  r:\n    url: https://example.invalid/r.yaml\n")
	store := filepath.Join(s.project, ".enver", "team.yaml")
	s.writeFile(filepath.Join(store, "r.yaml"), "profiles:\n  default:\n    env:\n      R: \"1\"\n")
	tokens := completionTokens(s.run("__complete", "remove", "--file", "team.yaml", "").Stdout)
	if !slices.Contains(tokens, "t") || slices.Contains(tokens, "g") || slices.Contains(tokens, "r/default") {
		t.Fatalf("completion under --file = %q", tokens)
	}
}

// TestFileCompletionOffersTheFileToImportExtends pins the extends flag
// completion: import under --file offers parents from the file, not the global
// config.
func TestFileCompletionOffersTheFileToImportExtends(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  g:\n    env:\n      A: \"1\"\n")
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\n")
	tokens := completionTokens(s.run("__complete", "import", "--file", "team.yaml", "p", "--extends", "").Stdout)
	if !slices.Contains(tokens, "t") || slices.Contains(tokens, "g") {
		t.Fatalf("completion under --file = %q", tokens)
	}
}

// TestFileCompletionOffersTheFileToPull pins record completion: pull under
// --file offers the file's records, not the global config's.
func TestFileCompletionOffersTheFileToPull(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("remotes:\n  g:\n    url: https://example.invalid/g.yaml\n")
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\nremotes:\n  r:\n    url: https://example.invalid/r.yaml\n")
	tokens := completionTokens(s.run("__complete", "pull", "--file", "team.yaml", "").Stdout)
	if !slices.Contains(tokens, "r") || slices.Contains(tokens, "g") {
		t.Fatalf("completion under --file = %q", tokens)
	}
}
