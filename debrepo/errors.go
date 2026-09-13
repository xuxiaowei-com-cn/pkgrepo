package debrepo

import (
	"errors"
	"fmt"
)

var (
	// ErrNotRepository indicates that the given address is not a valid deb repository
	// (Release/InRelease cannot be read).
	ErrNotRepository = errors.New("debrepo: not a valid deb repository, cannot read Release/InRelease")

	// ErrSuiteRequired indicates that no distribution suite was specified.
	// The suite can be a suite name (stable, testing) or a codename (bookworm, jammy).
	ErrSuiteRequired = errors.New("debrepo: no distribution suite specified, use debrepo.WithSuite (for example bookworm, jammy, stable)")

	// ErrNoRepository indicates that no repository address was given: the address is empty and no
	// address is configured with WithRepositories.
	ErrNoRepository = errors.New("debrepo: repository address must not be empty")

	// ErrMultipleRepositories indicates that a single-repository function (Open) was called while
	// further repository addresses are configured with WithRepositories; use ListPackages,
	// FindPackages, ListSources, or FindSources to query several repositories.
	ErrMultipleRepositories = errors.New("debrepo: Open reads a single repository, but several repository addresses are configured")

	// ErrIndexNotFound indicates that no usable Packages or Sources index was found in the repository.
	ErrIndexNotFound = errors.New("debrepo: no Packages/Sources index found in the repository")

	// ErrUnsupportedCompression indicates that the index uses an unsupported compression format.
	ErrUnsupportedCompression = errors.New("debrepo: unsupported index compression format")

	// ErrUnsupportedChecksum indicates that the index uses an unsupported checksum algorithm.
	ErrUnsupportedChecksum = errors.New("debrepo: unsupported checksum algorithm")

	// ErrChecksumMismatch indicates that the index checksum does not match (the download may be
	// incomplete or tampered with).
	ErrChecksumMismatch = errors.New("debrepo: index checksum mismatch")

	// ErrPackageNotFound indicates that the repository has no matching package; it is returned by
	// FindPackage.
	ErrPackageNotFound = errors.New("debrepo: matching package not found")

	// ErrInvalidVersion indicates an invalid Debian version number.
	ErrInvalidVersion = errors.New("debrepo: invalid Debian version number")

	// ErrInvalidControl indicates invalid deb822 (control) data.
	ErrInvalidControl = errors.New("debrepo: invalid deb822 control file")

	// ErrInvalidRelation indicates an invalid dependency relation expression.
	ErrInvalidRelation = errors.New("debrepo: invalid dependency relation")

	// ErrInvalidPackage indicates that a Packages/Sources entry is missing a required field.
	ErrInvalidPackage = errors.New("debrepo: invalid package entry")
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
	return fmt.Sprintf("debrepo: request %s failed: %s", e.URL, e.Status)
}

// NotFound reports whether the error is an HTTP 404 (used for the by-hash fallback).
func (e *HTTPError) NotFound() bool { return e != nil && e.StatusCode == 404 }
