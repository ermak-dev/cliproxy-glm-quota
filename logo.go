package main

import (
	_ "embed"
	"encoding/base64"
)

//go:embed assets/logo.png
var logoPNG []byte

// logoDataURI is computed once, not hand-copied as a base64 blob in source:
// the committed asset (assets/logo.png) stays the single source of truth,
// and this can never drift out of sync with it.
var logoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(logoPNG)
