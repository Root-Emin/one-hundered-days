package main

import "os"

// exit exists so TestMain can end the process after closing its shared
// database handle, without importing os into the test file for one call.
func exit(code int) {
	os.Exit(code)
}
