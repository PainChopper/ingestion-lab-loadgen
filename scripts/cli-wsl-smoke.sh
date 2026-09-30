#!/usr/bin/env bash

set -euo pipefail

keep_artifacts=false
if (($# == 1)) && [[ $1 == "--keep-artifacts" ]]; then
	keep_artifacts=true
elif (($# != 0)); then
	printf 'usage: %s [--keep-artifacts]\n' "$0" >&2
	exit 2
fi

for command in go curl mktemp; do
	if ! command -v "$command" >/dev/null 2>&1; then
		printf 'required command is unavailable: %s\n' "$command" >&2
		exit 1
	fi
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/.." && pwd -P)
module_root=$repo_root/loadgen
runtime_root=$(cd -- "$repo_root/.." && pwd -P)/ingestion-lab-agents-runtime
build_root=$runtime_root/BUILD

if [[ ! -d $build_root ]]; then
	printf 'runtime BUILD directory is unavailable: %s\n' "$build_root" >&2
	exit 1
fi

cd -- "$module_root"

workdir=$(mktemp -d "$build_root/V0002-cli-wsl-smoke.XXXXXX")
workdir=$(cd -- "$workdir" && pwd -P)
build_root=$(cd -- "$build_root" && pwd -P)
service_pid=
passed=false

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

assert_workdir_guard() {
	[[ $(dirname -- "$workdir") == "$build_root" ]] || fail "workdir is not a direct BUILD child: $workdir"
	[[ $(basename -- "$workdir") == V0002-cli-wsl-smoke.* ]] || fail "workdir has an unexpected name: $workdir"
}

cleanup() {
	local deadline

	if [[ -n $service_pid ]] && kill -0 "$service_pid" 2>/dev/null; then
		kill -TERM "$service_pid" 2>/dev/null || true
		deadline=$((SECONDS + 5))
		while kill -0 "$service_pid" 2>/dev/null && ((SECONDS < deadline)); do
			sleep 0.1
		done
		if kill -0 "$service_pid" 2>/dev/null; then
			kill -KILL "$service_pid" 2>/dev/null || true
		fi
	fi
	if [[ -n $service_pid ]]; then
		wait "$service_pid" 2>/dev/null || true
	fi

	if [[ $passed == true && $keep_artifacts == false ]]; then
		assert_workdir_guard
		rm -rf -- "$workdir"
	else
		printf 'artifacts preserved at %s\n' "$workdir" >&2
	fi
}

trap cleanup EXIT
trap 'exit 130' INT TERM

binary=$workdir/ingestion-lab-loadgen
fixture_generator=$workdir/generate_fixture.go
fixture=$workdir/input.parquet
config=$workdir/config.toml
service_log=$workdir/service.log
base_url=http://127.0.0.1:8080

run_capture() {
	local name=$1
	shift

	if "$@" >"$workdir/$name.stdout" 2>"$workdir/$name.stderr"; then
		printf '0' >"$workdir/$name.exit"
	else
		printf '%s' "$?" >"$workdir/$name.exit"
	fi
}

assert_exit() {
	local name=$1
	local expected=$2
	local actual

	actual=$(<"$workdir/$name.exit")
	[[ $actual == "$expected" ]] || fail "$name exit=$actual, want $expected"
}

assert_stdout() {
	local name=$1
	local expected=$2

	printf '%s' "$expected" | cmp -s - "$workdir/$name.stdout" || fail "$name stdout differs"
}

assert_stderr_empty() {
	local name=$1

	[[ ! -s $workdir/$name.stderr ]] || fail "$name stderr is not empty"
}

assert_stderr_contains() {
	local name=$1
	local expected=$2

	grep -Fq -- "$expected" "$workdir/$name.stderr" || fail "$name stderr lacks: $expected"
}

assert_stdout_empty() {
	local name=$1

	[[ ! -s $workdir/$name.stdout ]] || fail "$name stdout is not empty"
}

snapshot_expect() {
	local name=$1
	local expected=$2

	run_capture "$name" "$binary" snapshot --url "$base_url"
	assert_exit "$name" 0
	assert_stderr_empty "$name"
	grep -Fq -- "$expected" "$workdir/$name.stdout" || fail "$name snapshot lacks: $expected"
}

cat >"$fixture_generator" <<'EOF'
package main

import (
	"os"
	"time"

	"github.com/parquet-go/parquet-go"
)

type Transaction struct {
	ClientID     string    `parquet:"client_id"`
	EventTime    time.Time `parquet:"event_time"`
	Amount       float32   `parquet:"amount"`
	EventType    int32     `parquet:"event_type"`
	EventSubtype int32     `parquet:"event_subtype"`
	Currency     int32     `parquet:"currency"`
	SrcType11    int32     `parquet:"src_type11"`
	SrcType12    int32     `parquet:"src_type12"`
	DstType11    int32     `parquet:"dst_type11"`
	DstType12    int32     `parquet:"dst_type12"`
	SrcType21    int32     `parquet:"src_type21"`
	SrcType22    int32     `parquet:"src_type22"`
	SrcType31    int32     `parquet:"src_type31"`
	SrcType32    int32     `parquet:"src_type32"`
	Fold         int32     `parquet:"fold"`
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: generate_fixture <path>")
	}
	if err := parquet.WriteFile(os.Args[1], []Transaction{{ClientID: "cli-wsl-smoke"}}); err != nil {
		panic(err)
	}
}
EOF

cat >"$config" <<EOF
schema_version = 2

[source]
path = "$workdir/*.parquet"
unit = "glob-pattern"
mutability = "startup-only"

[reader.read_batch_size]
default = 1_000
min = 1_000
max = 100_000
step = 1_000
unit = "transactions"
mutability = "idle-only"

[reader.workers]
default = 1
min = 1
max = 7
step = 1
unit = "workers"
mutability = "immediate"

[readerChannel.capacity]
default = 2
allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]
unit = "batches"
mutability = "idle-only"

[senderChannel.capacity]
default = 0
allowed = [0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]
unit = "batches"
mutability = "idle-only"

[throttler.requested_tps]
default = 2_000_000
min = 0
max = 4_000_000
step = 200_000
unit = "transactions/s"
mutability = "immediate"

[throttler.installation_mode]
default = "installed"
allowed = ["installed", "bypass"]
mutability = "immediate"

[sender.workers]
default = 32
min = 1
max = 32
step = 1
unit = "workers"
mutability = "immediate"

[sender.api]
url = "http://127.0.0.1:8080/internal/test/ingest"
mutability = "startup-only"

[sender.retry]
delays_ms = [250, 500, 1000, 2000, 5000]
jitter_percent = 20
mutability = "startup-only"

[metrics.window_ms]
default = 1_000
min = 100
max = 10_000
step = 100
unit = "milliseconds"
mutability = "startup-only"

[logging]
level = "info"
mutability = "startup-only"
EOF

go run "$fixture_generator" "$fixture"
go build -o "$binary" .

"$binary" serve --config "$config" >"$service_log" 2>&1 &
service_pid=$!
deadline=$((SECONDS + 10))
while true; do
	if ! kill -0 "$service_pid" 2>/dev/null; then
		cat "$service_log" >&2 || true
		fail "service exited before readiness"
	fi
	if curl --fail --connect-timeout 1 --max-time 1 "$base_url/api/loadgen/snapshot" >"$workdir/readiness.json" 2>"$workdir/readiness.stderr"; then
		break
	fi
	if ((SECONDS >= deadline)); then
		cat "$service_log" >&2 || true
		fail "service did not become ready within 10 seconds"
	fi
	sleep 0.1
done

run_capture help "$binary" --help
assert_exit help 0
assert_stderr_empty help
grep -Fq -- 'usage:' "$workdir/help.stdout" || fail 'help stdout lacks usage:'

snapshot_expect snapshot_idle '"state":"idle"'

run_capture set_reader_workers "$binary" set reader-workers 2 --url "$base_url"
assert_exit set_reader_workers 0
assert_stderr_empty set_reader_workers
assert_stdout set_reader_workers $'action=set target=reader-workers value=2 state=idle\n'
snapshot_expect snapshot_reader_workers '"workers":2'

run_capture set_sender_workers "$binary" set sender-workers 31 --url "$base_url"
assert_exit set_sender_workers 0
assert_stderr_empty set_sender_workers
assert_stdout set_sender_workers $'action=set target=sender-workers value=31 state=idle\n'
snapshot_expect snapshot_sender_workers '"workers":31'

run_capture set_requested_tps "$binary" set requested-tps 200000 --url "$base_url"
assert_exit set_requested_tps 0
assert_stderr_empty set_requested_tps
assert_stdout set_requested_tps $'action=set target=requested-tps value=200000 state=idle\n'
snapshot_expect snapshot_requested_tps '"requestedTps":200000'

run_capture set_throttler_mode "$binary" set throttler-mode bypass --url "$base_url"
assert_exit set_throttler_mode 0
assert_stderr_empty set_throttler_mode
assert_stdout set_throttler_mode $'action=set target=throttler-mode value=bypass state=idle\n'
snapshot_expect snapshot_throttler_mode '"installationMode":"bypass"'

run_capture invalid_requested_tps "$binary" set requested-tps 101 --url "$base_url"
assert_exit invalid_requested_tps 3
assert_stdout_empty invalid_requested_tps
assert_stderr_contains invalid_requested_tps 'HTTP 400'
assert_stderr_contains invalid_requested_tps 'POST /api/loadgen/commands'
assert_stderr_contains invalid_requested_tps 'Invalid requested TPS'
snapshot_expect snapshot_after_http_failure '"requestedTps":200000'

run_capture invalid_requested_tps_syntax "$binary" set requested-tps not-an-integer --url "$base_url"
assert_exit invalid_requested_tps_syntax 2
assert_stdout_empty invalid_requested_tps_syntax
assert_stderr_contains invalid_requested_tps_syntax 'usage:'
snapshot_expect snapshot_after_syntax_failure '"requestedTps":200000'

for action in run pause resume pause reset; do
	run_capture "lifecycle_$action" "$binary" "$action" --url "$base_url"
	assert_exit "lifecycle_$action" 0
	assert_stderr_empty "lifecycle_$action"
	case $action in
		run|resume) expected_state=running ;;
		pause) expected_state=paused ;;
		reset) expected_state=idle ;;
	esac
	assert_stdout "lifecycle_$action" "action=$action state=$expected_state"$'\n'
	snapshot_expect "snapshot_$action" "\"state\":\"$expected_state\""
done

passed=true
printf 'PASS: CLI WSL smoke completed\n'
