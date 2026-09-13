package rpmrepo

import (
	"errors"
	"fmt"
)

var (
	// ErrNotRepository indicates that the given address is not a valid RPM repository
	// (repodata/repomd.xml cannot be read).
	ErrNotRepository = errors.New("rpmrepo: not a valid rpm repository, cannot read repodata/repomd.xml")

	// ErrNoRepository indicates that no repository address was given: the address is empty and no
	// address is configured with WithRepositories.
	ErrNoRepository = errors.New("rpmrepo: repository address must not be empty")

	// ErrMultipleRepositories indicates that a single-repository function (Open) was called while
	// further repository addresses are configured with WithRepositories; use ListPackages or
	// FindPackages to query several repositories.
	ErrMultipleRepositories = errors.New("rpmrepo: Open reads a single repository, but several repository addresses are configured")

	// ErrPrimaryNotFound indicates that repomd.xml has no primary metadata entry.
	ErrPrimaryNotFound = errors.New("rpmrepo: repomd.xml is missing primary metadata")

	// ErrUnsupportedCompression indicates that the metadata uses an unsupported compression format.
	ErrUnsupportedCompression = errors.New("rpmrepo: unsupported metadata compression format")

	// ErrUnsupportedChecksum indicates that the metadata uses an unsupported checksum algorithm.
	ErrUnsupportedChecksum = errors.New("rpmrepo: unsupported checksum algorithm")

	// ErrChecksumMismatch indicates that the metadata checksum does not match (the download may be
	// incomplete or tampered with).
	ErrChecksumMismatch = errors.New("rpmrepo: metadata checksum mismatch")

	// ErrPackageNotFound indicates that the repository has no matching package; it is returned by
	// FindPackage.
	ErrPackageNotFound = errors.New("rpmrepo: matching package not found")
)

// HTTPError indicates that an HTTP request returned a non-2xx status code.
type HTTPError struct {
	// URL is the request address.
	URL string
	// StatusCode is the HTTP status code.
	StatusCode int
	// Status is the status line, for example "404 Not Found".
	Status string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("rpmrepo: request %s failed: %s", e.URL, e.Status)
}
