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
	// --demo drives an in-memory domain, so probing the host would report a
	// version that has nothing to do with what is on screen.
	if demo {
		return compat.Result{}
	}
	m, err := manifest.Load(tuidc.ManifestJSON)
	if err != nil {
		return compat.Result{}
	}
	backend, ok := m.Backend(backendName)
	if !ok {
		return compat.Result{}
	}
	return compat.Probe(ctx, backend)
}
