//go:build llvm15 || llvm16 || llvm17 || llvm18 || llvm19 || llvm20 || llvm21 || llvm22 || llvm23

package llvm

/*
#cgo CPPFLAGS: -I/usr/lib/llvm-22/include -I/usr/lib/llvm-18/include
#include <llvm-c/Core.h>
*/
import "C"

import (
	"unsafe"

	"tinygo.org/x/go-llvm"
)

// aggElem is element i of a constant aggregate, including the packed
// ConstantDataArray form go-llvm has no accessor for (LLVMGetAggregateElement).
// go-llvm's Value is a struct holding one LLVMValueRef, converted here
// through unsafe since cgo types of two packages differ.
func aggElem(v llvm.Value, i int) llvm.Value {
	ref := *(*C.LLVMValueRef)(unsafe.Pointer(&v))
	e := C.LLVMGetAggregateElement(ref, C.unsigned(i))
	var r llvm.Value
	*(*C.LLVMValueRef)(unsafe.Pointer(&r)) = e
	return r
}
