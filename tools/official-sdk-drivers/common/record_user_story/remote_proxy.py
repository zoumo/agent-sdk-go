"""Keep a remote connection capture alive through official SDK TERM-after-EOF."""
import signal
import subprocess
import sys


def main():
    if len(sys.argv) < 2:
        return 2
    process = subprocess.Popen(sys.argv[1:], stdin=sys.stdin, stdout=sys.stdout, stderr=sys.stderr)
    def terminate(signum, _frame):
        # The official client closes stdin and immediately sends TERM. The Go
        # recorder must receive EOF and reap the connection bridge first.
        try:
            process.wait(timeout=1.5)
        except subprocess.TimeoutExpired:
            process.send_signal(signum)
    for sig in (signal.SIGTERM, signal.SIGINT):
        signal.signal(sig, terminate)
    return process.wait()


if __name__ == '__main__':
    sys.exit(main())
