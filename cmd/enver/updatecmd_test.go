package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/neiromaster/enver/internal/update"
	"github.com/neiromaster/enver/internal/version"
)

// mockGitHub serves the latest-release endpoint and any download paths
// supplied in assets, returning a base URL to point the client at.
func mockGitHub(t *testing.T, tag string, assets map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/neiromaster/enver/releases/latest" {
			_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tag)
			return
		}
		if body, ok := assets[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// pointClientAt routes the update client at the mock server and restores the
// real API bases afterwards.
func pointClientAt(t *testing.T, base string) {
	t.Helper()
	oldAPI, oldDL := update.DefaultAPIBase, update.DefaultDownloadBase
	update.DefaultAPIBase, update.DefaultDownloadBase = base, base
	t.Cleanup(func() { update.DefaultAPIBase, update.DefaultDownloadBase = oldAPI, oldDL })
}

func TestRunUpdateUpToDate(t *testing.T) {
	oldVersion := version.Version
	version.Version = "9.9.9"
	t.Cleanup(func() { version.Version = oldVersion })

	pointClientAt(t, mockGitHub(t, "v0.9.1", nil))
	if err := runUpdate(false); err != nil {
		t.Fatalf("runUpdate = %v, want nil", err)
	}
}

func TestRunUpdateCheckReportsAvailable(t *testing.T) {
	oldVersion := version.Version
	version.Version = "0.0.1"
	t.Cleanup(func() { version.Version = oldVersion })

	pointClientAt(t, mockGitHub(t, "v0.9.1", nil))
	err := runUpdate(true)
	var ce *codedError
	if !errors.As(err, &ce) {
		t.Fatalf("runUpdate err = %v, want codedError", err)
	}
	if ce.code != 1 {
		t.Errorf("exit code = %d, want 1", ce.code)
	}
	if !ce.silent {
		t.Error("--check exit must be silent")
	}
}

func TestRunUpdateCheckError(t *testing.T) {
	pointClientAt(t, mockGitHub(t, "", nil)) // server 404s the release call
	err := runUpdate(true)
	var ce *codedError
	if !errors.As(err, &ce) {
		t.Fatalf("runUpdate err = %v, want codedError", err)
	}
	if ce.code != 2 {
		t.Errorf("exit code = %d, want 2", ce.code)
	}
}

func TestRunUpdateDelegatesToLocalNPM(t *testing.T) {
	oldPath, oldVersion := execPath, version.Version
	exe := filepath.Join("work", "project", "node_modules", "@enver-go", "enver-darwin-arm64", "bin", "enver")
	execPath = func() (string, error) { return exe, nil }
	version.Version = "0.0.1"
	t.Cleanup(func() { execPath, version.Version = oldPath, oldVersion })

	oldOutput := update.Output
	update.Output = func(name string, args ...string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { update.Output = oldOutput })

	var gotName string
	var gotArgs []string
	oldDelegate := delegate
	delegate = func(name string, args []string) error { gotName, gotArgs = name, args; return nil }
	t.Cleanup(func() { delegate = oldDelegate })

	pointClientAt(t, mockGitHub(t, "v0.9.1", nil))
	if err := runUpdate(false); err != nil {
		t.Fatal(err)
	}
	if gotName != "npm" || len(gotArgs) != 4 || gotArgs[0] != "update" {
		t.Errorf("delegate = %s %v, want npm update with --prefix", gotName, gotArgs)
	}
}

func TestRunUpdateDelegateFailureExits2(t *testing.T) {
	oldPath, oldVersion := execPath, version.Version
	execPath = func() (string, error) { return "/usr/local/bin/enver", nil }
	version.Version = "0.0.1"
	t.Cleanup(func() { execPath, version.Version = oldPath, oldVersion })

	// Force brew detection so the update delegates to brew.
	oldLook, oldRun, oldOut := update.LookPath, update.RunCommand, update.Output
	update.LookPath = func(string) (string, error) { return "/usr/local/bin/brew", nil }
	update.RunCommand = func(name string, args ...string) bool {
		return name == "/usr/local/bin/brew" && len(args) == 2 &&
			args[0] == "list" && args[1] == "enver"
	}
	update.Output = func(name string, args ...string) (string, error) { return "/usr/local", nil }
	t.Cleanup(func() { update.LookPath, update.RunCommand, update.Output = oldLook, oldRun, oldOut })

	var gotName string
	var gotArgs []string
	oldDelegate := delegate
	delegate = func(name string, args []string) error {
		gotName, gotArgs = name, args
		return errors.New("brew upgrade failed")
	}
	t.Cleanup(func() { delegate = oldDelegate })

	pointClientAt(t, mockGitHub(t, "v0.9.1", nil))
	err := runUpdate(false)
	var ce *codedError
	if !errors.As(err, &ce) {
		t.Fatalf("runUpdate err = %v, want codedError", err)
	}
	if ce.code != 2 {
		t.Errorf("exit code = %d, want 2", ce.code)
	}
	if gotName != "brew" || len(gotArgs) != 2 || gotArgs[0] != "upgrade" || gotArgs[1] != "enver" {
		t.Errorf("delegate = %s %v, want brew upgrade enver", gotName, gotArgs)
	}
}
