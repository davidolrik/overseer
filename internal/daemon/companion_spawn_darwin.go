package daemon

/*
#include <fcntl.h>
#include <spawn.h>
#include <stdlib.h>
#include <sys/types.h>

// Private SPI exported by libquarantine (reexported via libSystem, so no extra
// linker flags are required). Disclaiming responsibility makes the spawned
// child its own "responsible process" for macOS TCC attribution, rather than
// inheriting the daemon's responsible process (which via responsible-process
// inheritance can resolve to whatever app launched the terminal that launched
// the daemon — frequently misattributed, e.g. to Raycast).
//
// Chromium, LLDB, Qt Creator, and Firefox all use this symbol the same way.
extern int responsibility_spawnattrs_setdisclaim(posix_spawnattr_t *attrs, int disclaim);

static int overseer_spawn_disclaimed(
    pid_t *out_pid,
    const char *path,
    const char *workdir,
    char *const argv[],
    char *const envp[]
) {
    posix_spawnattr_t attr;
    posix_spawn_file_actions_t fa;
    int rc;

    rc = posix_spawnattr_init(&attr);
    if (rc != 0) return rc;

    rc = responsibility_spawnattrs_setdisclaim(&attr, 1);
    if (rc != 0) {
        posix_spawnattr_destroy(&attr);
        return rc;
    }

    // POSIX_SPAWN_SETSID starts the child in a new session, matching the
    // SysProcAttr{Setsid: true} the exec.Cmd path uses on Linux and what the
    // daemon relies on for survive-parent-death during hot reload.
    rc = posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETSID);
    if (rc != 0) {
        posix_spawnattr_destroy(&attr);
        return rc;
    }

    rc = posix_spawn_file_actions_init(&fa);
    if (rc != 0) {
        posix_spawnattr_destroy(&attr);
        return rc;
    }

    // Bind stdio to /dev/null — matches exec.Cmd's default when Stdin/Stdout/
    // Stderr are left unset. The wrapper writes output via a Unix socket, not
    // via inherited stdio.
    rc = posix_spawn_file_actions_addopen(&fa, 0, "/dev/null", O_RDONLY, 0);
    if (rc == 0) rc = posix_spawn_file_actions_addopen(&fa, 1, "/dev/null", O_WRONLY, 0);
    if (rc == 0) rc = posix_spawn_file_actions_addopen(&fa, 2, "/dev/null", O_WRONLY, 0);
    if (rc != 0) {
        posix_spawn_file_actions_destroy(&fa);
        posix_spawnattr_destroy(&attr);
        return rc;
    }

    if (workdir != NULL && workdir[0] != '\0') {
        // The non-_np `posix_spawn_file_actions_addchdir` is macOS 26.0+; the
        // _np variant covers older releases and is what we target. Suppress
        // the deprecation notice since we can't switch without dropping older macOS.
        #pragma clang diagnostic push
        #pragma clang diagnostic ignored "-Wdeprecated-declarations"
        rc = posix_spawn_file_actions_addchdir_np(&fa, workdir);
        #pragma clang diagnostic pop
        if (rc != 0) {
            posix_spawn_file_actions_destroy(&fa);
            posix_spawnattr_destroy(&attr);
            return rc;
        }
    }

    rc = posix_spawn(out_pid, path, &fa, &attr, argv, envp);

    posix_spawn_file_actions_destroy(&fa);
    posix_spawnattr_destroy(&attr);
    return rc;
}
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// spawnCompanionWrapper starts the companion wrapper via posix_spawn with
// responsibility disclaimed. The returned *exec.Cmd has only Process populated
// — callers use it solely for Wait/Kill, which both route through os.Process
// and therefore work regardless of how the child was spawned.
func spawnCompanionWrapper(path string, argv []string, envv []string, workdir string) (*exec.Cmd, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cArgv := makeCStringArray(argv)
	defer freeCStringArray(cArgv)

	cEnvv := makeCStringArray(envv)
	defer freeCStringArray(cEnvv)

	var cWorkdir *C.char
	if workdir != "" {
		cWorkdir = C.CString(workdir)
		defer C.free(unsafe.Pointer(cWorkdir))
	}

	var pid C.pid_t
	rc := C.overseer_spawn_disclaimed(
		&pid,
		cPath,
		cWorkdir,
		(**C.char)(unsafe.Pointer(&cArgv[0])),
		(**C.char)(unsafe.Pointer(&cEnvv[0])),
	)
	if rc != 0 {
		return nil, fmt.Errorf("posix_spawn %q: %w", path, syscall.Errno(rc))
	}

	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return nil, fmt.Errorf("find spawned process %d: %w", int(pid), err)
	}

	return &exec.Cmd{
		Path:    path,
		Args:    argv,
		Env:     envv,
		Dir:     workdir,
		Process: proc,
	}, nil
}

// makeCStringArray converts a Go string slice into a NULL-terminated C array
// of C strings, as required by posix_spawn's argv/envp parameters.
func makeCStringArray(strs []string) []*C.char {
	arr := make([]*C.char, len(strs)+1)
	for i, s := range strs {
		arr[i] = C.CString(s)
	}
	arr[len(strs)] = nil
	return arr
}

func freeCStringArray(arr []*C.char) {
	for _, p := range arr {
		if p != nil {
			C.free(unsafe.Pointer(p))
		}
	}
}
