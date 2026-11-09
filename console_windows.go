package main

import (
	"bufio"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")

// setupConsole, Türkçe karakterlerin düzgün görünmesi için konsolu UTF-8'e alır.
func setupConsole() {
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
}

// pauseIfOwnConsole, program çift tıklamayla açıldıysa (konsol yalnızca bize
// aitse) hata mesajı okunabilsin diye pencereyi kapatmadan önce bekler.
func pauseIfOwnConsole() {
	var ids [2]uint32
	n, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	if n != 1 {
		return
	}
	fmt.Fprint(os.Stderr, "\nKapatmak için Enter'a basın...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
