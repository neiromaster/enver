package main

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

func TestCompleteProfileForCryptAndDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	want := []string{"dev", "prod", "stage"}
	for _, p := range want {
		if err := config.UpsertProfile(path, p, config.Profile{Env: map[string]string{"A": "1"}}, false, nil); err != nil {
			t.Fatal(err)
		}
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("no-local", false, "")
	_ = cmd.Flags().Set("config", path)
	_ = cmd.Flags().Set("no-local", "true")

	for _, c := range []*cobra.Command{encryptCmd, decryptCmd, defaultCmd} {
		got, dir := c.ValidArgsFunction(cmd, nil, "")
		if dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%s: directive=%v, want NoFileComp", c.Use, dir)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", c.Use, got, want)
		}
	}
}
