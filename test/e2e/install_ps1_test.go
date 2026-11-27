package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	enverCompletionMarker = "# enver completions (added by install.ps1)"
	enverCompletionAdvice = "Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"
)

// psQuote renders a value as a PowerShell single-quoted literal.
func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// runInstallSnippetEnv is runInstallSnippet with extra child environment, so a
// test can pin the home ladder the installer reads.
func runInstallSnippetEnv(t *testing.T, engine, policy, snippet string, env map[string]string) string {
	t.Helper()
	stdout, err := runPowerShell(t, engine, policy, installScriptText(t)+"\n"+snippet, env)
	if err != nil {
		t.Fatalf("install.ps1 + snippet: %v", err)
	}
	return stdout
}

// runInstallSnippet dot-sources the installer for its helpers, then runs the
// snippet. The skip-flow switch keeps the download/PATH flow out of tests.
func runInstallSnippet(t *testing.T, engine, policy, snippet string) string {
	t.Helper()
	return runInstallSnippetEnv(t, engine, policy, snippet,
		map[string]string{"ENVER_SKIP_INSTALL_FLOW": "1"})
}

// outputLines splits captured host output into non-blank lines, dropping the
// carriage returns and byte order mark a console host may add.
func outputLines(stdout string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimPrefix(stdout, "\ufeff"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// normalizeNewlines folds Windows line endings so file content compares the
// same on every host.
func normalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// uninstallSandbox describes a temp install tree plus the home ladder the
// config-dir note reads. Nothing here touches the real PATH or profile.
type uninstallSandbox struct {
	installDir  string
	installRoot string
	completion  string
	profile     string
	home        string
	exe         string
}

func newUninstallSandbox(t *testing.T, dir string) uninstallSandbox {
	t.Helper()
	s := uninstallSandbox{
		installDir: filepath.Join(dir, "enver", "bin"),
		profile:    filepath.Join(dir, "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		home:       filepath.Join(dir, "home"),
	}
	s.installRoot = filepath.Dir(s.installDir)
	s.completion = filepath.Join(s.installRoot, "completions", "enver.ps1")
	s.exe = filepath.Join(s.installDir, "enver.exe")
	return s
}

// plant writes the artifacts an install leaves behind: the exe, the completion
// script, and a profile with the marker block between foreign lines.
func (s uninstallSandbox) plant(t *testing.T) (before, after string) {
	t.Helper()
	before = "Set-Location C:\\work"
	after = "Write-Host 'ready'"
	for _, path := range []string{s.exe, s.completion} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("stub"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.profile), 0o755); err != nil {
		t.Fatal(err)
	}
	ifLine := fmt.Sprintf("if (Test-Path \"%s\") { . \"%s\" }", s.completion, s.completion)
	wired := before + "\n" + enverCompletionMarker + "\n" + ifLine + "\n" + after + "\n"
	if err := os.WriteFile(s.profile, []byte(wired), 0o600); err != nil {
		t.Fatal(err)
	}
	return before, after
}

// uninstallEnv pins the home ladder and keeps the install flow off, so the
// function runs on explicit sandbox paths only.
func (s uninstallSandbox) env() map[string]string {
	return map[string]string{
		"ENVER_SKIP_INSTALL_FLOW": "1",
		"HOME":                    s.home,
		"USERPROFILE":             s.home,
		"XDG_CONFIG_HOME":         "",
	}
}

// configDir is where the user-data note points with this sandbox home.
func (s uninstallSandbox) configDir() string {
	return filepath.Join(s.home, ".config", "enver")
}

// userDataLine is the exact closing note the function prints after a change.
func (s uninstallSandbox) userDataLine() string {
	return fmt.Sprintf(`enver: user data at %s was kept - remove it with: Remove-Item -Recurse -Force "%s"`,
		s.configDir(), s.configDir())
}

// runUninstall drives the uninstall function with explicit sandbox paths and
// returns its host output lines plus the returned PATH value ("<null>" when it
// returned nothing). A thrown error fails the test.
func runUninstall(t *testing.T, engine string, env map[string]string, installDir, profile, userPath string) ([]string, string) {
	t.Helper()
	snippet := fmt.Sprintf(`$result = Invoke-EnverUninstall -InstallDir %s -ProfilePath %s -UserPath %s
if ($null -eq $result) { Write-Host 'result=<null>' } else { Write-Host ('result=' + $result) }
`, psQuote(installDir), psQuote(profile), psQuote(userPath))
	stdout := runInstallSnippetEnv(t, engine, "Bypass", snippet, env)

	lines := outputLines(stdout)
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], "result=") {
		t.Fatalf("no result line at the end of stdout:\n%s", stdout)
	}
	result := strings.TrimPrefix(lines[len(lines)-1], "result=")
	return lines[:len(lines)-1], result
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestAddEnverCompletionCreatesProfileLine(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		profile := filepath.Join(dir, "PowerShell", "Microsoft.PowerShell_profile.ps1")
		completion := filepath.Join(dir, "enver", "completions", "enver.ps1")

		runInstallSnippet(t, engine, "Bypass", fmt.Sprintf(
			"Add-EnverCompletion -CompletionScript %s -ProfilePath %s\n",
			psQuote(completion), psQuote(profile)))

		content := readFileString(t, profile)
		if !strings.Contains(content, enverCompletionMarker) {
			t.Fatalf("profile has no marker line, got:\n%s", content)
		}
		want := fmt.Sprintf("if (Test-Path \"%s\") { . \"%s\" }", completion, completion)
		if !strings.Contains(content, want) {
			t.Fatalf("profile has no dot-source line %q, got:\n%s", want, content)
		}
	})
}

