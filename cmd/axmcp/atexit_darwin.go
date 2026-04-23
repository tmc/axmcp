package main

/*
#include <stdlib.h>
#include <stdio.h>
#include <execinfo.h>
#include <signal.h>
#include <unistd.h>
#include <string.h>

// File descriptor for the diagnostic log file, set from Go.
static int diag_log_fd = -1;

static void write_backtrace(const char *prefix) {
    void *bt[30];
    int n = backtrace(bt, 30);

    // Write to stderr.
    fprintf(stderr, "%s\n", prefix);
    backtrace_symbols_fd(bt, n, 2);
    fflush(stderr);

    // Also write to the diagnostic log file if available.
    if (diag_log_fd >= 0) {
        write(diag_log_fd, prefix, strlen(prefix));
        write(diag_log_fd, "\n", 1);
        backtrace_symbols_fd(bt, n, diag_log_fd);
        fsync(diag_log_fd);
    }
}

static void axmcp_atexit_handler(void) {
    // Only write to the diagnostic log file, not stderr, to avoid noisy
    // output on normal exit.
    if (diag_log_fd >= 0) {
        const char *msg = "axmcp: atexit handler — process exiting\n";
        write(diag_log_fd, msg, strlen(msg));
        fsync(diag_log_fd);
    }
}

static void axmcp_signal_handler(int sig) {
    char buf[128];
    snprintf(buf, sizeof(buf), "axmcp: caught signal %d (%s)! backtrace:", sig, strsignal(sig));
    write_backtrace(buf);
    // Re-raise with default handler so the process terminates normally.
    signal(sig, SIG_DFL);
    raise(sig);
}

static void install_atexit(void) {
    atexit(axmcp_atexit_handler);
}

static void install_signal_handlers(void) {
    signal(SIGTERM, axmcp_signal_handler);
    signal(SIGHUP, axmcp_signal_handler);
}

static void set_diag_fd(int fd) {
    diag_log_fd = fd;
}
*/
import "C"

import "os"

func installAtexitHandler() {
	C.install_atexit()
	if os.Getenv("AXMCP_C_SIGNAL_BACKTRACE") == "1" {
		C.install_signal_handlers()
	}
}

// setDiagFd tells the C atexit/signal handlers where to write.
func setDiagFd(fd int) {
	C.set_diag_fd(C.int(fd))
}
