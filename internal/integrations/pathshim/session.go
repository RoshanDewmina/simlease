package pathshim

import "path/filepath"

const (
	sessionSubdir = "pathshim"
	binSubdir     = "bin"
	auditLogName  = "audit.log"
)

// SessionPaths are the ephemeral, session-scoped PATH-shim artifacts under
// one lease root — mirrors internal/integrations/xcodebuildmcp.AdapterDir's
// layout convention (one small subdir per integration under the lease
// root, never mixed with the lease's own DerivedData/ModuleCache/etc).
type SessionPaths struct {
	Root     string
	BinDir   string
	AuditLog string
}

// SessionDir returns <leaseRoot>/pathshim and its derived paths. It does
// not create anything; callers create BinDir when materializing shims.
func SessionDir(leaseRoot string) SessionPaths {
	root := filepath.Join(leaseRoot, sessionSubdir)
	return SessionPaths{
		Root:     root,
		BinDir:   filepath.Join(root, binSubdir),
		AuditLog: filepath.Join(root, auditLogName),
	}
}
