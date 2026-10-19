//go:build libmedxfer

// Command libmedxfer is medXfer as a C library, loaded by the Flutter app through dart:ffi
// (bible §10.2 API-01, §10.4; gate G2a). Built with -buildmode=c-shared -tags libmedxfer, by
// scripts/build-lib.ps1 (Windows, Android) or scripts/build-lib.sh (Linux).
//
// Five functions, all strings UTF-8 JSON (B3):
//
//	char* medxfer_start(const char* config)    starts the core; idempotent (B4)
//	char* medxfer_call(const char* request)    {id, action, payload} -> {id, ok, ...}
//	char* medxfer_next_event(int timeout_ms)   an event, NULL after timeout_ms (0: no wait,
//	                                           negative: no limit), {"event":"core_stopped"}
//	                                           once stopped (B9)
//	void  medxfer_stop(void)
//	void  medxfer_free(char* p)                frees every char* returned here (B1)
//
// No Go pointer crosses the boundary: every string is copied by C.CString (B2). The logic is
// in internal/cabi, tested without a C compiler.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"time"
	"unsafe"

	"github.com/Medboy224/medXfer/internal/cabi"
)

func main() {} // required by -buildmode=c-shared

//export medxfer_start
func medxfer_start(config *C.char) *C.char {
	return C.CString(cabi.Start(goString(config)))
}

//export medxfer_call
func medxfer_call(request *C.char) *C.char {
	return C.CString(cabi.Call(goString(request)))
}

//export medxfer_next_event
func medxfer_next_event(timeoutMS C.int) *C.char {
	e, ok := cabi.NextEvent(time.Duration(timeoutMS) * time.Millisecond)
	if !ok {
		return nil
	}
	return C.CString(e)
}

//export medxfer_stop
func medxfer_stop() { cabi.Stop() }

//export medxfer_free
func medxfer_free(p *C.char) { C.free(unsafe.Pointer(p)) }

func goString(p *C.char) string {
	if p == nil {
		return ""
	}
	return C.GoString(p)
}
