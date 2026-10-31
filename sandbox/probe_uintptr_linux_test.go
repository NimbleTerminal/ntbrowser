package sandbox

import "unsafe"

func uintptrOf(p *[120]byte) uintptr { return uintptr(unsafe.Pointer(p)) }
