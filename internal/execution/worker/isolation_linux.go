//go:build linux

package worker

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

func harden() error {
	// Child commands share our UID, but must not inspect the worker's transport
	// credential or descriptors via procfs, ptrace or process_vm_readv.
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
func monitorCommand(args []string) error {
	if len(args) != 2 || args[0] != "network" && args[0] != "offline" {
		return errors.New("invalid monitor helper arguments")
	}
	binary, err := exec.LookPath("bash")
	if err != nil {
		return err
	}
	var arch uint32
	switch runtime.GOARCH {
	case "amd64":
		arch = unix.AUDIT_ARCH_X86_64
	case "arm64":
		arch = unix.AUDIT_ARCH_AARCH64
	default:
		return errors.New("unsupported monitor architecture")
	}
	// Lock to the exec thread: filters installed on another Go thread would
	// leave the shell unrestricted. The caller must exit if exec fails.
	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	load := func(offset uint32) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: offset}
	}
	jeq := func(k uint32, yes, no uint8) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: k, Jt: yes, Jf: no}
	}
	ret := func(k uint32) unix.SockFilter { return unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: k} }
	deny := uint32(unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM))
	filters := []unix.SockFilter{load(4), jeq(arch, 1, 0), ret(unix.SECCOMP_RET_KILL_PROCESS), load(0)}
	if runtime.GOARCH == "amd64" {
		filters = append(filters, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, K: 0x40000000, Jf: 1}, ret(unix.SECCOMP_RET_KILL_PROCESS))
	}
	for _, nr := range []uint32{unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV, unix.SYS_PIDFD_GETFD, unix.SYS_IO_URING_SETUP} {
		filters = append(filters, jeq(nr, 0, 1), ret(deny))
	}
	if args[0] == "offline" {
		for _, nr := range []uint32{unix.SYS_SOCKET, unix.SYS_SOCKETPAIR} {
			filters = append(filters, jeq(nr, 0, 4), load(16), jeq(unix.AF_UNIX, 1, 0), ret(deny), ret(unix.SECCOMP_RET_ALLOW))
		}
	}
	filters = append(filters, ret(unix.SECCOMP_RET_ALLOW))
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0); err != nil {
		return err
	}
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "THREAVIA_") {
			env = append(env, entry)
		}
	}
	return unix.Exec(binary, []string{binary, "--noprofile", "--norc", "-c", args[1]}, env)
}
