#!/bin/sh
# Run an already-built Linux credentials test binary against an isolated keyring.
set -eu
if [ "${1:-}" != --inside ]; then
    exec dbus-run-session -- sh "$0" --inside "$@"
fi
shift
task_test_binary=$(realpath "${1:-.clash-tokens/credentials-linux.test}")
task_scratch=$(mktemp -d /tmp/cot-keyring-test-XXXXXXXX)
export XDG_DATA_HOME="$task_scratch/data"
export XDG_RUNTIME_DIR="$task_scratch/runtime"
mkdir -p "$XDG_DATA_HOME" "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
task_daemon_binary=${COT_KEYRING_DAEMON:-gnome-keyring-daemon}
printf 'synthetic-test-only-keyring-password' | "$task_daemon_binary" --foreground --unlock --components=secrets > "$task_scratch/daemon.log" 2>&1 &
task_daemon=$!
finish() {
    kill "$task_daemon" 2>/dev/null || true
    wait "$task_daemon" 2>/dev/null || true
    case "$task_scratch" in /tmp/cot-keyring-test-*) rm -rf -- "$task_scratch";; esac
}
trap finish EXIT
task_attempt=0
until dbus-send --session --dest=org.freedesktop.secrets --type=method_call --print-reply /org/freedesktop/secrets org.freedesktop.DBus.Peer.Ping >/dev/null 2>&1; do
    task_attempt=$((task_attempt + 1))
    if [ "$task_attempt" -ge 30 ]; then
        echo 'Isolated Secret Service did not start.' >&2
        exit 1
    fi
    sleep 0.1
done
COT_TEST_NATIVE_KEYRING=1 "$task_test_binary" -test.v -test.timeout=30s
