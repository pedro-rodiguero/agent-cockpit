package source

import (
	"context"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processCommandLine is the NtQueryInformationProcess class that returns a process's command line
// (ProcessCommandLineInformation, Windows 8.1 and later).
const processCommandLine = 60

// listProcs reads the process table from a toolhelp snapshot, and each process's command line from the
// process itself. A process of another account, or a protected one, gives only its program's name.
func listProcs(context.Context) (map[int]Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	procs := map[int]Proc{}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		pid := int(e.ProcessID)
		args := []string{windows.UTF16ToString(e.ExeFile[:])}
		if line := commandLine(e.ProcessID); line != "" {
			if argv, err := windows.DecomposeCommandLine(line); err == nil && len(argv) > 0 {
				args = argv
			}
		}
		procs[pid] = Proc{PID: pid, PPID: int(e.ParentProcessID), Args: strings.Join(args, " "), Cmd: commandName(args)}
	}
	return procs, nil
}

// commandLine is the command line pid was started with, "" when Windows does not let this process read it.
func commandLine(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var need uint32
	_ = windows.NtQueryInformationProcess(h, processCommandLine, nil, 0, &need)
	if need < uint32(unsafe.Sizeof(windows.NTUnicodeString{})) || need > 1<<20 {
		return ""
	}
	// The answer is a UNICODE_STRING followed by the text it points to, so the buffer must be aligned
	// for a pointer: a slice of uint64 is.
	buf := make([]uint64, (need+7)/8)
	if windows.NtQueryInformationProcess(h, processCommandLine, unsafe.Pointer(&buf[0]), need, &need) != nil {
		return ""
	}
	return (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String()
}

// openFiles is empty on Windows, where one process cannot list the files another has open without
// debugging rights: Codex and agy sessions are then matched to their processes by folder alone.
func openFiles(context.Context, int) []string { return nil }

// procCwd is unknown on Windows: a process's folder is only in its own memory.
func procCwd(context.Context, int) string { return "" }
