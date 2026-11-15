// Package remote resolves the sources a remotes: section registers into the
// URL and ref to fetch, and fetches them under the fetch policy.
package remote

import (
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/remote/source"
)

// ParseSource resolves a source spelling into the URL to fetch and the ref it
// pins; source.ParseSource documents the forms and their ref semantics.
func ParseSource(raw string) (string, string, error) {
	return source.ParseSource(raw)
}

// ResolveRecord resolves a remotes record into the URL to fetch and the ref it
// pins; source.ResolveRecord carries the precedence rule and the ref semantics.
func ResolveRecord(rec config.RemoteRecord) (string, string, error) {
	return source.ResolveRecord(rec.URL, rec.Ref)
}
