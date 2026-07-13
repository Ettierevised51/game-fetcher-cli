//go:build !unix

package cli

func statfsFree(string) (int64, bool) { return 0, false }
