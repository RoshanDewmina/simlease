// Package isolator manages per-lease environment directories and build-flag emission.
//
// Isolation flag/env matrix adapted from VibeChard's seven-resource pattern
// (repo: VibeChard, license: Apache-2.0) — pattern reuse with attribution;
// no wholesale code copying. See docs/COMPETITOR_NOTES.md §1.
//
// Of the seven resources: DerivedData, ModuleCache, SwiftPM cache, and
// xcresult bundles are owned here; simulator devices are provisioner's job
// (M1.2); the PATH shim is deferred to v0.1; source-tree isolation
// (git worktree) is the calling agent's concern.
package isolator
