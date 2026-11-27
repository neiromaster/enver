package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// runInstallSnippet dot-sources the installer for its helpers, then runs the
// snippet. The skip-flow switch keeps the download/PATH flow out of tests.
func runInstallSnippet(t *testing.T, engine, policy, snippet string) string {
	t.Helper()
	stdout, err := runPowerShell(t, engine, policy,
		installScriptText(t)+"\n"+snippet,
		map[string]string{"ENVER_SKIP_INSTALL_FLOW": "1"})
	if err != nil {
		t.Fatalf("install.ps1 + snippet: %v", err)
	}
	return stdout
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
