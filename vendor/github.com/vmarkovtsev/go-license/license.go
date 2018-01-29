// Package license provides software license identification.
//
// A number of common license types are built-in, allowing the license
// package to automatically identify them based on their text body.
// Any license type may be used. PR's are very welcome for unrecognized
// license types.
package license

import (
	"bytes"
	"errors"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// Recognized license types
	LicenseMIT       = "MIT"
	LicenseNewBSD    = "BSD-3-Clause"
	LicenseFreeBSD   = "BSD-2-Clause-FreeBSD"
	LicenseApache20  = "Apache-2.0"
	LicenseMPL20     = "MPL-2.0"
	LicenseGPL20     = "GPL-2.0-only"
	LicenseGPL30     = "GPL-3.0-only"
	LicenseLGPL21    = "LGPL-2.1-only"
	LicenseLGPL30    = "LGPL-3.0-only"
	LicenseAGPL30    = "AGPL-3.0-only"
	LicenseCDDL10    = "CDDL-1.0"
	LicenseEPL10     = "EPL-1.0"
	LicenseUnlicense = "Unlicense"
)

var (
	// Various error messages.
	ErrNoLicenseFile       = errors.New("license: unable to find any license file")
	ErrUnrecognizedLicense = errors.New("license: could not guess license type")
	ErrMultipleLicenses    = errors.New("license: multiple license files found")

	// Base names of guessable license files.
	fileNames = []string{
		"copying",
		"copyleft",
		"copyright",
		"license",
		"unlicense",
	}

	// License file extensions. Combined with the licenseFiles slice
	// to create a set of files we can reasonably assume contain
	// licensing information.
	fileExtensions = []string{
		"",
		".md",
		".rst",
		".txt",
	}

	// Lookup tables used for license file names and license types. We
	// use a poor man's set here to get O(1) lookups.
	fileTable    map[string]struct{}
	licenseTable map[string]struct{}

	// Global normalizing helpers
	normalizer = strings.NewReplacer(
		"\r\n", " ", // Windows newline -> space
		"\n", " ", // ANSI newline -> space
		"\t", " ", // Tab stop -> space
		",", "") // Remove commas
	spaceRe = regexp.MustCompile("\\s{2,}")

	// The following exported vars are here for backwards compatibility.
	// In normal usage they should not be required, but the original
	// library exported these, so here they remain.

	// List of license file names to be scanned.
	DefaultLicenseFiles []string

	// List of license identifier strings.
	KnownLicenses []string
)

// init allocates substructures
func init() {
	size := len(fileNames) * len(fileExtensions)

	// Generate the list of known file names.
	fileTable = make(map[string]struct{}, size)
	for _, file := range fileNames {
		for _, ext := range fileExtensions {
			fileTable[file+ext] = struct{}{}
			DefaultLicenseFiles = append(DefaultLicenseFiles, file+ext)
		}
	}

	// Initialize the license types.
	licenseTable = make(map[string]struct{})
	for _, l := range []string{
		LicenseMIT,
		LicenseNewBSD,
		LicenseFreeBSD,
		LicenseApache20,
		LicenseMPL20,
		LicenseGPL20,
		LicenseGPL30,
		LicenseLGPL21,
		LicenseLGPL30,
		LicenseAGPL30,
		LicenseCDDL10,
		LicenseEPL10,
		LicenseUnlicense,
	} {
		licenseTable[l] = struct{}{}
		KnownLicenses = append(KnownLicenses, l)
	}
}

// License describes a software license
type License struct {
	Type string // The type of license in use
	Text string // License text data
	File string // The path to the source file, if any
}

// New creates a new License from explicitly passed license type and data
func New(licenseType, licenseText string) *License {
	return &License{
		Type: licenseType,
		Text: licenseText,
	}
}

// NewFromFile will attempt to load a license from a file on disk, and guess the
// type of license based on the bytes read.
func NewFromFile(path string) (*License, error) {
	licenseText, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	l := &License{
		Text: string(licenseText),
		File: path,
	}

	if err := l.GuessType(); err != nil {
		return nil, err
	}

	return l, nil
}

