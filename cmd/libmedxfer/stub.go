//go:build !libmedxfer

// Without the libmedxfer tag there is no C library: this file keeps "go build ./..." working
// on any machine, with or without a C compiler. Build the library with scripts/build-lib.ps1
// or scripts/build-lib.sh, which pass the tag.
package main

func main() {}
