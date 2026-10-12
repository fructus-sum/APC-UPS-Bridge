package fileutil

// syncDir is a no-op on Windows, which cannot fsync a directory handle. The
// service only runs on Linux; this keeps development builds and tests working.
func syncDir(string) error { return nil }
