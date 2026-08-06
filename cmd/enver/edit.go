package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
)

const (
	actionAdd           = "action:add"
	actionExtends       = "action:extends"
	actionDefault       = "action:default"
	actionDeleteVar     = "action:delete-var"
	actionDeleteProfile = "action:delete-profile"
	actionDone          = "action:done"
)

// editState is the in-memory working copy of the profile being edited: own env
// entries (with comments), the extends pointer, whether it is the default, and a
// pending-delete flag. Nothing is written until commit.
type editState struct {
	name          string
	entries       []ui.EnvEntry
	extends       string
	isDefault     bool
	deleteProfile bool
}

func newEditState(name string, prof config.Profile, comments map[string]string, isDefault bool) editState {
	keys := make([]string, 0, len(prof.Env))
	for k := range prof.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := make([]ui.EnvEntry, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, ui.EnvEntry{Key: k, Value: prof.Env[k], Comment: comments[k]})
	}
	return editState{name: name, entries: entries, extends: prof.Extends, isDefault: isDefault}
}

func (s *editState) find(key string) (ui.EnvEntry, bool) {
	for _, e := range s.entries {
		if e.Key == key {
			return e, true
		}
	}
	return ui.EnvEntry{}, false
}

func (s *editState) upsert(e ui.EnvEntry) {
	e.Key = strings.TrimSpace(e.Key)
	for i := range s.entries {
		if s.entries[i].Key == e.Key {
			s.entries[i] = e
			return
		}
	}
	s.entries = append(s.entries, e)
}

func (s *editState) deleteKey(key string) {
	for i, e := range s.entries {
		if e.Key == key {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

func (s editState) envMap() map[string]string {
	m := make(map[string]string, len(s.entries))
	for _, e := range s.entries {
		if e.Key != "" {
			m[e.Key] = e.Value
		}
	}
	return m
}

func (s editState) commentsMap() map[string]string {
	m := map[string]string{}
	for _, e := range s.entries {
		if e.Key != "" && e.Comment != "" {
			m[e.Key] = e.Comment
		}
	}
	return m
}

func (s editState) canCommit() error {
	if len(s.entries) == 0 && s.extends == "" {
		return fmt.Errorf("a profile needs at least one env var or an extends")
	}
	return nil
}

func (s editState) menuOptions(inherited []ui.EnvEntry) []ui.Option {
	var opts []ui.Option
	for _, e := range s.entries {
		opts = append(opts, ui.Option{Value: e.Key, Label: fmt.Sprintf("%s = %s", e.Key, e.Value)})
	}
	for _, e := range inherited {
		opts = append(opts, ui.Option{Value: "inherited:" + e.Key, Label: fmt.Sprintf("%s = %s (inherited)", e.Key, e.Value)})
	}
	opts = append(opts,
		ui.Option{Value: actionAdd, Label: "＋ Add variable"},
		ui.Option{Value: actionExtends, Label: extendsLabel(s.extends)},
		ui.Option{Value: actionDefault, Label: defaultLabel(s.isDefault)},
		ui.Option{Value: actionDeleteVar, Label: "🗑 Delete variable…"},
		ui.Option{Value: actionDeleteProfile, Label: "⚠ Delete profile…"},
		ui.Option{Value: actionDone, Label: "✓ Done"},
	)
	return opts
}

func extendsLabel(extends string) string {
	if extends == "" {
		return "🔗 Change extends… (none)"
	}
	return fmt.Sprintf("🔗 Change extends… (%s)", extends)
}

func defaultLabel(isDefault bool) string {
	if isDefault {
		return "★ Clear default (current)"
	}
	return "★ Set as default"
}

// parseMenuChoice classifies a selection from menuOptions into a kind and key.
func parseMenuChoice(choice string, s editState) (kind, key string) {
	if strings.HasPrefix(choice, "inherited:") {
		return "inherited", strings.TrimPrefix(choice, "inherited:")
	}
	if _, ok := s.find(choice); ok {
		return "own", choice
	}
	return "action", choice
}