func TestAddEnverCompletionIdempotent(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		profile := filepath.Join(dir, "profile.ps1")
		completion := filepath.Join(dir, "completions", "enver.ps1")
		call := fmt.Sprintf("Add-EnverCompletion -CompletionScript %s -ProfilePath %s\n",
			psQuote(completion), psQuote(profile))

		runInstallSnippet(t, engine, "Bypass", call+call)

		content := readFileString(t, profile)
		if got := strings.Count(content, enverCompletionMarker); got != 1 {
			t.Fatalf("marker line appears %d times, want 1:\n%s", got, content)
		}
	})
}

func TestAddEnverCompletionRestrictedPolicyPrintsAdvice(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("off Windows every engine reports Unrestricted, so the advice path cannot trigger; windows-latest covers it")
	}
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		completion := filepath.Join(dir, "completions", "enver.ps1")

		restricted := filepath.Join(dir, "restricted.ps1")
		stdout := runInstallSnippet(t, engine, "Restricted", fmt.Sprintf(
			"Add-EnverCompletion -CompletionScript %s -ProfilePath %s\n",
			psQuote(completion), psQuote(restricted)))
		if !strings.Contains(stdout, enverCompletionAdvice) {
			t.Fatalf("stdout has no advice under Restricted, got:\n%s", stdout)
		}
		if !strings.Contains(readFileString(t, restricted), enverCompletionMarker) {
			t.Fatal("profile was not wired under Restricted")
		}

		remoteSigned := filepath.Join(dir, "remote-signed.ps1")
		stdout = runInstallSnippet(t, engine, "RemoteSigned", fmt.Sprintf(
			"Add-EnverCompletion -CompletionScript %s -ProfilePath %s\n",
			psQuote(completion), psQuote(remoteSigned)))
		if strings.Contains(stdout, "Set-ExecutionPolicy") {
			t.Fatalf("RemoteSigned still prints advice, got:\n%s", stdout)
		}
		if !strings.Contains(readFileString(t, remoteSigned), enverCompletionMarker) {
			t.Fatal("profile was not wired under RemoteSigned")
		}
	})
}

func TestRemoveEnverCompletion(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		completion := filepath.Join(dir, "completions", "enver.ps1")
		ifLine := fmt.Sprintf("if (Test-Path \"%s\") { . \"%s\" }", completion, completion)
		before := "Set-Location C:\\work"
		after := "Write-Host 'ready'"
		profile := filepath.Join(dir, "profile.ps1")
		wired := before + "\n" + enverCompletionMarker + "\n" + ifLine + "\n" + after + "\n"
		if err := os.WriteFile(profile, []byte(wired), 0o600); err != nil {
			t.Fatal(err)
		}

		runInstallSnippet(t, engine, "Bypass", fmt.Sprintf(
			"Remove-EnverCompletion -ProfilePath %s\n", psQuote(profile)))

		content := readFileString(t, profile)
		if strings.Contains(content, enverCompletionMarker) || strings.Contains(content, ifLine) {
			t.Fatalf("completion block survived:\n%s", content)
		}
		if !strings.Contains(content, before) || !strings.Contains(content, after) {
			t.Fatalf("foreign lines were not preserved:\n%s", content)
		}

		untouched := before + "\n" + after + "\n"
		missing := filepath.Join(dir, "missing.ps1")
		if err := os.WriteFile(missing, []byte(untouched), 0o600); err != nil {
			t.Fatal(err)
		}

		runInstallSnippet(t, engine, "Bypass", fmt.Sprintf(
			"Remove-EnverCompletion -ProfilePath %s\n", psQuote(missing)))

		if got := readFileString(t, missing); got != untouched {
			t.Fatalf("file without a marker was rewritten:\n%q\nwant:\n%q", got, untouched)
		}

		sole := filepath.Join(dir, "sole.ps1")
		if err := os.WriteFile(sole, []byte(enverCompletionMarker+"\n"+ifLine+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		runInstallSnippet(t, engine, "Bypass", fmt.Sprintf(
			"Remove-EnverCompletion -ProfilePath %s\n", psQuote(sole)))

		if got := readFileString(t, sole); strings.TrimSpace(got) != "" {
			t.Fatalf("profile holding only the block was not emptied:\n%q", got)
		}
	})
}

