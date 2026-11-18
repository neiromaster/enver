package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/neiromaster/enver/internal/app"
	"github.com/spf13/cobra"
)

// pullListNow is the clock the freshness listing ages a pull against; tests pin
// it so the rendered age is stable.
var pullListNow = time.Now

// runPullList is the read-only listing mode: it refuses every flag that changes
// something, then prints the records the merged view declares with the
// freshness of their stored content.
func runPullList(cmd *cobra.Command, args []string) error {
	switch {
	case pullAs != "":
		return fmt.Errorf("--list only reads the registry; --as names a record being registered")
	case pullNoPrefix:
		return fmt.Errorf("--list only reads the registry; --no-prefix sets how a registration stores its profiles")
	case pullRm:
		return fmt.Errorf("--list only reads the registry; --rm removes a record instead")
	case pullForce:
		return fmt.Errorf("--list only reads the registry; --force accepts a takeover instead")
	case pullLocal || globalFlags.global:
		return fmt.Errorf("--list reads the merged view; -g/--local select the layer a registration writes")
	case len(args) > 0:
		return fmt.Errorf("--list takes no source or remote name")
	}
	cfg, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	return printRemoteStatuses(cmd.OutOrStdout(), app.RemoteStatuses(cfg))
}

// printRemoteStatuses renders one line per record: the name as the key, its
// source plain so it copies cleanly, and the freshness of its stored content as
// a dim comment. A record declared by another record's stored content indents
// two spaces per level under its declarer, so the listing reads as the tree a
// pull stores. The color gate follows the writer, exactly as enver show does,
// so a redirect or NO_COLOR keeps the listing escape-free.
func printRemoteStatuses(w io.Writer, rows []app.RemoteStatus) error {
	var dim, bold lipgloss.Style
	if colorEnabled(w) {
		dim, bold = dimStyle, boldStyle
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, dim.Render("# no remotes registered; register one with enver pull <source> --as <name>"))
		return err
	}
	if _, err := fmt.Fprintln(w, dim.Render(fmt.Sprintf("# remotes: %d", len(rows)))); err != nil {
		return err
	}
	now := pullListNow()
	for _, row := range rows {
		indent := strings.Repeat("  ", max(row.Depth-1, 0))
		line := indent + bold.Render(row.Name) + "  " + row.Source + "  " + dim.Render("# "+freshnessNote(row, now))
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// freshnessNote is the dim comment after one record: its pin state and what the
// sidecar records about the last pull. An unpinned record leads with that,
// because the commit it was last pulled at is the only fixed point its content
// has.
func freshnessNote(row app.RemoteStatus, now time.Time) string {
	var parts []string
	if !row.Pinned {
		parts = append(parts, "unpinned")
	}
	switch {
	case row.Pulled:
		parts = append(parts, commitNote(row))
		parts = append(parts, pulledNote(row, now))
	case row.Stored:
		parts = append(parts, "no pull state; run enver pull "+row.Name)
	default:
		parts = append(parts, "never pulled")
	}
	if row.Stale {
		parts = append(parts, "stored content came from "+row.StaleFrom)
	}
	return strings.Join(parts, " · ")
}

// commitNote names the last pull's commit sha, labelled for a pinned record as
// the commit the content is and for an unpinned one as the commit it was last
// pulled at.
func commitNote(row app.RemoteStatus) string {
	switch {
	case row.Commit == "":
		return "commit not recorded"
	case row.Pinned:
		return "commit " + row.Commit
	default:
		return "last pulled commit " + row.Commit
	}
}

// pulledNote names when the last pull ran and how long ago that was.
func pulledNote(row app.RemoteStatus, now time.Time) string {
	if row.PulledAt.IsZero() {
		return "pull time not recorded"
	}
	return "pulled " + row.PulledAt.UTC().Format("2006-01-02 15:04 UTC") + " (" + pullAge(now.Sub(row.PulledAt)) + ")"
}

// pullAge renders a duration the way a freshness listing reads: coarse enough
// to scan, and never a precision the pull time does not have.
func pullAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
