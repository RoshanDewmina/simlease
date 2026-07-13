// Package errortriple provides a static, AST-based audit that every NewError
// call site passes non-empty string literals for code, message, and fix.
//
// Scope boundary: this check only flags empty *string literals* (""). Calls that
// pass a variable or other expression are not flagged here — those are covered
// by each package's existing runtime NewError validation (broker, provisioner,
// safety, ipc). This package does not replace that validation; it is a
// compile-time / CI safety net for the literal case the runtime cannot catch
// until the path executes.
package errortriple