func TestGetEnverCompletionScriptPath(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		installDir := filepath.Join(dir, "enver", "bin")

		stdout := runInstallSnippet(t, engine, "Bypass", fmt.Sprintf(
			"Write-Output ('path=' + (Get-EnverCompletionScriptPath -InstallDir %s))\n",
			psQuote(installDir)))

		want := "path=" + filepath.Join(dir, "enver", "completions", "enver.ps1")
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout lacks %q, got:\n%s", want, stdout)
		}
	})
}

func TestRemoveEnverPathSegmentExactOnly(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		snippet := `$value = 'C:\a;C:\tools\enver;D:\enver\bin;C:\b'
Write-Output ('removed=' + (Remove-EnverPathSegment -PathValue $value -Segment 'D:\enver\bin'))
Write-Output ('absent=' + (Remove-EnverPathSegment -PathValue $value -Segment 'C:\missing'))
`
		stdout := runInstallSnippet(t, engine, "Bypass", snippet)

		for _, want := range []string{
			`removed=C:\a;C:\tools\enver;C:\b`,
			`absent=C:\a;C:\tools\enver;D:\enver\bin;C:\b`,
		} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("stdout lacks %q, got:\n%s", want, stdout)
			}
		}
	})
}

func TestUninstallRemovesAllEnverArtifacts(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		s := newUninstallSandbox(t, dir)
		before, after := s.plant(t)
		userPath := "C:\\Windows;" + s.installDir + ";C:\\Tools"
		userData := filepath.Join(s.configDir(), "config.yaml")
		if err := os.MkdirAll(s.configDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(userData, []byte("profiles: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		messages, result := runUninstall(t, engine, s.env(), s.installDir, s.profile, userPath)

		wantMessages := []string{
			"enver: removed " + s.exe,
			"enver: removed " + s.completion,
			"enver: removed " + filepath.Dir(s.completion),
			"enver: removed the enver completion from " + s.profile,
			"enver: removed " + s.installDir + " from your user PATH",
			"enver: removed " + s.installRoot,
			s.userDataLine(),
		}
		if !slices.Equal(messages, wantMessages) {
			t.Fatalf("output lines:\n%q\nwant:\n%q", messages, wantMessages)
		}
		if result != "C:\\Windows;C:\\Tools" {
			t.Fatalf("returned PATH = %q, want %q", result, "C:\\Windows;C:\\Tools")
		}

		for _, gone := range []string{s.exe, s.completion, filepath.Dir(s.completion), s.installRoot} {
			if _, err := os.Stat(gone); !os.IsNotExist(err) {
				t.Fatalf("%s survived uninstall (stat err: %v)", gone, err)
			}
		}

		wantProfile := before + "\n" + after + "\n"
		if got := normalizeNewlines(readFileString(t, s.profile)); got != wantProfile {
			t.Fatalf("profile:\n%q\nwant:\n%q", got, wantProfile)
		}
		if got := readFileString(t, userData); got != "profiles: {}\n" {
			t.Fatalf("user data changed to %q", got)
		}
	})
}

func TestUninstallUserDataNoteHonorsXDG(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		s := newUninstallSandbox(t, dir)
		if err := os.MkdirAll(s.installDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.exe, []byte("stub"), 0o600); err != nil {
			t.Fatal(err)
		}
		xdg := filepath.Join(dir, "xdg")
		env := s.env()
		env["XDG_CONFIG_HOME"] = xdg

		messages, _ := runUninstall(t, engine, env, s.installDir, s.profile, "")

		configDir := filepath.Join(xdg, "enver")
		wantMessages := []string{
			"enver: removed " + s.exe,
			"enver: removed " + s.installRoot,
			fmt.Sprintf(`enver: user data at %s was kept - remove it with: Remove-Item -Recurse -Force "%s"`,
				configDir, configDir),
		}
		if !slices.Equal(messages, wantMessages) {
			t.Fatalf("output lines:\n%q\nwant:\n%q", messages, wantMessages)
		}
	})
}