// NewFromDir will search a directory for well-known and accepted license file
// names, and if one is found, read in its content and guess the license type.
func NewFromDir(dir string) (*License, error) {
	files, err := SearchDir(dir)
	if err != nil {
		return nil, err
	}

	switch len(files) {
	case 0:
		return nil, ErrNoLicenseFile
	case 1:
		path := filepath.Join(dir, files[0])
		return NewFromFile(path)
	default:
		return nil, ErrMultipleLicenses
	}
}

// GuessType will scan license text and attempt to guess what license type it
// describes. It will return the license type on success, or an error if it
// cannot accurately guess the license type.
//
// This method is a hack. It might be more accurate to also scan the entire body
// of license text and compare it using an algorithm like Jaro-Winkler or
// Levenshtein against a generic version. The problem is that some of the
// common licenses, such as GPL-family licenses, are quite large, and running
// these algorithms against them is considerably more expensive and is still not
// completely deterministic on which license is in play. For now, we will just
// scan until we find differentiating strings and call that good-enuf.gov.
func (l *License) GuessType() error {
	// First normalize the license text for accurate comparison.
	comp := normalize(l.Text)

	switch {
	case scan(comp, "permission is hereby granted free of charge to any "+
		"person obtaining a copy of this software"):
		l.Type = LicenseMIT

	case scan(comp, "apache license version 2.0 ") ||
		scan(comp, "http://www.apache.org/licenses/license-2.0"):
		l.Type = LicenseApache20

	// MPL 2.0 must be scanned in before GPL-family licenses
	case scan(comp, "mozilla public license version 2.0 "):
		l.Type = LicenseMPL20

	// Specialized GPL-family licenses must be scanned before the vanilla
	// GPL2/GPL3 licenses.
	case scan(comp, "gnu lesser general public license version 2.1 "):
		l.Type = LicenseLGPL21

	case scan(comp, "gnu lesser general public license version 3 "):
		l.Type = LicenseLGPL30

	case scan(comp, "gnu affero general public license version 3 "):
		l.Type = LicenseAGPL30

	case scan(comp, "gnu general public license version 2 "):
		l.Type = LicenseGPL20

	case scan(comp, "gnu general public license version 3 "):
		l.Type = LicenseGPL30

	case scan(comp, "redistribution and use in source and binary forms"):
		switch {
		case scan(comp, "neither the name of"):
			l.Type = LicenseNewBSD
		default:
			l.Type = LicenseFreeBSD
		}

	case scan(comp, "common development and distribution license (cddl) "+
		"version 1.0 "):
		l.Type = LicenseCDDL10

	case scan(comp, "eclipse public license - v 1.0 "):
		l.Type = LicenseEPL10

	case scan(comp, "this is free and unencumbered software released into "+
		"the public domain"):
		l.Type = LicenseUnlicense

	default:
		return ErrUnrecognizedLicense
	}

	return nil
}

// Recognized determines if the license is known to go-license.
func (l *License) Recognized() bool {
	_, ok := licenseTable[l.Type]
	return ok
}

// SearchDir will scan the given directory for files which match our
// list of known license file names.
func SearchDir(dir string) ([]string, error) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, fi := range files {
		if !fi.Mode().IsRegular() {
			continue
		}
		name := fi.Name()
		lower := strings.ToLower(name)
		if _, ok := fileTable[lower]; ok {
			out = append(out, name)
		}
	}
	return out, nil
}

// scan is used to find substrings. It type-casts to byte slices because
// bytes is an order of magnitude faster than its strings counterpart.
func scan(text, pattern string) bool {
	return bytes.Contains([]byte(text), []byte(pattern))
}

// normalize is used to massage minor differences out of the given string.
func normalize(text string) string {
	// Lower case everything
	text = strings.ToLower(text)

	// Normalize with the global normalizer
	text = normalizer.Replace(text)

	// Multiple spaces to a single space
	text = spaceRe.ReplaceAllLiteralString(text, " ")

	return text
}
