// Package provisioner implements broker.Provisioner against public simctl
// interfaces: clone a Simurgh-owned base (or warm template, when present),
// rename to simurgh-clone[<lease-id>:<model>:<runtime>], boot, and wait for
// Booted.
//
// Warm templates (ARCHITECTURE §6): CreateWarmTemplate primes a shut-down
// device named simurgh-warm[<model>:<runtime>] used as a preferred clone
// source. Provision prefers an existing warm template over the base for the
// same (model, runtime). Removal of warm templates is not in this package —
// callers use safety.DeleteSimulatorClone.
//
// Aside from CreateWarmTemplate's deliberate shutdown of the template it just
// primed, this package performs no destructive simulator operations (no
// delete or erase). Base devices use the simurgh-base[...] name so they never
// match the ^simurgh-(clone|warm)\[.*\]$ deletion-invariant regex.
package provisioner