func TestUninstallKeepsForeignFiles(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		s := newUninstallSandbox(t, dir)
		s.plant(t)
		notes := filepath.Join(s.installRoot, "notes.txt")
		if err := os.WriteFile(notes, []byte("keep me"), 0o600); err != nil {
			t.Fatal(err)
		}
		userPath := "C:\\Windows;" + s.installDir

		messages, result := runUninstall(t, engine, s.env(), s.installDir, s.profile, userPath)

		wantMessages := []string{
			"enver: removed " + s.exe,
			"enver: removed " + s.completion,
			"enver: removed " + filepath.Dir(s.completion),
			"enver: removed the enver completion from " + s.profile,
			"enver: removed " + s.installDir + " from your user PATH",
			"enver: kept " + s.installRoot + " because it holds files enver did not install",
			s.userDataLine(),
		}
		if !slices.Equal(messages, wantMessages) {
			t.Fatalf("output lines:\n%q\nwant:\n%q", messages, wantMessages)
		}
		if result != "C:\\Windows" {
			t.Fatalf("returned PATH = %q, want %q", result, "C:\\Windows")
		}

		if _, err := os.Stat(s.exe); !os.IsNotExist(err) {
			t.Fatalf("exe survived uninstall (stat err: %v)", err)
		}
		if _, err := os.Stat(s.completion); !os.IsNotExist(err) {
			t.Fatalf("completion script survived uninstall (stat err: %v)", err)
		}
		if got := readFileString(t, notes); got != "keep me" {
			t.Fatalf("foreign file changed to %q", got)
		}
		if _, err := os.Stat(s.installRoot); err != nil {
			t.Fatalf("install root was removed despite a foreign file: %v", err)
		}
	})
}

func TestUninstallIdempotent(t *testing.T) {
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		s := newUninstallSandbox(t, dir)
		s.plant(t)
		userPath := "C:\\Windows;" + s.installDir

		_, firstResult := runUninstall(t, engine, s.env(), s.installDir, s.profile, userPath)
		if firstResult != "C:\\Windows" {
			t.Fatalf("first run returned PATH = %q, want %q", firstResult, "C:\\Windows")
		}

		messages, secondResult := runUninstall(t, engine, s.env(), s.installDir, s.profile, firstResult)
		if len(messages) != 0 {
			t.Fatalf("second run printed %q, want no output", messages)
		}
		if secondResult != "<null>" {
			t.Fatalf("second run returned %q, want <null>", secondResult)
		}
	})
}

func TestUninstallLockedExeFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a no-share file handle only blocks deletes on Windows; windows-latest covers it")
	}
	forEachPowerShellEngine(t, func(t *testing.T, engine, dir string) {
		s := newUninstallSandbox(t, dir)
		s.plant(t)
		userPath := "C:\\Windows;" + s.installDir
		lockFileExclusive(t, s.exe)

		snippet := fmt.Sprintf("Invoke-EnverUninstall -InstallDir %s -ProfilePath %s -UserPath %s\n",
			psQuote(s.installDir), psQuote(s.profile), psQuote(userPath))
		stdout, err := runPowerShell(t, engine, "Bypass", installScriptText(t)+"\n"+snippet, s.env())
		if err == nil {
			t.Fatalf("uninstall succeeded while enver.exe was locked; stdout:\n%s", stdout)
		}
		// A console host may wrap the error line, so compare with whitespace
		// folded instead of raw bytes.
		flat := strings.Join(strings.Fields(err.Error()), " ")
		if want := "enver: enver.exe is in use - close terminals running enver and rerun"; !strings.Contains(flat, want) {
			t.Fatalf("error lacks %q:\n%v", want, err)
		}
		if got := strings.TrimSpace(stdout); got != "" {
			t.Fatalf("a failed uninstall changed something before aborting, output:\n%s", stdout)
		}

		if _, err := os.Stat(s.exe); err != nil {
			t.Fatalf("locked exe disappeared: %v", err)
		}
		if _, err := os.Stat(s.completion); err != nil {
			t.Fatalf("completion script was removed before the abort: %v", err)
		}
		content := normalizeNewlines(readFileString(t, s.profile))
		if !strings.Contains(content, enverCompletionMarker) {
			t.Fatalf("profile block was removed before the abort:\n%s", content)
		}
	})
}
