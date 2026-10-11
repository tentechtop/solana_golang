package main

import (
	"golang.org/x/sys/windows"
	"os/exec"
)

func openURL(address string) {
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address).Start()
}
func showError(message string) {
	title, _ := windows.UTF16PtrFromString("Ordexus Node")
	body, _ := windows.UTF16PtrFromString(message)
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptrPointer(body), uintptrPointer(title), 0x10)
}
