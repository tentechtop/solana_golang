package main

import "unsafe"

func uintptrPointer(pointer *uint16) uintptr { return uintptr(unsafe.Pointer(pointer)) }
