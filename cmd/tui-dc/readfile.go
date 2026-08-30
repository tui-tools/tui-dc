package main

import (
	"fmt"
	"io"
	"os"
)

// maxSmallFile bounds what --report will read off disk. smb.conf is a few
// kilobytes; anything enormous under that name is not a configuration file and
// is not worth loading into a bug report.
const maxSmallFile = 512 << 10

// readSmallFile reads a configuration file, refusing one that is too large or
// is not a regular file. A named pipe at /etc/samba/smb.conf would otherwise
// hang --report forever, which is the one command that has to work everywhere.
func readSmallFile(path string) (string, error) {
	// #nosec G304 -- path comes from smbConfPaths, a fixed list in this
	// package; nothing a user supplies reaches here, which is the point of
	// the list being fixed.
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSmallFile))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
