package main

import (
	"context"

	tuidc "github.com/tui-tools/tui-dc"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/manifest"
)

// backendName is the name the manifest gives the backend this tool drives.
// One program is driven and the manifest declares it once, so no version
// number is written into the code.
const backendName = "samba"

// probeCompat reads samba-tool's version and classifies it against what the
// manifest declares: below the minimum, tested, or merely untested. The result
// goes in the header through ui.CompatFact, and its capability set answers
// caps.Has("feature") for the parts that need a recent samba-tool.
//
// It never fails. A missing binary, a hung process or unparsable output all
// end as the "version unknown" badge, because a compatibility probe that can
// stop a tool from starting is worse than no probe.
func probeCompat(ctx context.Context, demo bool) compat.Result {
	result, _ := probeBackend(ctx, demo)
	return result
}

// probeBackend is probeCompat that also returns the error the version command
// itself ended with, if it ran at all. The compat result keeps only "version
// unknown" for a samba-tool that exists and fails, which is right for a badge
// and not enough for --report: that line must say the same thing the TUI says
// (see samba.CannotRun). It is still the one probe, so the two cannot disagree.
func probeBackend(ctx context.Context, demo bool) (compat.Result, error) {
	// --demo drives an in-memory domain, so probing the host would report a
	// version that has nothing to do with what is on screen.
	if demo {
		return compat.Result{}, nil
	}
	m, err := manifest.Load(tuidc.ManifestJSON)
	if err != nil {
		return compat.Result{}, nil
	}
	backend, ok := m.Backend(backendName)
	if !ok {
		return compat.Result{}, nil
	}
	var runErr error
	exec := compat.RunnerExec(backend.SearchPaths)
	record := func(ctx context.Context, argv []string) (string, error) {
		out, err := exec(ctx, argv)
		runErr = err
		return out, err
	}
	return compat.ProbeWith(ctx, backend, record), runErr
}
